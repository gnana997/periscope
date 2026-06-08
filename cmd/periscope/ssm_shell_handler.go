package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/gnana997/periscope/internal/audit"
	"github.com/gnana997/periscope/internal/auth"
	"github.com/gnana997/periscope/internal/authz"
	"github.com/gnana997/periscope/internal/awsssm"
	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
	"github.com/gnana997/periscope/internal/k8s"
	"github.com/gnana997/periscope/internal/nodeshell"
)

// ssmShell serves the in-browser SSM node shell (#105): a "Node shell"
// WebSocket endpoint and a preflight check, both on a node's EC2 host.
//
// The session is opened with the user's OWN short-lived AWS credentials,
// minted from their OIDC id_token via sts:AssumeRoleWithWebIdentity — an
// IAM trust policy, not Periscope config, is the access gate, and
// CloudTrail attributes the session to the human. In dev mode (no real
// OIDC) it falls back to the server's ambient credentials, which must
// never happen in a deployed OIDC instance.
type ssmShell struct {
	reg      *clusters.Registry
	sessions *nodeshell.Registry
	auditer  *audit.Emitter
	cfg      nodeshell.Config
	resolver *authz.Resolver
	idToken  *auth.IDTokenSource // nil in dev mode
	devMode  bool
}

func newSSMShell(reg *clusters.Registry, sessions *nodeshell.Registry, auditer *audit.Emitter, cfg nodeshell.Config, resolver *authz.Resolver, idToken *auth.IDTokenSource, devMode bool) *ssmShell {
	return &ssmShell{reg: reg, sessions: sessions, auditer: auditer, cfg: cfg, resolver: resolver, idToken: idToken, devMode: devMode}
}

// prepared bundles what both endpoints need after the shared gates and
// credential acquisition succeed.
type prepared struct {
	cluster    clusters.Cluster
	nodeName   string
	instanceID string
	region     string
	creds      aws.CredentialsProvider
	assumed    awsssm.AssumedIdentity
	authKind   string // "sts" | "ambient"
	actor      string
}

// prepErr is a gate failure, rendered as an HTTP JSON error by the
// preflight endpoint or as a {type:error} WebSocket frame by the shell
// endpoint (after the upgrade, so the SPA shows a clean message and does
// not reconnect-loop on a permanent condition).
type prepErr struct {
	status int // HTTP status for the preflight path
	code   string
	msg    string
	extra  map[string]any
}

