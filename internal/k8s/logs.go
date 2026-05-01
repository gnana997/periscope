package k8s

import (
	"context"
	"fmt"
	"io"
	"time"

	corev1 "k8s.io/api/core/v1"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

// PodLogsArgs are the read parameters for a single-pod log stream.
//
// Empty Container selects the pod's first container (k8s default). When
// the pod has multiple containers, callers should set this explicitly.
type PodLogsArgs struct {
	Cluster      clusters.Cluster
	Namespace    string
	Name         string
	Container    string
	TailLines    *int64
	SinceSeconds *int64
	Previous     bool
	Follow       bool
	Timestamps   bool
}

// OpenPodLogStream opens a streaming reader for the pod's logs. The caller
// owns the returned ReadCloser and must Close it. When Follow is true, the
// stream stays open until the pod terminates or ctx is cancelled.
func OpenPodLogStream(ctx context.Context, p credentials.Provider, args PodLogsArgs) (io.ReadCloser, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return nil, fmt.Errorf("build clientset: %w", err)
	}
	opts := &corev1.PodLogOptions{
		Container:    args.Container,
		Follow:       args.Follow,
		Previous:     args.Previous,
		TailLines:    args.TailLines,
		SinceSeconds: args.SinceSeconds,
		Timestamps:   args.Timestamps,
	}
	req := cs.CoreV1().Pods(args.Namespace).GetLogs(args.Name, opts)
	rc, err := req.Stream(ctx)
	if err != nil {
		return nil, fmt.Errorf("open log stream %s/%s: %w", args.Namespace, args.Name, err)
	}
	return rc, nil
}

// SplitLogTimestamp separates the leading RFC3339Nano timestamp that k8s
// prepends when PodLogOptions.Timestamps is true. Returns ("", line) when
// the line doesn't have one (e.g. multi-line continuations from container
// runtimes that don't re-stamp every fragment).
func SplitLogTimestamp(line string) (string, string) {
	// K8s emits "2026-05-01T12:34:56.123456789Z message...". The first
	// space-separated token is always 30..35 chars for RFC3339Nano UTC.
	for i := 0; i < len(line) && i < 36; i++ {
		if line[i] == ' ' {
			ts := line[:i]
			if _, err := time.Parse(time.RFC3339Nano, ts); err == nil {
				return ts, line[i+1:]
			}
			return "", line
		}
	}
	return "", line
}
