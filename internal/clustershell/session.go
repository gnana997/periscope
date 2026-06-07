package clustershell

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
	"github.com/gnana997/periscope/internal/exec"
	"github.com/gnana997/periscope/internal/k8s"
)

// In-cluster paths the shell pod inherits via the standard SA mount.
// Hardcoded because v1.2 supports only BackendInCluster — the values
// are stable for that backend. When v1.3 extends to agent-tunnel /
// EKS, refactor to call a build-rest-config helper instead.
const (
	inClusterCAFile     = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
	inClusterAPIServer  = "https://kubernetes.default.svc"
	inClusterConfigName = "in-cluster"
)

// fieldManager identifies Periscope as the writer of the Pods and
// Secrets it creates. Mirrors the cronjobs.go convention so a
// cluster admin doing `kubectl get pod -o yaml | yq .metadata.managedFields`
// sees a recognisable owner.
const fieldManager = "periscope-spa"

// Session is one in-flight cluster-shell session. The handler creates
// it pre-WS-upgrade, calls Start to provision the ephemeral pod,
// Attach to bridge WS↔exec, and Close to tear down and read back
// the audit transcript.
//
// Lifecycle: New → Start → Attach (blocks for session duration) →
// Close. Close is idempotent and safe under any path (panic,
// double-close, etc.) via the closed atomic flag.
type Session struct {
	ID        string
	Cluster   clusters.Cluster
	Provider  credentials.Provider
	Mode      Mode
	Tier      string
	Actor     string
	Config    Config
	StartedAt time.Time

	// Built during Start, used during Close.
	podName    string
	secretName string

	closed atomic.Bool
}

// New returns a Session ready for Start. Caller supplies the
// resolved tier (post-authz.Resolver) and the Provider that carries
// the operator's impersonation strings.
func New(cluster clusters.Cluster, p credentials.Provider, tier string, mode Mode, cfg Config) *Session {
	return &Session{
		ID:        uuid.NewString(),
		Cluster:   cluster,
		Provider:  p,
		Mode:      mode,
		Tier:      tier,
		Actor:     p.Actor(),
		Config:    cfg,
		StartedAt: time.Now(),
	}
}

// Start provisions the ephemeral shell pod and its kubeconfig
// Secret, waits for the pod to reach Ready, and returns. On any
// error past the Secret/Pod create, partially-created resources are
// torn down before returning so retries don't leak state.
//
// The pod runs the periscope-shell image with PERISCOPE_SHELL_MODE
// set; the entrypoint validates env and blocks until SIGTERM (the
// pod has no liveness work to do — the operator's interactive bash
// is spawned by Attach via kubectl exec on a separate channel).
func (s *Session) Start(ctx context.Context) error {
	cs, err := k8s.NewClientset(ctx, s.Provider, s.Cluster)
	if err != nil {
		return fmt.Errorf("build clientset: %w", err)
	}

	caData, err := os.ReadFile(inClusterCAFile)
	if err != nil {
		return fmt.Errorf("read in-cluster CA at %s: %w", inClusterCAFile, err)
	}

	// 1. Mint a short-lived token via TokenRequest on the per-tier SA.
	token, err := MintSessionToken(ctx, cs, s.Config.Namespace, ServiceAccountName(s.Tier), s.Config.TokenTTL)
	if err != nil {
		return fmt.Errorf("mint session token: %w", err)
	}

	// 2. Build the per-session kubeconfig.
	impersonation := s.Provider.Impersonation()
	kubeconfig, err := BuildKubeconfig(KubeconfigParams{
		SessionID:         s.ID,
		ClusterName:       inClusterConfigName,
		Server:            inClusterAPIServer,
		CAData:            caData,
		Token:             token,
		Impersonate:       impersonation.UserName,
		ImpersonateGroups: impersonation.Groups,
		ImpersonateUserExtra: map[string][]string{
			"audit.periscope.io/session-id": {s.ID},
			"audit.periscope.io/actor":      {s.Actor},
		},
	})
	if err != nil {
		return fmt.Errorf("build kubeconfig: %w", err)
	}

	// 3. Create the Secret first — Pod create requires the Secret to
	// already exist for the volume mount to validate at admission.
	secret := BuildSecret(s.ID, s.Config.Namespace, ActorHash(s.Actor), s.Tier, kubeconfig)
	if _, err := cs.CoreV1().Secrets(s.Config.Namespace).Create(ctx, secret, metav1.CreateOptions{
		FieldManager: fieldManager,
	}); err != nil {
		return fmt.Errorf("create kubeconfig secret %s/%s: %w", s.Config.Namespace, secret.Name, err)
	}
	s.secretName = secret.Name

	// 4. Create the Pod.
	pod := BuildPodSpec(PodSpecParams{
		SessionID:  s.ID,
		Namespace:  s.Config.Namespace,
		Tier:       s.Tier,
		Mode:       s.Mode,
		Actor:      s.Actor,
		Image:      s.Config.ShellImage,
		PullPolicy: pullPolicyFromString(s.Config.ImagePullPolicy),
	})
	if _, err := cs.CoreV1().Pods(s.Config.Namespace).Create(ctx, pod, metav1.CreateOptions{
		FieldManager: fieldManager,
	}); err != nil {
		// Roll back the Secret we just created; otherwise a transient
		// pod-create failure leaks Secrets across retries.
		_ = cs.CoreV1().Secrets(s.Config.Namespace).Delete(context.Background(), s.secretName, metav1.DeleteOptions{})
		s.secretName = ""
		return fmt.Errorf("create shell pod %s/%s: %w", s.Config.Namespace, pod.Name, err)
	}
	s.podName = pod.Name

	// 5. Wait for the pod to become Ready, bounded by PodStartTimeout.
	waitCtx, cancel := context.WithTimeout(ctx, s.Config.PodStartTimeout)
	defer cancel()
	if err := WaitForPodReady(waitCtx, cs, s.Config.Namespace, s.podName); err != nil {
		// Tear down both resources — the pod is in a state we can't
		// usefully recover from, and the Secret is now orphaned.
		// Use a fresh background context: the request context may
		// already be expired (timeout path).
		_ = s.deletePodAndSecret(context.Background(), cs)
		s.podName = ""
		s.secretName = ""
		return fmt.Errorf("shell pod not ready: %w", err)
	}
	return nil
}