// prepare runs the gates shared by the shell and preflight endpoints:
// cluster lookup, feature flag, node→instance/region resolution,
// per-cluster config, tier gate, and credential acquisition (per-user
// STS in OIDC mode, ambient in dev mode). Returns a *prepErr (nil on
// success) rather than writing — the caller renders it as HTTP or a WS
// frame.
func (h *ssmShell) prepare(r *http.Request, p credentials.Provider) (prepared, *prepErr) {
	var out prepared

	c, ok := h.reg.ByName(chi.URLParam(r, "cluster"))
	if !ok {
		return out, &prepErr{http.StatusNotFound, "E_NOT_FOUND", "cluster not found", nil}
	}
	if !h.cfg.Enabled {
		return out, &prepErr{http.StatusForbidden, "E_NODE_SHELL_DISABLED", "node shell is not enabled on this dashboard", nil}
	}
	nodeName := chi.URLParam(r, "name")

	// Gate on the node's AWS providerID — works across eks/agent/
	// in-cluster backends, since SSM reaches the host through AWS, not
	// the apiserver.
	instanceID, region, err := nodeInstance(r.Context(), p, c, nodeName)
	if err != nil {
		return out, &prepErr{http.StatusNotFound, "E_NODE_NOT_EC2",
			"this node is not an SSM-managed EC2 instance", map[string]any{"err": err.Error()}}
	}

	rc := h.cfg.Resolve(c)
	if rc.Region == "" {
		rc.Region = region // fall back to the Node's region label
	}
	if rc.Region == "" {
		return out, &prepErr{http.StatusBadRequest, "E_NODE_SHELL_NO_REGION",
			"could not determine the AWS region for this node; set nodeShell.region", nil}
	}

	actor := p.Actor()
	var creds aws.CredentialsProvider
	var assumed awsssm.AssumedIdentity
	authKind := "ambient"

	if h.devMode {
		// Dev / no-auth: the server's ambient credentials (the developer's
		// own AWS profile / pod role). NEVER reached in a deployed OIDC
		// instance.
		creds = p
	} else {
		if h.resolver == nil || h.resolver.Mode() != authz.ModeTier {
			return out, &prepErr{http.StatusForbidden, "E_NODE_SHELL_REQUIRES_TIER",
				"node shell requires auth.authorization.mode=tier", nil}
		}
		s, ok := auth.SessionFromContext(r.Context())
		if !ok {
			return out, &prepErr{http.StatusUnauthorized, "E_AUTH", "unauthenticated", nil}
		}
		tier := h.resolver.ResolvedTier(authz.Identity{Subject: s.Subject, Groups: s.Groups})
		if !h.cfg.TierAllowed(tier) {
			return out, &prepErr{http.StatusForbidden, "E_FORBIDDEN",
				"your tier is not allowed to open a node shell",
				map[string]any{"tier": tier, "allowed_tiers": h.cfg.Tiers}}
		}
		if rc.RoleArn == "" {
			return out, &prepErr{http.StatusForbidden, "E_NODE_SHELL_NO_ROLE",
				"no node-shell IAM role is configured for this cluster", nil}
		}
		if h.idToken == nil {
			return out, &prepErr{http.StatusInternalServerError, "E_INTERNAL", "id token source unavailable", nil}
		}
		idToken, err := h.idToken.FreshIDToken(r)
		if errors.Is(err, auth.ErrReauthRequired) {
			return out, &prepErr{http.StatusUnauthorized, "E_REAUTH_REQUIRED", "sign in again to open a node shell", nil}
		}
		if err != nil {
			return out, &prepErr{http.StatusInternalServerError, "E_INTERNAL", "could not obtain id token", nil}
		}
		// aud pre-check: the commonest trust-policy failure, caught up
		// front with a precise message instead of an opaque AccessDenied.
		if rc.OIDCAudience != "" && !awsssm.AudMatches(idToken, rc.OIDCAudience) {
			return out, &prepErr{http.StatusForbidden, "E_NODE_SHELL_AUD_MISMATCH",
				"your id_token audience does not match the role's trust policy",
				map[string]any{"expected_aud": rc.OIDCAudience}}
		}
		prov, id, err := awsssm.AssumeWebIdentity(r.Context(), aws.Config{Region: rc.Region}, rc.RoleArn, idToken, "periscope-"+s.Subject)
		if err != nil {
			// The trust policy refused — surfaced cleanly, not as a hung WS.
			return out, &prepErr{http.StatusForbidden, "E_NODE_SHELL_TRUST_DENIED",
				"AWS denied the session; check the role's trust policy", map[string]any{"err": err.Error()}}
		}
		creds, assumed, authKind = prov, id, "sts"
	}

	return prepared{
		cluster: c, nodeName: nodeName, instanceID: instanceID, region: rc.Region,
		creds: creds, assumed: assumed, authKind: authKind, actor: actor,
	}, nil
}

