// main.go — SSM node-shell data-channel POC probe.
//
// Proves the transport composition behind issue #105's "node shell"
// feature, in isolation from auth — the same way hack/poc-exec-tunnel
// proved the exec-over-tunnel *path* while stubbing auth with a dev
// cookie. Here auth/STS is stubbed by the laptop's ambient AWS
// credentials (default chain: AWS_PROFILE / SSO / env), so the probe
// can focus on the one genuinely-novel-to-us piece:
//
//	ambient creds → ssm:StartSession → session-manager-plugin → bytes
//
// We deliberately do NOT reimplement the SSM message-gateway (MGS)
// binary protocol. session-manager-plugin is the AWS-maintained
// reference implementation (Apache-2.0); production composes it rather
// than owning a reverse-engineered wire format — exactly as
// poc-exec-tunnel reused client-go's exec machinery instead of
// reimplementing the exec protocol. This probe proves the composition
// is driveable: StartSession handshake, interactive byte round-trip,
// transcript capture (the audit shape), idle-timeout kill, and clean
// TerminateSession.
//
// What production layers on top (all low-risk, all out of scope here):
// per-user sts:AssumeRoleWithWebIdentity from the user's OIDC id_token,
// the IAM trust policy as source of truth, the FreshIDToken egress
// seam, and the providerID gate.
//
// Usage:
//
//	go run . --instance-id i-0abc... --region us-east-1            # interactive
//	go run . --instance-id i-0abc... --region us-east-1 --assert   # automated round-trip
//	go run . --instance-id i-0abc... --idle-seconds 30             # demo idle-kill
//
// Exit codes: 0 PASS, 1 FAIL, 2 usage.
package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

const defaultPlugin = "session-manager-plugin"

