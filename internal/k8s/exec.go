package k8s

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/httpstream"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	exec_util "k8s.io/client-go/util/exec"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

// DefaultContainerAnnotation is the standard annotation that indicates which
// container `kubectl exec` and `kubectl logs` should target by default when
// the user does not specify --container. We honor it here for parity.
const DefaultContainerAnnotation = "kubectl.kubernetes.io/default-container"

// DefaultShellCommand is the command used when the caller does not supply
// one. Two compatibility choices that took some work to get right:
//
//  1. Unqualified program names ("sh", "bash") rather than absolute paths
//     so the container runtime (runc/crun) performs PATH lookup. This
//     matters for images that ship a shell at /usr/bin/sh or
//     /usr/bin/bash without the conventional /bin/sh symlink — common
//     in Wolfi, newer distroless variants, and custom slim images.
//
//  2. `command -v bash` BEFORE `exec bash` instead of `exec bash || exec sh`.
//     POSIX-strict shells (busybox sh, dash) terminate the shell with
//     status 127 when an `exec` target isn't found, *before* the `||`
//     branch can run. So the elegant `||` chain only works on
//     bash/ksh/zsh — silently broken on alpine/busybox images that have
//     sh but no bash. Probing first sidesteps that.
//
// Containers that ship no shell at all on PATH still fail with exit 127;
// PR3 will detect that case up-front and surface E_NO_SHELL.
var DefaultShellCommand = []string{
	"sh", "-c",
	"command -v bash >/dev/null 2>&1 && exec bash; exec sh",
}

// ExecPodArgs is the set of inputs to ExecPod. Streams are wired by the
// caller (typically session.go's WebSocket pumps).
type ExecPodArgs struct {
	Cluster   clusters.Cluster
	Namespace string
	Pod       string

	// Container, if empty, is resolved from the
	// kubectl.kubernetes.io/default-container annotation on the pod, falling
	// back to the first non-init container.
	Container string

	// Command, if empty, defaults to DefaultShellCommand.
	Command []string

	TTY          bool
	Stdin        io.Reader
	Stdout       io.Writer
	Stderr       io.Writer // may be the same writer as Stdout (merged)
	TerminalSize <-chan remotecommand.TerminalSize

	// SessionID is forwarded into structured logs so app-level audit can be
	// joined to other records keyed on the same UUID. Also injected into the
	// outgoing apiserver request as the audit.periscope.io/session-id
	// HTTP header via rest.Config.WrapTransport, so v2 K8s audit logs
	// can join Periscope's app log on a single ID.
	SessionID string

	// Policy, if non-nil, governs the WS-vs-SPDY transport choice for
	// this attempt and records the outcome. nil means "always try
	// WS-then-SPDY without bookkeeping" (PR1/PR2 behavior).
	Policy *Policy
}

// ResolvedExec captures the parameters that were actually used after
// defaults were applied. Useful for the WS hello frame and audit.
type ResolvedExec struct {
	Container string
	Command   []string
}

// ExecResult is returned when the exec stream finishes cleanly.
type ExecResult struct {
	ExitCode int
	Reason   string // "completed" | "container_exit" | "stream_canceled"
	Resolved ResolvedExec

	// Transport is the wire protocol that actually carried the stream.
	// Empty when the stream never opened (config error / pod lookup
	// failed). Surfaced into the audit session_end record so compliance
	// teams can correlate apiserver-side transport with our log line.
	Transport PolicyTransport
}

// ResolveExecTarget resolves the container name for a pod given the caller's
// preference. Exposed separately so the HTTP handler can echo the resolved
// target back to the client in its hello frame before any streaming starts.
func ResolveExecTarget(ctx context.Context, p credentials.Provider, c clusters.Cluster, namespace, pod, requestedContainer string) (string, error) {
	cs, err := newClientFn(ctx, p, c)
	if err != nil {
		return "", err
	}
	return resolveContainer(ctx, cs, namespace, pod, requestedContainer)
}