// preflight verifies a node is reachable for a shell without opening one:
// it acquires per-user creds (proving trust-policy reachability) and
// checks the SSM agent is Online. A clear error beats a WS that dies a
// second after connecting.
func (h *ssmShell) preflight() credentials.Handler {
	return func(w http.ResponseWriter, r *http.Request, p credentials.Provider) {
		pre, perr := h.prepare(r, p)
		if perr != nil {
			apiErrorJSON(w, perr.status, perr.code, perr.msg, perr.extra)
			return
		}
		res, err := awsssm.Preflight(r.Context(), pre.creds, pre.region, pre.instanceID)
		if err != nil {
			apiErrorJSON(w, http.StatusBadGateway, "E_NODE_SHELL_PREFLIGHT",
				"preflight failed", map[string]any{"err": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"instanceId":     res.InstanceID,
			"agentOnline":    res.AgentOnline,
			"pingStatus":     res.PingStatus,
			"platform":       res.PlatformName,
			"auth":           pre.authKind,
			"assumedRoleArn": pre.assumed.AssumedRoleARN,
		})
	}
}

// shell upgrades to a WebSocket and streams an SSM session to the node.
// Wire format: binary frames carry the raw session byte stream both ways
// (the browser's xterm.js is the terminal). StartSession runs before the
// upgrade so failures are clean HTTP errors.
func (h *ssmShell) shell() credentials.Handler {
	return func(w http.ResponseWriter, r *http.Request, p credentials.Provider) {
		// Upgrade FIRST, so every gate failure below is delivered as a
		// clean {type:error} frame — the SPA shows the message and (since
		// the frame is non-retryable) does not reconnect-loop, instead of
		// the opaque failed-handshake the pre-upgrade path produced.
		ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: originPatterns()})
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			slog.WarnContext(r.Context(), "ssm_shell.upgrade failed", "err", err)
			return
		}
		defer ws.Close(websocket.StatusNormalClosure, "session ended")

		pre, perr := h.prepare(r, p)
		if perr != nil {
			wsErrorFrame(r.Context(), ws, perr.code, perr.msg)
			return
		}

		// --- concurrency caps ---
		if h.sessions.CountForActor(pre.actor) >= h.cfg.MaxSessionsPerUser {
			wsErrorFrame(r.Context(), ws, "E_CAP_USER",
				"you've hit your concurrent node-shell cap; close one to open another")
			return
		}
		if h.sessions.CountForCluster(pre.cluster.Name) >= h.cfg.MaxSessionsTotal {
			wsErrorFrame(r.Context(), ws, "E_CAP_CLUSTER",
				"this cluster has hit its node-shell cap; try again shortly")
			return
		}

		// --- open the SSM session ---
		sessCfg := awsssm.Config{
			Region:        pre.region,
			InstanceID:    pre.instanceID,
			Reason:        fmt.Sprintf("periscope cluster=%s node=%s actor=%s", pre.cluster.Name, pre.nodeName, pre.actor),
			IdleTimeout:   h.cfg.IdleTimeout,
			TranscriptMax: h.cfg.TranscriptMaxBytes,
			PluginPath:    h.cfg.PluginPath,
		}
		sess, err := awsssm.Open(r.Context(), pre.creds, sessCfg)
		if err != nil {
			wsErrorFrame(r.Context(), ws, "E_NODE_SHELL_START_FAILED",
				"could not start the SSM session: "+err.Error())
			return
		}
		// Always tear the SSM session down — fresh context, the request
		// one is usually cancelled by the time this runs.
		defer func() {
			tctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if terr := sess.Terminate(tctx); terr != nil {
				slog.WarnContext(r.Context(), "ssm_shell.terminate (best-effort)", "session_id", sess.ID(), "err", terr)
			}
		}()

		if !h.sessions.Add(nodeshell.SessionRecord{
			ID: sess.ID(), Actor: pre.actor, Cluster: pre.cluster.Name, Node: pre.nodeName, StartedAt: time.Now().UTC(),
		}) {
			wsErrorFrame(r.Context(), ws, "E_INTERNAL", "session id collision")
			return
		}
		defer h.sessions.Remove(sess.ID())

		actorRec := actorFromContext(r.Context())
		started := time.Now().UTC()
		h.auditer.Record(r.Context(), audit.Event{
			Actor:   actorRec,
			Verb:    audit.VerbSSMSessionOpen,
			Outcome: audit.OutcomeSuccess,
			Cluster: pre.cluster.Name,
			Extra: map[string]any{
				"session_id":        sess.ID(),
				"instance_id":       pre.instanceID,
				"node":              pre.nodeName,
				"auth":              pre.authKind,
				"role_session_name": pre.assumed.RoleSessionName,
				"assumed_role_arn":  pre.assumed.AssumedRoleARN,
				"region":            pre.region,
				"started_at":        started.Format(time.RFC3339Nano),
			},
		})

		// --- exec frame protocol (shared with pod-exec / cluster-shell) ---
		// hello first so the SPA's ExecClient transitions to "connected";
		// then binary frames carry the SSM byte stream both ways; closed
		// at the end. Container is empty — this is a node, not a pod.
		_ = wsWriteJSON(r.Context(), ws, map[string]any{
			"type": "hello", "sessionId": sess.ID(), "container": "",
		})

		runCtx, cancel := context.WithCancel(r.Context())
		defer cancel()

		// user -> node: WS binary frames feed plugin stdin via a pipe;
		// text frames are control ({type:close} ends the session).
		stdinR, stdinW := io.Pipe()
		go func() {
			defer stdinW.Close()
			for {
				typ, data, rerr := ws.Read(runCtx)
				if rerr != nil {
					cancel()
					return
				}
				switch typ {
				case websocket.MessageBinary:
					if _, werr := stdinW.Write(data); werr != nil {
						cancel()
						return
					}
				case websocket.MessageText:
					var ctrl struct {
						Type string `json:"type"`
					}
					if json.Unmarshal(data, &ctrl) == nil && ctrl.Type == "close" {
						cancel()
						return
					}
					// {type:resize} is accepted but not yet forwarded (v1).
				}
			}
		}()

		// node -> user: plugin stdout becomes WS binary frames.
		res := sess.Run(runCtx, stdinR, wsBinaryWriter{ctx: runCtx, ws: ws})

		_ = wsWriteJSON(r.Context(), ws, map[string]any{
			"type": "closed", "exitCode": res.ExitCode, "reason": res.Reason,
		})

		outcome := audit.OutcomeSuccess
		if res.Reason == awsssm.ReasonServerError {
			outcome = audit.OutcomeFailure
		}
		h.emitClose(r.Context(), actorRec, pre, sess.ID(), started, res, outcome)
	}
}