func main() {
	instanceID := flag.String("instance-id", "", "target EC2 instance id (required, e.g. i-0abc123)")
	region := flag.String("region", "", "AWS region (defaults to the resolved default-chain region)")
	document := flag.String("document-name", "", "SSM session document (default: the account's SSM-SessionManagerRunShell)")
	assert := flag.Bool("assert", false, "automated mode: send an echo token, assert it round-trips, exit")
	idleSeconds := flag.Int("idle-seconds", 0, "kill the session after N seconds with no stdin/stdout activity (0 = disabled)")
	transcriptMax := flag.Int64("transcript-max-bytes", 1<<20, "cap on the captured transcript buffer")
	plugin := flag.String("plugin", defaultPlugin, "path to session-manager-plugin")
	timeout := flag.Duration("timeout", 0, "overall wall-clock timeout (0 = none)")
	// Per-user impersonation stage (opt-in). Without --id-token-file the
	// probe uses ambient creds (no Periscope / no OIDC needed). With it,
	// the probe exchanges the OIDC id_token for per-user creds via
	// sts:AssumeRoleWithWebIdentity — the production auth model.
	idTokenFile := flag.String("id-token-file", "", "path to a file containing an OIDC id_token (JWT); enables per-user STS impersonation")
	roleARN := flag.String("role-arn", "", "IAM role to assume via web identity (required with --id-token-file)")
	expectedAud := flag.String("expected-aud", "", "if set, fail fast unless the id_token's aud matches (the #1 trust-policy gotcha)")
	sessionName := flag.String("role-session-name", "", "override the STS roleSessionName (default: derived from the token's sub)")
	groupsClaim := flag.String("groups-claim", "", "id_token claim name to display group membership from (IdP-specific, e.g. a namespaced Auth0 claim); display only")
	flag.Parse()

	if *instanceID == "" {
		fmt.Fprintln(os.Stderr, "ssm-poc: --instance-id is required")
		flag.Usage()
		os.Exit(2)
	}
	if _, err := exec.LookPath(*plugin); err != nil {
		fmt.Fprintf(os.Stderr, "ssm-poc: %q not found in PATH — install the AWS Session Manager plugin\n", *plugin)
		fmt.Fprintln(os.Stderr, "  https://docs.aws.amazon.com/systems-manager/latest/userguide/session-manager-working-with-install-plugin.html")
		os.Exit(2)
	}
	if *idTokenFile != "" && *roleARN == "" {
		fmt.Fprintln(os.Stderr, "ssm-poc: --role-arn is required when --id-token-file is set")
		os.Exit(2)
	}

	opts := probeOpts{
		instanceID:    *instanceID,
		region:        *region,
		document:      *document,
		assert:        *assert,
		idle:          time.Duration(*idleSeconds) * time.Second,
		transcriptMax: *transcriptMax,
		plugin:        *plugin,
		timeout:       *timeout,
		idTokenFile:   *idTokenFile,
		roleARN:       *roleARN,
		expectedAud:   *expectedAud,
		sessionName:   *sessionName,
		groupsClaim:   *groupsClaim,
	}
	if err := run(opts); err != nil {
		fmt.Fprintf(os.Stderr, "ssm-poc: FAIL: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("ssm-poc: PASS")
}

type probeOpts struct {
	instanceID    string
	region        string
	document      string
	assert        bool
	idle          time.Duration
	transcriptMax int64
	plugin        string
	timeout       time.Duration
	idTokenFile   string
	roleARN       string
	expectedAud   string
	sessionName   string
	groupsClaim   string
}

// assertToken is the sentinel we echo and look for in --assert mode.
// Long enough not to collide with a shell prompt or MOTD.
const assertToken = "PERISCOPE-SSM-POC-OK-9f3c1d20"

func run(o probeOpts) error {
	// Ctrl-C cancels the whole probe (and so kills the child + fires
	// TerminateSession via the deferred cleanup below).
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if o.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, o.timeout)
		defer cancel()
	}

	// --- 1. AWS config (ambient default chain — the auth stub) ---
	cfgOpts := []func(*config.LoadOptions) error{}
	if o.region != "" {
		cfgOpts = append(cfgOpts, config.WithRegion(o.region))
	}
	cfg, err := config.LoadDefaultConfig(ctx, cfgOpts...)
	if err != nil {
		return fmt.Errorf("load aws config: %w", err)
	}
	if cfg.Region == "" {
		return errors.New("no AWS region resolved — pass --region or set AWS_REGION")
	}
	fmt.Printf("ssm-poc: region=%s instance=%s\n", cfg.Region, o.instanceID)

	// --- 1b. Per-user impersonation (opt-in via --id-token-file) ---
	// This is the production auth model, exercised from the laptop: the
	// OIDC id_token → sts:AssumeRoleWithWebIdentity → per-user creds. The
	// IAM trust policy on the role is the source of truth; if the token's
	// claims don't satisfy it, this call fails and no session is created.
	if o.idTokenFile != "" {
		creds, err := assumeViaWebIdentity(ctx, cfg, o)
		if err != nil {
			return err
		}
		// Every downstream AWS call (StartSession/TerminateSession) now
		// runs as the user's assumed role, not ambient creds.
		cfg.Credentials = credentials.NewStaticCredentialsProvider(
			creds.AccessKeyID, creds.SecretAccessKey, creds.SessionToken)
	} else {
		fmt.Println("ssm-poc: auth=ambient (default credential chain; no OIDC/STS — pass --id-token-file to exercise per-user impersonation)")
	}

	client := ssm.NewFromConfig(cfg)

	// --- 2. StartSession (the handshake under test) ---
	in := &ssm.StartSessionInput{Target: &o.instanceID}
	if o.document != "" {
		in.DocumentName = &o.document
	}
	out, err := client.StartSession(ctx, in)
	if err != nil {
		return fmt.Errorf("ssm:StartSession (check creds, ssm:StartSession perm, and that the SSM agent on %s is Online): %w", o.instanceID, err)
	}
	sessionID := derefn(out.SessionId)
	fmt.Printf("ssm-poc: session started id=%s\n", sessionID)

	// Always tear the session down, even on error/ctrl-c. Use a fresh
	// context — the request ctx may already be cancelled.
	defer func() {
		tctx, tcancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer tcancel()
		if _, terr := client.TerminateSession(tctx, &ssm.TerminateSessionInput{SessionId: out.SessionId}); terr != nil {
			fmt.Fprintf(os.Stderr, "ssm-poc: warn: TerminateSession: %v\n", terr)
		} else {
			fmt.Println("ssm-poc: session terminated cleanly")
		}
	}()

	// --- 3. Drive session-manager-plugin ---
	// Arg shape matches the AWS CLI's invocation: the plugin opens the
	// MGS data channel from the StartSession response (arg 1) using the
	// embedded TokenValue; the remaining args let it resume/terminate.
	sessionJSON, err := json.Marshal(map[string]any{
		"SessionId":  out.SessionId,
		"StreamUrl":  out.StreamUrl,
		"TokenValue": out.TokenValue,
	})
	if err != nil {
		return fmt.Errorf("marshal session response: %w", err)
	}
	paramsJSON, err := json.Marshal(in)
	if err != nil {
		return fmt.Errorf("marshal session params: %w", err)
	}
	endpoint := fmt.Sprintf("https://ssm.%s.amazonaws.com", cfg.Region)
	args := []string{
		string(sessionJSON),
		cfg.Region,
		"StartSession",
		"", // profile — empty; creds flow via the inherited env
		string(paramsJSON),
		endpoint,
	}

	cmd := exec.CommandContext(ctx, o.plugin, args...)
	cmd.Env = os.Environ()
	// In headless (--assert) mode, give the plugin its own process group so
	// the idle/timeout watchdog can SIGKILL the whole group. Do NOT do this
	// interactively: a child in a non-foreground process group that touches
	// the controlling TTY (raw mode, reading keystrokes) is hit with
	// SIGTTIN/SIGTTOU and stalls — the shell would freeze on the first
	// command. Interactively the plugin must share our foreground group so
	// it can drive the terminal, exactly like `aws ssm start-session` does.
	if o.assert {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}

	transcript := &capBuf{max: o.transcriptMax}
	act := &activity{}
	act.touch()

	cmd.Stderr = os.Stderr

	if o.assert {
		// Headless mode: tee stdout into the transcript (+ activity) so we
		// can prove the audit-row capture shape. No TTY needed — stdin is a
		// scripted pipe: echo the token, then exit so the shell (and the
		// plugin) terminate on their own. Close the writer right after — the
		// EOF lets os/exec's stdin-copier goroutine return so cmd.Wait() can
		// complete. (Closing it after Wait would deadlock: Wait blocks on
		// that goroutine, the goroutine blocks on Close.)
		cmd.Stdout = io.MultiWriter(transcript, act)
		pr, pw := io.Pipe()
		cmd.Stdin = &actReader{r: pr, act: act}
		go func() {
			_, _ = fmt.Fprintf(pw, "echo %s\n", assertToken)
			// Give the echo a beat to round-trip before ending the shell.
			time.Sleep(2 * time.Second)
			_, _ = fmt.Fprintln(pw, "exit")
			_ = pw.Close()
		}()
	} else {
		fmt.Println("ssm-poc: interactive — type into the shell; 'exit' or Ctrl-C to end")
		// Interactive: hand the plugin the REAL terminal on all three fds.
		// session-manager-plugin needs a genuine TTY on stdin AND stdout —
		// it puts the terminal in raw mode and tracks window size. Teeing
		// stdout through a MultiWriter would make os/exec give the plugin a
		// pipe instead, and the remote shell misbehaves (no echo / no output
		// on the first command). So interactive is a clean passthrough;
		// transcript capture is exercised by --assert instead. (That also
		// means the idle watchdog can't observe bytes here — see below.)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
	}

	started := time.Now()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", o.plugin, err)
	}

	// --- 4. Idle watchdog ---
	// Only armable in headless (--assert) mode, where stdout is tee'd
	// through `act` so we can observe byte activity. In interactive mode
	// the plugin owns the real TTY directly (no tee), so there's nothing
	// to observe — arming it would kill on wall-clock, not idleness.
	// Production enforces idle-timeout server-side; demonstrating it here
	// would need a pty in the middle (out of scope for the spike).
	watchdogDone := make(chan struct{})
	if o.idle > 0 && o.assert {
		go idleWatchdog(cmd, act, o.idle, watchdogDone)
	} else if o.idle > 0 {
		fmt.Println("ssm-poc: note: --idle-seconds is ignored in interactive mode (needs a pty to observe activity); use --assert to demo idle-kill")
	}

	waitErr := cmd.Wait()
	close(watchdogDone)
	dur := time.Since(started)

	// --- 5. Close summary (the audit-row shape, proven feasible) ---
	exitCode := 0
	if waitErr != nil {
		var ee *exec.ExitError
		if errors.As(waitErr, &ee) {
			exitCode = ee.ExitCode()
		} else {
			exitCode = -1
		}
	}
	fmt.Printf("ssm-poc: close: duration=%s exit_code=%d transcript_bytes=%d truncated=%t\n",
		dur.Round(time.Millisecond), exitCode, transcript.written, transcript.truncated)
	if snip := transcript.snippet(400); snip != "" {
		fmt.Printf("ssm-poc: transcript head:\n%s\n", indent(snip))
	}

	// --- 6. Assertions ---
	if o.assert {
		if !bytes.Contains(transcript.Bytes(), []byte(assertToken)) {
			return fmt.Errorf("assert token %q not observed on stdout — transport did not round-trip", assertToken)
		}
		fmt.Println("ssm-poc: assert token observed on stdout — byte round-trip confirmed")
	}
	// A plugin exit forced by our idle/timeout kill surfaces as a
	// non-nil waitErr with a signal; that's expected for those demos,
	// not a probe failure.
	if waitErr != nil && o.idle == 0 && o.timeout == 0 && !o.assert {
		// Interactive 'exit' yields code 0; anything else is worth surfacing.
		return fmt.Errorf("plugin exited: %w", waitErr)
	}
	return nil
}

