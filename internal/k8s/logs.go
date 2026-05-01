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

// PodAttribution identifies a streaming pod inside an aggregated workload
// log stream. Node carries the pod's scheduled node name (empty for
// not-yet-scheduled pods) and is what makes DaemonSet streams readable.
type PodAttribution struct {
	Name string `json:"name"`
	Node string `json:"node"`
}

// WorkloadLogsArgs are the read parameters for an aggregated multi-pod
// stream backing a controller (Deployment/StatefulSet/DaemonSet/Job).
type WorkloadLogsArgs struct {
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

// DeploymentLogsArgs is preserved as an alias for callers that pre-date
// the multi-workload abstraction. New callers should use WorkloadLogsArgs.
type DeploymentLogsArgs = WorkloadLogsArgs

// DeploymentLogSink receives streamed events from any aggregated workload
// log stream. Implementations must be safe for concurrent use — Line is
// invoked from per-pod goroutines, PodSet from the Watch goroutine.
//
// (The name predates the multi-workload abstraction. Renaming would churn
// the consumer side without behavioral change; left as-is for now.)
type DeploymentLogSink interface {
	Line(pod, line string)
	PodSet(pods []PodAttribution)
}

// selectorFetcher retrieves a workload's pod-template label selector.
// Used to keep streamWorkloadLogs generic across kinds.
type selectorFetcher func(ctx context.Context, cs kubernetes.Interface, ns, name string) (*metav1.LabelSelector, error)

// streamWorkloadLogs is the shared engine behind StreamDeploymentLogs and
// the StatefulSet/DaemonSet/Job variants. It resolves the workload's pod
// label selector via fetchSelector, then lists + watches matching pods
// and fans their log streams into the sink.
func streamWorkloadLogs(
	ctx context.Context,
	p credentials.Provider,
	args WorkloadLogsArgs,
	sink DeploymentLogSink,
	fetchSelector selectorFetcher,
) error {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return fmt.Errorf("build clientset: %w", err)
	}

	rawSel, err := fetchSelector(ctx, cs, args.Namespace, args.Name)
	if err != nil {
		return fmt.Errorf("get workload %s/%s: %w", args.Namespace, args.Name, err)
	}
	if rawSel == nil {
		return fmt.Errorf("workload %s/%s has no selector", args.Namespace, args.Name)
	}
	selector, err := metav1.LabelSelectorAsSelector(rawSel)
	if err != nil {
		return fmt.Errorf("invalid selector on workload %s/%s: %w", args.Namespace, args.Name, err)
	}
	if selector.Empty() {
		return fmt.Errorf("workload %s/%s has empty selector", args.Namespace, args.Name)
	}
	selStr := selector.String()

	list, err := cs.CoreV1().Pods(args.Namespace).List(ctx, metav1.ListOptions{LabelSelector: selStr})
	if err != nil {
		return fmt.Errorf("list pods for workload %s/%s: %w", args.Namespace, args.Name, err)
	}

	type streamerEntry struct {
		cancel context.CancelFunc
		node   string
	}
	var mu sync.Mutex
	streamers := map[string]streamerEntry{}
	var wg sync.WaitGroup

	currentPodSet := func() []PodAttribution {
		mu.Lock()
		defer mu.Unlock()
		out := make([]PodAttribution, 0, len(streamers))
		for name, e := range streamers {
			out = append(out, PodAttribution{Name: name, Node: e.node})
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
		return out
	}

	startStreamer := func(pod *corev1.Pod) {
		mu.Lock()
		if _, exists := streamers[pod.Name]; exists {
			mu.Unlock()
			return
		}
		streamerCtx, cancel := context.WithCancel(ctx)
		streamers[pod.Name] = streamerEntry{cancel: cancel, node: pod.Spec.NodeName}
		mu.Unlock()

		wg.Add(1)
		go func(podName string) {
			defer wg.Done()
			streamSinglePodToSink(streamerCtx, cs, args, podName, sink)
			mu.Lock()
			if e, ok := streamers[podName]; ok {
				e.cancel()
				delete(streamers, podName)
			}
			mu.Unlock()
		}(pod.Name)
	}

	stopStreamer := func(podName string) {
		mu.Lock()
		e, ok := streamers[podName]
		if ok {
			delete(streamers, podName)
		}
		mu.Unlock()
		if ok {
			e.cancel()
		}
	}

	for i := range list.Items {
		startStreamer(&list.Items[i])
	}
	sink.PodSet(currentPodSet())

	watcher, werr := cs.CoreV1().Pods(args.Namespace).Watch(ctx, metav1.ListOptions{
		LabelSelector:   selStr,
		ResourceVersion: list.ResourceVersion,
	})
	if werr != nil {
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
				startStreamer(pod)
				sink.PodSet(currentPodSet())
			case watch.Deleted:
				stopStreamer(pod.Name)
				sink.PodSet(currentPodSet())
			}
		}
	}
}

