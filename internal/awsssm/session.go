package awsssm

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

// defaultPlugin is the session-manager-plugin binary name; overridable
// per Config for tests or non-standard installs.
const defaultPlugin = "session-manager-plugin"

// SSMAPI is the subset of the SSM client awsssm uses. An interface so
// tests can stub StartSession / TerminateSession / preflight.
type SSMAPI interface {
	StartSession(ctx context.Context, in *ssm.StartSessionInput, opts ...func(*ssm.Options)) (*ssm.StartSessionOutput, error)
	TerminateSession(ctx context.Context, in *ssm.TerminateSessionInput, opts ...func(*ssm.Options)) (*ssm.TerminateSessionOutput, error)
	DescribeInstanceInformation(ctx context.Context, in *ssm.DescribeInstanceInformationInput, opts ...func(*ssm.Options)) (*ssm.DescribeInstanceInformationOutput, error)
}

// Config parameterizes a node-shell session.
type Config struct {
	Region        string
	InstanceID    string
	DocumentName  string        // optional; default is the account's SessionManagerRunShell
	Reason        string        // recorded in CloudTrail / SSM session history
	IdleTimeout   time.Duration // 0 disables the idle watchdog
	TranscriptMax int64         // cap on the captured transcript
	PluginPath    string        // default "session-manager-plugin"
}

// Close-reason constants, mirroring RFC 0003's exec_close dispositions.
const (
	ReasonCompleted   = "completed"
	ReasonIdleTimeout = "idle_timeout"
	ReasonAbort       = "abort"
	ReasonServerError = "server_error"
)

// CloseResult is the outcome of a finished session — shaped to become an
// ssm_session_close audit row.
type CloseResult struct {
	SessionID       string
	Duration        time.Duration
	ExitCode        int
	Reason          string
	TranscriptBytes int64
	Truncated       bool
	Transcript      []byte
	Err             error // non-nil only on server_error
}

// Session is an opened-but-not-yet-streaming SSM session. Open it,
// emit the open audit row from ID(), Run() the byte stream, then
// Terminate() (typically deferred for cleanup on every path).
type Session struct {
	api SSMAPI
	cfg Config
	out *ssm.StartSessionOutput
}

// Open calls ssm:StartSession with per-user credentials. It does NOT
// start the byte stream — that's Run. Returns an error (no session
// created) if StartSession is rejected, so the handler can surface a
// clean failure before upgrading the WebSocket.
func Open(ctx context.Context, creds aws.CredentialsProvider, cfg Config) (*Session, error) {
	api := ssm.NewFromConfig(aws.Config{Region: cfg.Region, Credentials: creds})
	return open(ctx, api, cfg)
}

func open(ctx context.Context, api SSMAPI, cfg Config) (*Session, error) {
	in := &ssm.StartSessionInput{Target: aws.String(cfg.InstanceID)}
	if cfg.DocumentName != "" {
		in.DocumentName = aws.String(cfg.DocumentName)
	}
	if cfg.Reason != "" {
		in.Reason = aws.String(cfg.Reason)
	}
	out, err := api.StartSession(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("ssm start session on %s: %w", cfg.InstanceID, err)
	}
	if out == nil || out.StreamUrl == nil || out.TokenValue == nil {
		return nil, fmt.Errorf("ssm start session on %s: incomplete response", cfg.InstanceID)
	}
	return &Session{api: api, cfg: cfg, out: out}, nil
}

// ID returns the SSM session id — the cross-reference key shared with
// CloudTrail and the audit log.
func (s *Session) ID() string { return aws.ToString(s.out.SessionId) }

// Terminate closes the SSM session server-side. Idempotent and
// best-effort; safe to defer. Use a fresh context — the request context
// is usually already cancelled by the time this runs.
func (s *Session) Terminate(ctx context.Context) error {
	_, err := s.api.TerminateSession(ctx, &ssm.TerminateSessionInput{SessionId: s.out.SessionId})
	return err
}

// PreflightResult reports whether a node is reachable for a shell.
type PreflightResult struct {
	InstanceID   string
	AgentOnline  bool
	PingStatus   string
	PlatformName string
}

// Preflight checks that the target's SSM agent is registered and Online,
// using the same per-user credentials a real session would. A passing
// preflight (plus a successful prior AssumeWebIdentity, which proves
// trust-policy reachability) means StartSession will almost certainly
// succeed — better than a WebSocket that dies a second after it opens.
func Preflight(ctx context.Context, creds aws.CredentialsProvider, region, instanceID string) (PreflightResult, error) {
	api := ssm.NewFromConfig(aws.Config{Region: region, Credentials: creds})
	return preflight(ctx, api, instanceID)
}

func preflight(ctx context.Context, api SSMAPI, instanceID string) (PreflightResult, error) {
	res := PreflightResult{InstanceID: instanceID}
	out, err := api.DescribeInstanceInformation(ctx, &ssm.DescribeInstanceInformationInput{
		Filters: []ssmtypes.InstanceInformationStringFilter{
			{Key: aws.String("InstanceIds"), Values: []string{instanceID}},
		},
	})
	if err != nil {
		return res, fmt.Errorf("describe instance information: %w", err)
	}
	if len(out.InstanceInformationList) == 0 {
		// No agent reporting for this instance — not registered with SSM.
		return res, nil
	}
	info := out.InstanceInformationList[0]
	res.PingStatus = string(info.PingStatus)
	res.PlatformName = aws.ToString(info.PlatformName)
	res.AgentOnline = info.PingStatus == ssmtypes.PingStatusOnline
	return res, nil
}