// idleWatchdog kills the plugin's process group once no stdin/stdout
// byte has flowed for `idle`. Activity definition mirrors exec: any
// stdin OR stdout byte resets the timer; heartbeats don't count
// because there are none on this channel.
func idleWatchdog(cmd *exec.Cmd, act *activity, idle time.Duration, done <-chan struct{}) {
	t := time.NewTicker(idle / 4)
	defer t.Stop()
	for {
		select {
		case <-done:
			return
		case <-t.C:
			if time.Since(act.last()) >= idle {
				fmt.Fprintf(os.Stderr, "ssm-poc: idle %s exceeded — killing session\n", idle)
				if cmd.Process != nil {
					// Negative pid = the whole process group.
					_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
				}
				return
			}
		}
	}
}

// assumeViaWebIdentity reads the id_token, prints its identity claims,
// runs the aud pre-check, then exchanges it for per-user credentials via
// sts:AssumeRoleWithWebIdentity. The AssumeRoleWithWebIdentity API needs
// no AWS credentials of its own — the web-identity token IS the
// credential — so an empty default chain is fine here.
func assumeViaWebIdentity(ctx context.Context, cfg aws.Config, o probeOpts) (creds, error) {
	raw, err := os.ReadFile(o.idTokenFile)
	if err != nil {
		return creds{}, fmt.Errorf("read --id-token-file: %w", err)
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return creds{}, fmt.Errorf("--id-token-file %q is empty", o.idTokenFile)
	}

	cl := decodeJWTClaims(token)
	line := fmt.Sprintf("ssm-poc: id_token claims: sub=%q iss=%q aud=%v", cl.str("sub"), cl.str("iss"), cl.audience())
	if o.groupsClaim != "" {
		line += fmt.Sprintf(" %s=%v", o.groupsClaim, cl.groups(o.groupsClaim))
	}
	fmt.Println(line)

	// aud pre-check — the commonest trust-policy failure. The id_token's
	// aud is the OIDC *client_id*, NOT the API audience.
	if o.expectedAud != "" && !cl.hasAud(o.expectedAud) {
		return creds{}, fmt.Errorf("aud mismatch: id_token aud=%v, expected %q — "+
			"the trust policy's <issuer>:aud condition will reject this (use the OIDC client_id, not the API audience)",
			cl.audience(), o.expectedAud)
	}

	sessionName := o.sessionName
	if sessionName == "" {
		sessionName = sanitizeSessionName("periscope-poc-" + cl.str("sub"))
	}

	stsClient := sts.NewFromConfig(cfg)
	out, err := stsClient.AssumeRoleWithWebIdentity(ctx, &sts.AssumeRoleWithWebIdentityInput{
		RoleArn:          &o.roleARN,
		RoleSessionName:  &sessionName,
		WebIdentityToken: &token,
	})
	if err != nil {
		return creds{}, fmt.Errorf("sts:AssumeRoleWithWebIdentity rejected the token — "+
			"this is the trust-policy gate doing its job. Check the role's trust policy "+
			"(issuer, aud=client_id, and the groups condition) against the claims above: %w", err)
	}
	assumed := derefn(out.AssumedRoleUser.Arn)
	fmt.Printf("ssm-poc: assumed %s as %s\n", o.roleARN, assumed)
	fmt.Printf("ssm-poc: → CloudTrail will attribute this session to %q (the human, not a bot)\n", sessionName)

	return creds{
		AccessKeyID:     derefn(out.Credentials.AccessKeyId),
		SecretAccessKey: derefn(out.Credentials.SecretAccessKey),
		SessionToken:    derefn(out.Credentials.SessionToken),
	}, nil
}