func resolveContainer(ctx context.Context, cs kubernetes.Interface, namespace, podName, requested string) (string, error) {
	if requested != "" {
		return requested, nil
	}
	pod, err := cs.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get pod %s/%s: %w", namespace, podName, err)
	}
	if v, ok := pod.Annotations[DefaultContainerAnnotation]; ok && v != "" {
		return v, nil
	}
	if len(pod.Spec.Containers) == 0 {
		return "", fmt.Errorf("pod %s/%s has no containers", namespace, podName)
	}
	return pod.Spec.Containers[0].Name, nil
}

// ExecPod opens an exec stream into a container. It blocks until the stream
// ends (container exits, stdin EOF, ctx cancellation, or transport error)
// and returns the resolved target plus exit code on success.
//
// Per the architectural ground rules, this is the typed function v1 HTTP
// handlers, v2 SSO handlers, and v3 MCP tools all call unchanged.
func ExecPod(ctx context.Context, p credentials.Provider, args ExecPodArgs) (ExecResult, error) {
	cfg, err := buildRestConfig(ctx, p, args.Cluster)
	if err != nil {
		return ExecResult{}, err
	}
	// Inject session-id annotation header so v2 K8s audit can join app
	// audit on the UUID. The header is harmless on v1 — the apiserver
	// just ignores unknown headers — and sets us up for v2 without a
	// migration. Wrapping a fresh copy avoids mutating shared transport
	// state across requests.
	if args.SessionID != "" {
		cfg = withSessionIDHeader(cfg, args.SessionID)
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return ExecResult{}, fmt.Errorf("build clientset: %w", err)
	}

	container, err := resolveContainer(ctx, cs, args.Namespace, args.Pod, args.Container)
	if err != nil {
		return ExecResult{}, err
	}

	cmd := args.Command
	if len(cmd) == 0 {
		cmd = DefaultShellCommand
	}

	resolved := ResolvedExec{Container: container, Command: cmd}

	req := cs.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(args.Pod).
		Namespace(args.Namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: container,
			Command:   cmd,
			Stdin:     args.Stdin != nil,
			Stdout:    args.Stdout != nil,
			Stderr:    args.Stderr != nil,
			TTY:       args.TTY,
		}, scheme.ParameterCodec)

	mode := ModeWSThenSPDY
	if args.Policy != nil {
		mode = args.Policy.Choose(args.Cluster.Name)
	}

	transport, streamErr := runWithFallback(ctx, cfg, req.URL(), mode, remotecommand.StreamOptions{
		Stdin:             args.Stdin,
		Stdout:            args.Stdout,
		Stderr:            args.Stderr,
		Tty:               args.TTY,
		TerminalSizeQueue: chanQueue(args.TerminalSize),
	}, args.Policy, args.Cluster.Name)

	if streamErr == nil {
		return ExecResult{ExitCode: 0, Reason: "completed", Resolved: resolved, Transport: transport}, nil
	}

	// Container exited with a non-zero status — surface the exit code.
	var codeErr exec_util.CodeExitError
	if errors.As(streamErr, &codeErr) {
		return ExecResult{ExitCode: codeErr.Code, Reason: "container_exit", Resolved: resolved, Transport: transport}, nil
	}

	if errors.Is(streamErr, context.Canceled) || errors.Is(streamErr, context.DeadlineExceeded) {
		return ExecResult{ExitCode: -1, Reason: "stream_canceled", Resolved: resolved, Transport: transport}, streamErr
	}

	return ExecResult{ExitCode: -1, Resolved: resolved, Transport: transport}, streamErr
}