// streamSinglePodToSink scans logs for one pod and forwards every line to
// the sink. Returns silently on transient errors (pod not yet ready, just
// terminated, etc.).
func streamSinglePodToSink(ctx context.Context, cs kubernetes.Interface, args WorkloadLogsArgs, podName string, sink DeploymentLogSink) {
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

// --- Public per-workload streamers ---

// StreamDeploymentLogs aggregates logs from every pod matching the
// deployment's selector into a single sink. New pods that come up during
// the stream (e.g. rolling restart) are automatically picked up via Watch;
// terminating pods drop off when their log stream ends.
func StreamDeploymentLogs(ctx context.Context, p credentials.Provider, args WorkloadLogsArgs, sink DeploymentLogSink) error {
	return streamWorkloadLogs(ctx, p, args, sink, fetchDeploymentSelector)
}

// StreamStatefulSetLogs is like StreamDeploymentLogs but resolves pods via
// the StatefulSet's selector.
func StreamStatefulSetLogs(ctx context.Context, p credentials.Provider, args WorkloadLogsArgs, sink DeploymentLogSink) error {
	return streamWorkloadLogs(ctx, p, args, sink, fetchStatefulSetSelector)
}

// StreamDaemonSetLogs is like StreamDeploymentLogs but resolves pods via
// the DaemonSet's selector. Useful for "show me all kube-proxy logs at once".
func StreamDaemonSetLogs(ctx context.Context, p credentials.Provider, args WorkloadLogsArgs, sink DeploymentLogSink) error {
	return streamWorkloadLogs(ctx, p, args, sink, fetchDaemonSetSelector)
}

// StreamJobLogs is like StreamDeploymentLogs but resolves pods via the
// Job's selector. Captures both the live pod and any retried/completed
// pods still readable via the kubelet.
func StreamJobLogs(ctx context.Context, p credentials.Provider, args WorkloadLogsArgs, sink DeploymentLogSink) error {
	return streamWorkloadLogs(ctx, p, args, sink, fetchJobSelector)
}

// --- Per-workload selector fetchers ---

func fetchDeploymentSelector(ctx context.Context, cs kubernetes.Interface, ns, name string) (*metav1.LabelSelector, error) {
	d, err := cs.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return d.Spec.Selector, nil
}

func fetchStatefulSetSelector(ctx context.Context, cs kubernetes.Interface, ns, name string) (*metav1.LabelSelector, error) {
	s, err := cs.AppsV1().StatefulSets(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return s.Spec.Selector, nil
}

func fetchDaemonSetSelector(ctx context.Context, cs kubernetes.Interface, ns, name string) (*metav1.LabelSelector, error) {
	d, err := cs.AppsV1().DaemonSets(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return d.Spec.Selector, nil
}

func fetchJobSelector(ctx context.Context, cs kubernetes.Interface, ns, name string) (*metav1.LabelSelector, error) {
	j, err := cs.BatchV1().Jobs(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return j.Spec.Selector, nil
}
