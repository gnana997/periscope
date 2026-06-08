package main

import (
	"context"
	"errors"
	"fmt"
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

// prepare runs the gates shared by the shell and preflight endpoints:
// cluster lookup, feature flag, node→instance/region resolution,
// per-cluster config, tier gate, and credential acquisition (per-user
// STS in OIDC mode, ambient in dev mode). On any failure it writes the
// JSON error and returns ok=false.
func (h *ssmShell) prepare(w http.ResponseWriter, r *http.Request, p credentials.Provider) (prepared, bool) {
	var out prepared

	c, ok := h.reg.ByName(chi.URLParam(r, "cluster"))
	if !ok {
		apiErrorJSON(w, http.StatusNotFound, "E_NOT_FOUND", "cluster not found", nil)
		return out, false
	}
	if !h.cfg.Enabled {
		apiErrorJSON(w, http.StatusForbidden, "E_NODE_SHELL_DISABLED", "node shell is not enabled on this dashboard", nil)
		return out, false
	}
	nodeName := chi.URLParam(r, "name")

	// Gate on the node's AWS providerID — works across eks/agent/
	// in-cluster backends, since SSM reaches the host through AWS, not
	// the apiserver.
	instanceID, region, err := nodeInstance(r.Context(), p, c, nodeName)
	if err != nil {
		apiErrorJSON(w, http.StatusNotFound, "E_NODE_NOT_EC2",
			"this node is not an SSM-managed EC2 instance", map[string]any{"err": err.Error()})
		return out, false
	}

	rc := h.cfg.Resolve(c)
	if rc.Region == "" {
		rc.Region = region // fall back to the Node's region label
	}
	if rc.Region == "" {
		apiErrorJSON(w, http.StatusBadRequest, "E_NODE_SHELL_NO_REGION",
			"could not determine the AWS region for this node; set nodeShell.region", nil)
		return out, false
	}

	actor := p.Actor()
	var creds aws.CredentialsProvider
	var assumed awsssm.AssumedIdentity
	authKind := "ambient"

	if h.devMode {
		// Dev / no-auth: the server's ambient credentials (the developer's
		// own AWS profile). NEVER reached in a deployed OIDC instance.
		creds = p
	} else {
		if h.resolver == nil || h.resolver.Mode() != authz.ModeTier {
			apiErrorJSON(w, http.StatusForbidden, "E_NODE_SHELL_REQUIRES_TIER",
				"node shell requires auth.authorization.mode=tier", nil)
			return out, false
		}
		s, ok := auth.SessionFromContext(r.Context())
		if !ok {
			apiErrorJSON(w, http.StatusUnauthorized, "E_AUTH", "unauthenticated", nil)
			return out, false
		}
		tier := h.resolver.ResolvedTier(authz.Identity{Subject: s.Subject, Groups: s.Groups})
		if !h.cfg.TierAllowed(tier) {
			apiErrorJSON(w, http.StatusForbidden, "E_FORBIDDEN",
				"your tier is not allowed to open a node shell",
				map[string]any{"tier": tier, "allowed_tiers": h.cfg.Tiers})
			return out, false
		}
		if rc.RoleArn == "" {
			apiErrorJSON(w, http.StatusForbidden, "E_NODE_SHELL_NO_ROLE",
				"no node-shell IAM role is configured for this cluster", nil)
			return out, false
		}
		if h.idToken == nil {
			apiErrorJSON(w, http.StatusInternalServerError, "E_INTERNAL", "id token source unavailable", nil)
			return out, false
		}
		idToken, err := h.idToken.FreshIDToken(r)
		if errors.Is(err, auth.ErrReauthRequired) {
			apiErrorJSON(w, http.StatusUnauthorized, "E_REAUTH_REQUIRED", "sign in again to open a node shell", nil)
			return out, false
		}
		if err != nil {
			apiErrorJSON(w, http.StatusInternalServerError, "E_INTERNAL", "could not obtain id token", nil)
			return out, false
		}
		// aud pre-check: the commonest trust-policy failure, caught up
		// front with a precise message instead of an opaque AccessDenied.
		if rc.OIDCAudience != "" && !awsssm.AudMatches(idToken, rc.OIDCAudience) {
			apiErrorJSON(w, http.StatusForbidden, "E_NODE_SHELL_AUD_MISMATCH",
				"your id_token audience does not match the role's trust policy",
				map[string]any{"expected_aud": rc.OIDCAudience})
			return out, false
		}
		prov, id, err := awsssm.AssumeWebIdentity(r.Context(), aws.Config{Region: rc.Region}, rc.RoleArn, idToken, "periscope-"+s.Subject)
		if err != nil {
			// The trust policy refused — surfaced cleanly, not as a hung WS.
			apiErrorJSON(w, http.StatusForbidden, "E_NODE_SHELL_TRUST_DENIED",
				"AWS denied the session; check the role's trust policy", map[string]any{"err": err.Error()})
			return out, false
		}
		creds, assumed, authKind = prov, id, "sts"
	}

	return prepared{
		cluster: c, nodeName: nodeName, instanceID: instanceID, region: rc.Region,
		creds: creds, assumed: assumed, authKind: authKind, actor: actor,
	}, true
}

// preflight verifies a node is reachable for a shell without opening one:
// it acquires per-user creds (proving trust-policy reachability) and
// checks the SSM agent is Online. A clear error beats a WS that dies a
// second after connecting.
func (h *ssmShell) preflight() credentials.Handler {
	return func(w http.ResponseWriter, r *http.Request, p credentials.Provider) {
		pre, ok := h.prepare(w, r, p)
		if !ok {
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
		pre, ok := h.prepare(w, r, p)
		if !ok {
			return
		}

		// --- concurrency caps ---
		if h.sessions.CountForActor(pre.actor) >= h.cfg.MaxSessionsPerUser {
			apiErrorJSON(w, http.StatusTooManyRequests, "E_CAP_USER",
				"you've hit your concurrent node-shell cap; close one to open another",
				map[string]any{"limit": h.cfg.MaxSessionsPerUser})
			return
		}
		if h.sessions.CountForCluster(pre.cluster.Name) >= h.cfg.MaxSessionsTotal {
			apiErrorJSON(w, http.StatusTooManyRequests, "E_CAP_CLUSTER",
				"this cluster has hit its node-shell cap; try again shortly",
				map[string]any{"limit": h.cfg.MaxSessionsTotal})
			return
		}

		// --- open the SSM session (before the WS upgrade) ---
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
			apiErrorJSON(w, http.StatusBadGateway, "E_NODE_SHELL_START_FAILED",
				"could not start the SSM session", map[string]any{"err": err.Error()})
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
			apiErrorJSON(w, http.StatusInternalServerError, "E_INTERNAL", "session id collision", nil)
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

		// --- WebSocket upgrade ---
		ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: originPatterns()})
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			slog.WarnContext(r.Context(), "ssm_shell.upgrade failed", "err", err, "actor", pre.actor, "cluster", pre.cluster.Name)
			h.emitClose(r.Context(), actorRec, pre, sess.ID(), started,
				awsssm.CloseResult{SessionID: sess.ID(), Reason: awsssm.ReasonAbort, ExitCode: -1}, audit.OutcomeFailure)
			return
		}
		defer ws.Close(websocket.StatusNormalClosure, "session ended")

		// --- stream (blocks until the session ends) ---
		conn := websocket.NetConn(r.Context(), ws, websocket.MessageBinary)
		res := sess.Run(r.Context(), conn, conn)

		outcome := audit.OutcomeSuccess
		if res.Reason == awsssm.ReasonServerError {
			outcome = audit.OutcomeFailure
		}
		h.emitClose(r.Context(), actorRec, pre, sess.ID(), started, res, outcome)
	}
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

