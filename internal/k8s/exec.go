package k8s

import (
	"context"
	"errors"
	"fmt"
	"io"
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
// one. Tries bash first, falls back to sh, so we work on both glibc and
// alpine images. Distroless images without /bin/sh produce E_NO_SHELL.
var DefaultShellCommand = []string{
	"/bin/sh", "-c",
	"exec /bin/bash 2>/dev/null || exec /bin/sh",
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
	// joined to other records keyed on the same UUID. Cluster-side audit
	// integration is a v2 concern and not wired through transport here.
	SessionID string
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

	executor, err := buildFallbackExecutor(cfg, req.URL())
	if err != nil {
		return ExecResult{Resolved: resolved}, fmt.Errorf("build executor: %w", err)
	}

	streamErr := executor.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:             args.Stdin,
		Stdout:            args.Stdout,
		Stderr:            args.Stderr,
		Tty:               args.TTY,
		TerminalSizeQueue: chanQueue(args.TerminalSize),
	})

	if streamErr == nil {
		return ExecResult{ExitCode: 0, Reason: "completed", Resolved: resolved}, nil
	}

	// Container exited with a non-zero status — surface the exit code.
	var codeErr exec_util.CodeExitError
	if errors.As(streamErr, &codeErr) {
		return ExecResult{ExitCode: codeErr.Code, Reason: "container_exit", Resolved: resolved}, nil
	}

	if errors.Is(streamErr, context.Canceled) || errors.Is(streamErr, context.DeadlineExceeded) {
		return ExecResult{ExitCode: -1, Reason: "stream_canceled", Resolved: resolved}, streamErr
	}

	return ExecResult{ExitCode: -1, Resolved: resolved}, streamErr
}

// buildFallbackExecutor returns a remotecommand.Executor that prefers
// WebSocket (v5.channel.k8s.io) and falls back to SPDY when the apiserver
// or an intermediate proxy rejects the upgrade. This is the same shape
// kubectl uses since 1.31.
func buildFallbackExecutor(cfg *rest.Config, u *url.URL) (remotecommand.Executor, error) {
	wsExec, err := remotecommand.NewWebSocketExecutor(cfg, "GET", u.String())
	if err != nil {
		return nil, fmt.Errorf("ws executor: %w", err)
	}
	spdyExec, err := remotecommand.NewSPDYExecutor(cfg, "POST", u)
	if err != nil {
		return nil, fmt.Errorf("spdy executor: %w", err)
	}
	exec, err := remotecommand.NewFallbackExecutor(wsExec, spdyExec, httpstream.IsUpgradeFailure)
	if err != nil {
		return nil, fmt.Errorf("fallback executor: %w", err)
	}
	return exec, nil
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