// wsBinaryWriter adapts a WebSocket to io.Writer: each Write becomes one
// binary frame. Used as the SSM session's stdout sink.
type wsBinaryWriter struct {
	ctx context.Context
	ws  *websocket.Conn
}

func (w wsBinaryWriter) Write(p []byte) (int, error) {
	if err := w.ws.Write(w.ctx, websocket.MessageBinary, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

// wsWriteJSON sends a control frame as a text message.
func wsWriteJSON(ctx context.Context, ws *websocket.Conn, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return ws.Write(ctx, websocket.MessageText, b)
}

// wsErrorFrame sends an {type:error} control frame. retryable is left
// unset, which ExecClient treats as terminal — the SPA shows the message
// and does not reconnect (correct for permanent gate failures like a
// non-EC2 node or a refused trust policy).
func wsErrorFrame(ctx context.Context, ws *websocket.Conn, code, msg string) {
	_ = wsWriteJSON(ctx, ws, map[string]any{"type": "error", "code": code, "message": msg})
}

func (h *ssmShell) emitClose(ctx context.Context, actor audit.Actor, pre prepared, sessionID string, started time.Time, res awsssm.CloseResult, outcome audit.Outcome) {
	ended := time.Now().UTC()
	reason := res.Reason
	if reason == "" {
		reason = awsssm.ReasonCompleted
	}
	extra := map[string]any{
		"session_id":        sessionID,
		"instance_id":       pre.instanceID,
		"node":              pre.nodeName,
		"auth":              pre.authKind,
		"role_session_name": pre.assumed.RoleSessionName,
		"started_at":        started.Format(time.RFC3339Nano),
		"ended_at":          ended.Format(time.RFC3339Nano),
		"duration_ms":       ended.Sub(started).Milliseconds(),
		"exit_code":         res.ExitCode,
		"transcript_bytes":  res.TranscriptBytes,
		"truncated":         res.Truncated,
	}
	if len(res.Transcript) > 0 {
		extra["transcript"] = string(res.Transcript)
	}
	if res.Err != nil {
		extra["err"] = res.Err.Error()
	}
	h.auditer.Record(ctx, audit.Event{
		Actor:   actor,
		Verb:    audit.VerbSSMSessionClose,
		Outcome: outcome,
		Cluster: pre.cluster.Name,
		Reason:  reason,
		Extra:   extra,
	})
}

// nodeInstance reads the Node — uses the shared writeJSON from main.go. and extracts its EC2 instance id (from
// spec.providerID) and region (from the topology.kubernetes.io/region
// label). The K8s read uses the same per-request impersonated client as
// the rest of the dashboard; for agent-backed clusters it transparently
// goes through the tunnel.
func nodeInstance(ctx context.Context, p credentials.Provider, c clusters.Cluster, name string) (instanceID, region string, err error) {
	cs, err := k8s.NewClientset(ctx, p, c)
	if err != nil {
		return "", "", err
	}
	node, err := cs.CoreV1().Nodes().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", "", err
	}
	instanceID, err = awsssm.InstanceIDFromProviderID(node.Spec.ProviderID)
	if err != nil {
		return "", "", err
	}
	return instanceID, node.Labels["topology.kubernetes.io/region"], nil
}