// runWithFallback streams the exec attempt through the chosen transport
// and falls back to SPDY when the WS upgrade is rejected (HTTP 4xx with
// no upgrade response). When a Policy is supplied, it observes the
// outcome so the breaker can pin the cluster on consecutive WS failures.
//
// This is our own implementation of FallbackExecutor's behavior: same
// shape as client-go's NewFallbackExecutor but instrumented so we know
// which transport actually succeeded.
func runWithFallback(
	ctx context.Context,
	cfg *rest.Config,
	u *url.URL,
	mode PolicyMode,
	opts remotecommand.StreamOptions,
	policy *Policy,
	clusterName string,
) (PolicyTransport, error) {
	if mode == ModeSPDYOnly {
		err := streamSPDY(ctx, cfg, u, opts)
		if policy != nil {
			policy.RecordResult(clusterName, ModeSPDYOnly, false, err == nil)
		}
		return TransportSPDY, err
	}

	// ws_then_spdy: attempt WebSocket first.
	wsErr := streamWebSocket(ctx, cfg, u, opts)
	if wsErr == nil {
		if policy != nil {
			policy.RecordResult(clusterName, ModeWSThenSPDY, false, true)
		}
		return TransportWS, nil
	}

	// Only fall back when the apiserver explicitly rejected the upgrade —
	// other errors (auth, connection refused, etc.) are real failures
	// SPDY can't recover from. This matches client-go's predicate.
	if !httpstream.IsUpgradeFailure(wsErr) {
		if policy != nil {
			policy.RecordResult(clusterName, ModeWSThenSPDY, true, false)
		}
		return TransportWS, wsErr
	}

	// WS handshake failed but a fallback may succeed.
	spdyErr := streamSPDY(ctx, cfg, u, opts)
	if policy != nil {
		policy.RecordResult(clusterName, ModeWSThenSPDY, true, spdyErr == nil)
	}
	if spdyErr != nil {
		return TransportSPDY, spdyErr
	}
	return TransportSPDY, nil
}

func streamWebSocket(ctx context.Context, cfg *rest.Config, u *url.URL, opts remotecommand.StreamOptions) error {
	exec, err := remotecommand.NewWebSocketExecutor(cfg, "GET", u.String())
	if err != nil {
		return fmt.Errorf("ws executor: %w", err)
	}
	return exec.StreamWithContext(ctx, opts)
}

func streamSPDY(ctx context.Context, cfg *rest.Config, u *url.URL, opts remotecommand.StreamOptions) error {
	exec, err := remotecommand.NewSPDYExecutor(cfg, "POST", u)
	if err != nil {
		return fmt.Errorf("spdy executor: %w", err)
	}
	return exec.StreamWithContext(ctx, opts)
}

// withSessionIDHeader returns a copy of cfg whose transport injects the
// audit.periscope.io/session-id header on every outgoing request. The
// apiserver echoes this into its audit annotations when audit policy
// captures request headers, so v2 compliance teams can pivot from a
// Periscope app log entry to the cluster's audit log on the same UUID.
func withSessionIDHeader(cfg *rest.Config, sessionID string) *rest.Config {
	out := rest.CopyConfig(cfg)
	prev := out.WrapTransport
	out.WrapTransport = func(rt http.RoundTripper) http.RoundTripper {
		if prev != nil {
			rt = prev(rt)
		}
		return roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			// Avoid clobbering an existing header — chain-friendly.
			if req.Header.Get(sessionIDHeader) == "" {
				req.Header.Set(sessionIDHeader, sessionID)
			}
			return rt.RoundTrip(req)
		})
	}
	return out
}

const sessionIDHeader = "Audit-Annotation-Periscope-Session-Id"

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// chanTermSizeQueue adapts a <-chan TerminalSize into the
// remotecommand.TerminalSizeQueue interface that StreamWithContext expects.
type chanTermSizeQueue struct {
	ch <-chan remotecommand.TerminalSize
}

// Next returns the next size, or nil when the channel is closed (which
// signals the executor to stop polling for resizes).
func (q chanTermSizeQueue) Next() *remotecommand.TerminalSize {
	s, ok := <-q.ch
	if !ok {
		return nil
	}
	return &s
}

func chanQueue(ch <-chan remotecommand.TerminalSize) remotecommand.TerminalSizeQueue {
	if ch == nil {
		return nil
	}
	return chanTermSizeQueue{ch: ch}
}
