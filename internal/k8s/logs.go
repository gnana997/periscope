package k8s

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"sort"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"

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

// DeploymentLogsArgs are the read parameters for a multi-pod aggregated
// log stream backing a deployment. The same Container/TailLines/etc. apply
// uniformly across all matched pods.
type DeploymentLogsArgs struct {
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

// DeploymentLogSink receives streamed events from StreamDeploymentLogs.
// Implementations must be safe for concurrent use — Line is invoked from
// per-pod goroutines, PodSet from the Watch goroutine.
type DeploymentLogSink interface {
	// Line is called for each log line received. Pod is the source pod name;
	// line is the raw text (still carrying the leading RFC3339Nano timestamp
	// when Timestamps was true).
	Line(pod, line string)
	// PodSet is called whenever the streaming pod set changes — initial
	// listing, pod added by Watch, pod removed by Watch.
	PodSet(pods []string)
}

// StreamDeploymentLogs aggregates logs from every pod matching the
// deployment's selector into a single sink. New pods that come up during
// the stream (e.g. rolling restart) are automatically picked up via Watch;
// terminating pods drop off when their log stream ends.
//
// The function blocks until ctx is cancelled or the Watch ends. When ctx
// cancels, it waits for in-flight per-pod goroutines to drain before
// returning.
func StreamDeploymentLogs(ctx context.Context, p credentials.Provider, args DeploymentLogsArgs, sink DeploymentLogSink) error {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return fmt.Errorf("build clientset: %w", err)
	}

	deploy, err := cs.AppsV1().Deployments(args.Namespace).Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get deployment %s/%s: %w", args.Namespace, args.Name, err)
	}
	if deploy.Spec.Selector == nil {
		return fmt.Errorf("deployment %s/%s has no selector", args.Namespace, args.Name)
	}
	selector, err := metav1.LabelSelectorAsSelector(deploy.Spec.Selector)
	if err != nil {
		return fmt.Errorf("invalid selector on deployment %s/%s: %w", args.Namespace, args.Name, err)
	}
	if selector.Empty() {
		return fmt.Errorf("deployment %s/%s has empty selector", args.Namespace, args.Name)
	}
	selStr := selector.String()

	list, err := cs.CoreV1().Pods(args.Namespace).List(ctx, metav1.ListOptions{LabelSelector: selStr})
	if err != nil {
		return fmt.Errorf("list pods for deployment %s/%s: %w", args.Namespace, args.Name, err)
	}

	// Track active per-pod streamers. Each entry's CancelFunc tears down
	// its goroutine; goroutines also self-remove when their stream ends.
	var mu sync.Mutex
	streamers := map[string]context.CancelFunc{}
	var wg sync.WaitGroup

	currentPodNames := func() []string {
		mu.Lock()
		defer mu.Unlock()
		out := make([]string, 0, len(streamers))
		for name := range streamers {
			out = append(out, name)
		}
		sort.Strings(out)
		return out
	}

	startStreamer := func(podName string) {
		mu.Lock()
		if _, exists := streamers[podName]; exists {
			mu.Unlock()
			return
		}
		streamerCtx, cancel := context.WithCancel(ctx)
		streamers[podName] = cancel
		mu.Unlock()

		wg.Add(1)
		go func() {
			defer wg.Done()
			streamSinglePodToSink(streamerCtx, cs, args, podName, sink)
			mu.Lock()
			if cancel, ok := streamers[podName]; ok {
				cancel()
				delete(streamers, podName)
			}
			mu.Unlock()
		}()
	}

	stopStreamer := func(podName string) {
		mu.Lock()
		cancel, ok := streamers[podName]
		if ok {
			delete(streamers, podName)
		}
		mu.Unlock()
		if cancel != nil {
			cancel()
		}
	}

	for _, pod := range list.Items {
		startStreamer(pod.Name)
	}
	sink.PodSet(currentPodNames())

	watcher, werr := cs.CoreV1().Pods(args.Namespace).Watch(ctx, metav1.ListOptions{
		LabelSelector:   selStr,
		ResourceVersion: list.ResourceVersion,
	})
	if werr != nil {
		// Watch couldn't start; the initial pod set is still streaming.
		// Block until ctx cancels, then drain.
		<-ctx.Done()
		wg.Wait()
		return nil
	}
	defer watcher.Stop()

	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			return nil
		case event, ok := <-watcher.ResultChan():
			if !ok {
				// Watch closed (timeout, server hiccup). Initial streamers
				// stay alive; user can reload to re-establish the watch.
				<-ctx.Done()
				wg.Wait()
				return nil
			}
			pod, ok := event.Object.(*corev1.Pod)
			if !ok {
				continue
			}
			switch event.Type {
			case watch.Added:
				startStreamer(pod.Name)
				sink.PodSet(currentPodNames())
			case watch.Deleted:
				stopStreamer(pod.Name)
				sink.PodSet(currentPodNames())
			}
		}
	}
}

// streamSinglePodToSink scans logs for one pod and forwards every line to
// the sink. Returns silently on transient errors (pod not yet ready, just
// terminated, etc.) — the caller will pick the pod up again via Watch if
// it comes back.
func streamSinglePodToSink(ctx context.Context, cs kubernetes.Interface, args DeploymentLogsArgs, podName string, sink DeploymentLogSink) {
	opts := &corev1.PodLogOptions{
		Container:    args.Container,
		Follow:       args.Follow,
		Previous:     args.Previous,
		TailLines:    args.TailLines,
		SinceSeconds: args.SinceSeconds,
		Timestamps:   args.Timestamps,
	}
	rc, err := cs.CoreV1().Pods(args.Namespace).GetLogs(podName, opts).Stream(ctx)
	if err != nil {
		return
	}
	defer rc.Close()

	scanner := bufio.NewScanner(rc)
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
			sink.Line(podName, scanner.Text())
		}
	}
}