// creds is the trio AssumeRoleWithWebIdentity returns.
type creds struct{ AccessKeyID, SecretAccessKey, SessionToken string }

// claims is the decoded (unverified) JWT payload. AWS STS does the real
// verification; we read claims only for display + the aud pre-check, so a
// generic map keeps the probe IdP-agnostic — no claim name is hardcoded.
type claims struct{ m map[string]any }

func (c claims) str(key string) string {
	s, _ := c.m[key].(string)
	return s
}

// audience returns aud, which per the JWT spec may be a string or array.
func (c claims) audience() []string { return toStringSlice(c.m["aud"]) }

func (c claims) hasAud(want string) bool {
	for _, a := range c.audience() {
		if a == want {
			return true
		}
	}
	return false
}

// groups returns the values of an arbitrary (often IdP-namespaced) claim.
func (c claims) groups(claimName string) []string {
	if claimName == "" {
		return nil
	}
	return toStringSlice(c.m[claimName])
}

// toStringSlice coerces a claim value that may be a single string or an
// array of strings into a []string.
func toStringSlice(v any) []string {
	switch t := v.(type) {
	case string:
		return []string{t}
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

// decodeJWTClaims base64-decodes the JWT payload WITHOUT verifying the
// signature — fine here because AWS STS does the real verification.
func decodeJWTClaims(token string) claims {
	c := claims{m: map[string]any{}}
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return c
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return c
	}
	_ = json.Unmarshal(payload, &c.m)
	return c
}

// sessionNameInvalid matches anything outside the STS roleSessionName
// charset [\w+=,.@-]; we strip those so an OIDC sub like "auth0|abc"
// becomes a valid name.
var sessionNameInvalid = regexp.MustCompile(`[^\w+=,.@-]`)

func sanitizeSessionName(s string) string {
	s = sessionNameInvalid.ReplaceAllString(s, "-")
	if len(s) > 64 {
		s = s[:64]
	}
	if s == "" {
		s = "periscope-poc"
	}
	return s
}

// --- small helpers ---

// activity tracks the last-byte timestamp as unix-nanos, lock-free.
type activity struct{ ns atomic.Int64 }

func (a *activity) touch()           { a.ns.Store(time.Now().UnixNano()) }
func (a *activity) last() time.Time  { return time.Unix(0, a.ns.Load()) }
func (a *activity) Write(p []byte) (int, error) { a.touch(); return len(p), nil }

// actReader wraps a reader to mark activity on every read.
type actReader struct {
	r   io.Reader
	act *activity
}

func (ar *actReader) Read(p []byte) (int, error) {
	n, err := ar.r.Read(p)
	if n > 0 {
		ar.act.touch()
	}
	return n, err
}

// capBuf is a write-capped, truncation-flagging transcript buffer —
// the same shape clustershell.TranscriptMaxBytes enforces in prod.
type capBuf struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	max       int64
	written   int64 // total bytes seen, pre-cap (for the audit count)
	truncated bool
}

func (c *capBuf) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.written += int64(len(p))
	if room := c.max - int64(c.buf.Len()); room > 0 {
		if int64(len(p)) > room {
			c.buf.Write(p[:room])
			c.truncated = true
		} else {
			c.buf.Write(p)
		}
	} else if c.max >= 0 {
		c.truncated = true
	}
	return len(p), nil
}

func (c *capBuf) Bytes() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.buf.Bytes()...)
}

func (c *capBuf) snippet(n int) string {
	b := c.Bytes()
	if len(b) > n {
		b = b[:n]
	}
	return strings.TrimSpace(string(b))
}

func indent(s string) string {
	return "    " + strings.ReplaceAll(s, "\n", "\n    ")
}

func derefn(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