// Attach bridges the operator's WebSocket to a kubectl-exec'd bash
// inside the shell pod. Blocks for the lifetime of the session.
//
// Reuses internal/exec/Run for all the lifecycle discipline
// (heartbeat, idle timer, idle warning, control-frame routing,
// E_FORBIDDEN/E_NO_SHELL heuristics, byte accounting). Cluster-shell
// adds nothing of substance on the wire — the only difference vs.
// pod-exec is which pod we attach to and the fact that we own that
// pod's lifecycle.
func (s *Session) Attach(ctx context.Context, ws *websocket.Conn) (k8s.ExecResult, exec.Stats, error) {
	return exec.Run(ctx, ws, s.Provider, exec.Params{
		SessionID: s.ID,
		Actor:     s.Actor,
		Cluster:   s.Cluster,
		Namespace: s.Config.Namespace,
		Pod:       s.podName,
		Container: ContainerName,
		Command:   []string{"/bin/bash", "--login"},
		TTY:       true,
	}, s.Config.toExecConfig(), nil)
}

// Close tears down the ephemeral pod + kubeconfig Secret and reads
// back the per-command audit log the wrapper accumulated during the
// session. Idempotent — safe to call multiple times; first caller
// wins.
//
// Returns the parsed command lines for the caller to fold into the
// cluster_shell_close audit row's Extra.commands field. A nil slice
// means either no commands were run OR the audit-file read failed
// (logged at slog.Warn inside ReadCommandLog; either way, the close
// path doesn't fail-loud on audit best-effort).
func (s *Session) Close(ctx context.Context) ([]CommandLine, error) {
	if !s.closed.CompareAndSwap(false, true) {
		return nil, nil
	}

	cs, err := k8s.NewClientset(ctx, s.Provider, s.Cluster)
	if err != nil {
		// Best-effort cleanup: log + return; the pod will eventually
		// be reaped by other means (Periscope startup sweep, K8s GC
		// of completed pods, etc.). The audit close row will still
		// fire, just without commands.
		return nil, fmt.Errorf("build clientset for close: %w", err)
	}

	// Read the audit file BEFORE deleting the pod. ReadCommandLog
	// swallows errors and returns whatever lines it could parse —
	// the close path is best-effort on attribution.
	var cmds []CommandLine
	if s.podName != "" {
		cmds = ReadCommandLog(ctx, s.Provider, s.Cluster, s.Config.Namespace, s.podName, s.ID, s.Config.TranscriptMaxBytes)
	}

	if err := s.deletePodAndSecret(ctx, cs); err != nil {
		return cmds, err
	}
	return cmds, nil
}

// PodName returns the name of the ephemeral shell pod. Empty before
// Start has succeeded. Exposed so the handler can populate audit
// rows with the pod's identity.
func (s *Session) PodName() string {
	return s.podName
}

// SecretName returns the name of the per-session kubeconfig Secret.
// Empty before Start has succeeded.
func (s *Session) SecretName() string {
	return s.secretName
}

// deletePodAndSecret removes both resources, ignoring NotFound
// errors so it's safe to call after partial creation or after the
// pod has already been cleaned up by some other path.
func (s *Session) deletePodAndSecret(ctx context.Context, cs kubernetes.Interface) error {
	var errs []error
	if s.podName != "" {
		if err := cs.CoreV1().Pods(s.Config.Namespace).Delete(ctx, s.podName, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			errs = append(errs, fmt.Errorf("delete pod %s/%s: %w", s.Config.Namespace, s.podName, err))
		}
	}
	if s.secretName != "" {
		if err := cs.CoreV1().Secrets(s.Config.Namespace).Delete(ctx, s.secretName, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			errs = append(errs, fmt.Errorf("delete secret %s/%s: %w", s.Config.Namespace, s.secretName, err))
		}
	}
	return errors.Join(errs...)
}

// toExecConfig translates the cluster-shell knobs into the shape
// internal/exec.Run expects. Heartbeat and idle-warn are intentionally
// not exposed in clusterShell.* env vars (yet) — they're the same
// values pod-exec uses, and operators rarely want to tune them
// separately.
func (c Config) toExecConfig() exec.Config {
	const defaultHeartbeat = 20 * time.Second
	const defaultIdleWarn = 30 * time.Second
	return exec.Config{
		HeartbeatInterval:  defaultHeartbeat,
		IdleTimeout:        c.IdleTimeout,
		IdleWarnLead:       defaultIdleWarn,
		MaxSessionsPerUser: c.MaxSessionsPerUser,
		MaxSessionsTotal:   c.MaxSessionsTotal,
	}
}
