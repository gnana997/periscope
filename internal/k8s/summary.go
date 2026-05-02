package k8s

import (
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

type GetClusterSummaryArgs struct {
	Cluster clusters.Cluster
}

// failingReasonSet is the wait/term reasons that flip a pod into the
// "needs attention" bucket on the Overview page. Kubectl surfaces these
// separately from the Phase enum because the K8s API itself reports
// most of them as Pending or Running with a sub-state.
var failingReasonSet = map[string]struct{}{
	"CrashLoopBackOff":           {},
	"ImagePullBackOff":           {},
	"ErrImagePull":               {},
	"ErrImageNeverPull":          {},
	"InvalidImageName":           {},
	"CreateContainerConfigError": {},
	"CreateContainerError":       {},
	"RunContainerError":          {},
	"OOMKilled":                  {},
	"DeadlineExceeded":           {},
	"Evicted":                    {},
}

// needsAttentionLimit caps the curated failing-pods list. Operators
// usually have a handful of bad pods at any time; more than 20 means
// the dashboard becomes a wall of red noise and they need to filter the
// Pods page anyway.
const needsAttentionLimit = 20

// topPodsLimit is the size of the top-by-CPU and top-by-memory lists.
const topPodsLimit = 5

// GetClusterSummary returns the data the Overview page renders. It runs
// the independent fetches in parallel — on a kind-local cluster this
// shaves the dashboard from ~6 sequential round trips down to one
// wall-clock roundtrip. Failure of an optional fetch (workload kinds,
// metrics-server, PV/PVC) degrades that section silently rather than
// blanking the whole page.
// fillOKAccessibility marks any unset Accessibility field as ok.
// Called at the end of GetClusterSummary so success paths don't have
// to set it explicitly inside every goroutine.
func fillOKAccessibility(s *ClusterSummary) {
	if s.Accessibility.Nodes == "" { s.Accessibility.Nodes = AccessOK }
	if s.Accessibility.Pods == "" { s.Accessibility.Pods = AccessOK }
	if s.Accessibility.Namespaces == "" { s.Accessibility.Namespaces = AccessOK }
	if s.Accessibility.Metrics == "" { s.Accessibility.Metrics = AccessOK }
}

func GetClusterSummary(ctx context.Context, p credentials.Provider, args GetClusterSummaryArgs) (ClusterSummary, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return ClusterSummary{}, fmt.Errorf("build clientset: %w", err)
	}

	// Required up-front: server version is cheap and identifies the
	// cluster's K8s release for the identity banner.
	serverVersion, err := cs.Discovery().ServerVersion()
	if err != nil {
		return ClusterSummary{}, fmt.Errorf("get server version: %w", err)
	}

	// All the slow fetches run in parallel. We collect into pre-zeroed
	// fields and protect with mutexes only where needed (counts and
	// list slices — node/pod totals are computed in their own
	// goroutines, no shared state).

	var (
		summary ClusterSummary
		mu      sync.Mutex // guards summary mutations from helper goroutines
		wg      sync.WaitGroup

		// podList is captured for the post-parallel top-pods pass so we
		// can resolve per-pod limits without re-listing pods.
		podList *corev1.PodList

		totalCPUMillis int64
		totalMemBytes  int64
	)

	summary.KubernetesVersion = serverVersion.GitVersion
	summary.Provider = providerLabel(args.Cluster.Backend)

	// --- Nodes (required-ish — drives the capacity card) ---------------
	wg.Add(1)
	go func() {
		defer wg.Done()
		ns, err := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
		if err != nil {
			mu.Lock()
			summary.Accessibility.Nodes = classifyAccess(err)
			mu.Unlock()
			return
		}
		ready := 0
		var cpu, mem int64
		for _, n := range ns.Items {
			if nodeStatus(n.Status.Conditions) == "Ready" {
				ready++
			}
			cpu += n.Status.Allocatable.Cpu().MilliValue()
			mem += n.Status.Allocatable.Memory().Value()
		}
		mu.Lock()
		summary.NodeCount = len(ns.Items)
		summary.NodeReadyCount = ready
		summary.CPUAllocatable = formatCPU(cpu)
		summary.MemoryAllocatable = formatMemory(mem)
		totalCPUMillis = cpu
		totalMemBytes = mem
		mu.Unlock()
	}()

	// --- Pods (required — drives phase distribution + failing list) ---
	wg.Add(1)
	go func() {
		defer wg.Done()
		ps, err := cs.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
		if err != nil {
			mu.Lock()
			summary.Accessibility.Pods = classifyAccess(err)
			mu.Unlock()
			return
		}
		podList = ps
		phases, failing := computePodHealth(ps.Items)
		mu.Lock()
		summary.PodCount = len(ps.Items)
		summary.PodPhases = phases
		summary.NeedsAttention = failing
		mu.Unlock()
	}()

	// --- Namespaces -----------------------------------------------------
	wg.Add(1)
	go func() {
		defer wg.Done()
		ns, err := cs.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
		if err != nil {
			mu.Lock()
			summary.Accessibility.Namespaces = classifyAccess(err)
			mu.Unlock()
			return
		}
		mu.Lock()
		summary.NamespaceCount = len(ns.Items)
		mu.Unlock()
	}()

	// --- Workload kinds (one goroutine each) ---------------------------
	wg.Add(1)
	go func() {
		defer wg.Done()
		ds, err := cs.AppsV1().Deployments("").List(ctx, metav1.ListOptions{})
		if err != nil {
			return
		}
		healthy := 0
		for _, d := range ds.Items {
			if d.Status.AvailableReplicas == d.Status.Replicas && d.Status.Replicas > 0 {
				healthy++
			} else if d.Status.Replicas == 0 {
				healthy++ // intentionally scaled to zero — not failing
			}
		}
		mu.Lock()
		summary.Workloads.Deployments = WorkloadCount{Total: len(ds.Items), Healthy: healthy}
		mu.Unlock()
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		ss, err := cs.AppsV1().StatefulSets("").List(ctx, metav1.ListOptions{})
		if err != nil {
			return
		}
		healthy := 0
		for _, s := range ss.Items {
			if s.Status.ReadyReplicas == s.Status.Replicas && s.Status.Replicas > 0 {
				healthy++
			} else if s.Status.Replicas == 0 {
				healthy++
			}
		}
		mu.Lock()
		summary.Workloads.StatefulSets = WorkloadCount{Total: len(ss.Items), Healthy: healthy}
		mu.Unlock()
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		dms, err := cs.AppsV1().DaemonSets("").List(ctx, metav1.ListOptions{})
		if err != nil {
			return
		}
		healthy := 0
		for _, d := range dms.Items {
			if d.Status.NumberReady == d.Status.DesiredNumberScheduled && d.Status.DesiredNumberScheduled > 0 {
				healthy++
			} else if d.Status.DesiredNumberScheduled == 0 {
				healthy++
			}
		}
		mu.Lock()
		summary.Workloads.DaemonSets = WorkloadCount{Total: len(dms.Items), Healthy: healthy}
		mu.Unlock()
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		js, err := cs.BatchV1().Jobs("").List(ctx, metav1.ListOptions{})
		if err != nil {
			return
		}
		healthy := 0
		for _, j := range js.Items {
			// A Job is "healthy" when it's running with no failures or has completed
			// successfully. Consider failed if there are failed pods and no succeeded.
			if j.Status.Failed == 0 || j.Status.Succeeded > 0 {
				healthy++
			}
		}
		mu.Lock()
		summary.Workloads.Jobs = WorkloadCount{Total: len(js.Items), Healthy: healthy}
		mu.Unlock()
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		cjs, err := cs.BatchV1().CronJobs("").List(ctx, metav1.ListOptions{})
		if err != nil {
			return
		}
		healthy := 0
		for _, c := range cjs.Items {
			// CronJobs have no inherent "health" — treat Suspend != true as healthy.
			if c.Spec.Suspend == nil || !*c.Spec.Suspend {
				healthy++
			}
		}
		mu.Lock()
		summary.Workloads.CronJobs = WorkloadCount{Total: len(cjs.Items), Healthy: healthy}
		mu.Unlock()
	}()

	// --- Storage (PV + PVC) --------------------------------------------
	wg.Add(1)
	go func() {
		defer wg.Done()
		pvs, err := cs.CoreV1().PersistentVolumes().List(ctx, metav1.ListOptions{})
		if err != nil {
			return
		}
		var totalBytes int64
		for _, pv := range pvs.Items {
			if pv.Status.Phase == corev1.VolumeBound {
				if cap, ok := pv.Spec.Capacity[corev1.ResourceStorage]; ok {
					totalBytes += cap.Value()
				}
			}
		}
		mu.Lock()
		summary.Storage.PVCount = len(pvs.Items)
		if totalBytes > 0 {
			summary.Storage.TotalProvisioned = formatMemory(totalBytes)
		}
		mu.Unlock()
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		pvcs, err := cs.CoreV1().PersistentVolumeClaims("").List(ctx, metav1.ListOptions{})
		if err != nil {
			return
		}
		bound := 0
		pending := 0
		for _, pvc := range pvcs.Items {
			switch pvc.Status.Phase {
			case corev1.ClaimBound:
				bound++
			case corev1.ClaimPending:
				pending++
			}
		}
		mu.Lock()
		summary.Storage.PVCBound = bound
		summary.Storage.PVCPending = pending
		mu.Unlock()
	}()

	wg.Wait()
	fillOKAccessibility(&summary)

	// --- Metrics-server (cluster-wide CPU/memory + top pods) -----------
	// Sequential after the parallel section so we have nodeList /
	// podList for percentage math against allocatable + per-pod limits.
	mc, err := newMetricsClientFn(ctx, p, args.Cluster)
	if err != nil {
		// MetricsAvailable=false and skip.
		// Provider built no metrics client (e.g. RBAC denies) — record
		// the access reason and surface the legacy MetricsAvailable bool.
		summary.Accessibility.Metrics = classifyAccess(err)
		summary.MetricsAvailable = false
		return summary, nil
	}
	nodeMetrics, err := mc.MetricsV1beta1().NodeMetricses().List(ctx, metav1.ListOptions{})
	if err != nil {
		if isMetricsUnavailable(err) {
			summary.Accessibility.Metrics = AccessUnavailable
			summary.MetricsAvailable = false
			return summary, nil
		}
		summary.Accessibility.Metrics = classifyAccess(err)
		summary.MetricsAvailable = false
		return summary, nil
	}
	var usedCPU, usedMem int64
	for _, nm := range nodeMetrics.Items {
		usedCPU += nm.Usage.Cpu().MilliValue()
		usedMem += nm.Usage.Memory().Value()
	}
	summary.MetricsAvailable = true
	summary.Accessibility.Metrics = AccessOK
	summary.CPUUsed = formatCPU(usedCPU)
	summary.MemoryUsed = formatMemory(usedMem)
	if totalCPUMillis > 0 {
		summary.CPUPercent = pct(usedCPU, totalCPUMillis)
	}
	if totalMemBytes > 0 {
		summary.MemoryPercent = pct(usedMem, totalMemBytes)
	}

	// Per-pod metrics for the top-N tables. Best-effort: failure leaves
	// the cards empty but doesn't tank the page.
	podMetrics, err := mc.MetricsV1beta1().PodMetricses("").List(ctx, metav1.ListOptions{})
	if err == nil && podList != nil {
		summary.TopByCPU, summary.TopByMemory = computeTopPods(podMetrics.Items, podList.Items, totalCPUMillis, totalMemBytes)
	}

	return summary, nil
}

// computePodHealth derives the phase distribution and the curated
// "needs attention" list from the cluster's pod list.
func computePodHealth(pods []corev1.Pod) (PodPhaseCounts, []FailingPod) {
	var phases PodPhaseCounts
	failing := make([]FailingPod, 0, 8)

	for i := range pods {
		pod := &pods[i]

		// Kubectl-style: a pod is "Stuck" if any container reports a
		// failing wait/term reason, even if the K8s phase is still
		// Pending/Running. We look at the worst container per pod.
		stuckReason, stuckContainer, stuckMessage, restarts := worstContainerReason(pod)

		// Phase counts. Stuck wins over the raw phase so the donut
		// reflects what kubectl shows.
		switch {
		case stuckReason != "":
			phases.Stuck++
		case pod.Status.Phase == corev1.PodRunning:
			phases.Running++
		case pod.Status.Phase == corev1.PodPending:
			phases.Pending++
		case pod.Status.Phase == corev1.PodSucceeded:
			phases.Succeeded++
		case pod.Status.Phase == corev1.PodFailed:
			phases.Failed++
		default:
			phases.Unknown++
		}

		// Failing list. Cap at the limit but keep computing phases for
		// every pod (cheap).
		if len(failing) >= needsAttentionLimit {
			continue
		}
		switch {
		case stuckReason != "":
			failing = append(failing, FailingPod{
				Name:         pod.Name,
				Namespace:    pod.Namespace,
				Reason:       stuckReason,
				Container:    stuckContainer,
				Message:      stuckMessage,
				RestartCount: restarts,
				Phase:        string(pod.Status.Phase),
			})
		case pod.Status.Phase == corev1.PodFailed:
			failing = append(failing, FailingPod{
				Name:      pod.Name,
				Namespace: pod.Namespace,
				Reason:    nonEmpty(pod.Status.Reason, "Failed"),
				Message:   pod.Status.Message,
				Phase:     string(pod.Status.Phase),
			})
		}
	}

	// Sort failing list by (highest restart count desc, then name asc) so
	// the loudest offenders surface first. Stable so pods with equal
	// counts keep API order.
	sort.SliceStable(failing, func(i, j int) bool {
		if failing[i].RestartCount != failing[j].RestartCount {
			return failing[i].RestartCount > failing[j].RestartCount
		}
		return failing[i].Name < failing[j].Name
	})

	return phases, failing
}

// worstContainerReason returns the most operator-relevant wait/term
// reason across a pod's containers (init + main), or empty string when
// no container reports a known-bad state. The aggregate restartCount is
// the SUM across containers — it's the number kubectl shows in
// `RESTARTS` and matches what operators see in the table.
func worstContainerReason(pod *corev1.Pod) (reason, container, message string, restarts int32) {
	pick := func(statuses []corev1.ContainerStatus) (string, string, string, int32) {
		for _, c := range statuses {
			if c.State.Waiting != nil {
				if _, ok := failingReasonSet[c.State.Waiting.Reason]; ok {
					return c.State.Waiting.Reason, c.Name, c.State.Waiting.Message, c.RestartCount
				}
			}
			if c.LastTerminationState.Terminated != nil {
				if _, ok := failingReasonSet[c.LastTerminationState.Terminated.Reason]; ok {
					return c.LastTerminationState.Terminated.Reason, c.Name, c.LastTerminationState.Terminated.Message, c.RestartCount
				}
			}
			if c.State.Terminated != nil && c.State.Terminated.ExitCode != 0 {
				if _, ok := failingReasonSet[c.State.Terminated.Reason]; ok {
					return c.State.Terminated.Reason, c.Name, c.State.Terminated.Message, c.RestartCount
				}
			}
		}
		return "", "", "", 0
	}
	r, cn, msg, rc := pick(pod.Status.ContainerStatuses)
	if r == "" {
		r, cn, msg, rc = pick(pod.Status.InitContainerStatuses)
	}
	// Aggregate restarts across all containers regardless of which one
	// triggered the reason — matches kubectl's RESTARTS column.
	var total int32
	for _, c := range pod.Status.ContainerStatuses {
		total += c.RestartCount
	}
	if total > rc {
		rc = total
	}
	return r, cn, msg, rc
}

// computeTopPods returns the top-5 pods by CPU and by memory.
//
// Percent-of-pod-limit when the pod has a limit set; otherwise
// percent-of-cluster-allocatable (so we always show *something* and the
// UI tags which sense the percentage carries).
func computeTopPods(metrics []metricsv1beta1.PodMetrics, pods []corev1.Pod, clusterCPUMillis, clusterMemBytes int64) (top5CPU, top5Mem []TopPod) {
	// Build a name→pod lookup so we can resolve limits for each
	// metrics record without a second list call.
	type key struct{ ns, name string }
	podByKey := make(map[key]*corev1.Pod, len(pods))
	for i := range pods {
		podByKey[key{pods[i].Namespace, pods[i].Name}] = &pods[i]
	}

	type ranked struct {
		TopPod
		raw int64
	}
	cpuRanked := make([]ranked, 0, len(metrics))
	memRanked := make([]ranked, 0, len(metrics))

	for _, m := range metrics {
		var cpuMillis, memBytes int64
		for _, c := range m.Containers {
			cpuMillis += c.Usage.Cpu().MilliValue()
			memBytes += c.Usage.Memory().Value()
		}
		pod := podByKey[key{m.Namespace, m.Name}]
		cpuLimit, memLimit := podLimits(pod)

		cpuPct, cpuOfLimit := percentOf(cpuMillis, cpuLimit, clusterCPUMillis)
		memPct, memOfLimit := percentOf(memBytes, memLimit, clusterMemBytes)

		cpuRanked = append(cpuRanked, ranked{
			TopPod: TopPod{
				Name:      m.Name,
				Namespace: m.Namespace,
				Usage:     formatCPU(cpuMillis),
				Percent:   cpuPct,
				OfLimit:   cpuOfLimit,
			},
			raw: cpuMillis,
		})
		memRanked = append(memRanked, ranked{
			TopPod: TopPod{
				Name:      m.Name,
				Namespace: m.Namespace,
				Usage:     formatMemory(memBytes),
				Percent:   memPct,
				OfLimit:   memOfLimit,
			},
			raw: memBytes,
		})
	}

	sort.Slice(cpuRanked, func(i, j int) bool { return cpuRanked[i].raw > cpuRanked[j].raw })
	sort.Slice(memRanked, func(i, j int) bool { return memRanked[i].raw > memRanked[j].raw })

	for i := 0; i < topPodsLimit && i < len(cpuRanked); i++ {
		top5CPU = append(top5CPU, cpuRanked[i].TopPod)
	}
	for i := 0; i < topPodsLimit && i < len(memRanked); i++ {
		top5Mem = append(top5Mem, memRanked[i].TopPod)
	}
	return top5CPU, top5Mem
}

// podLimits sums per-container CPU/memory limits across a pod. Returns
// 0 when limits aren't set on every container — partial limits aren't
// meaningful for "% of limit" math.
func podLimits(pod *corev1.Pod) (cpuMillis, memBytes int64) {
	if pod == nil {
		return 0, 0
	}
	var cpu, mem int64
	allHaveCPU, allHaveMem := true, true
	for _, c := range pod.Spec.Containers {
		if q, ok := c.Resources.Limits[corev1.ResourceCPU]; ok {
			cpu += q.MilliValue()
		} else {
			allHaveCPU = false
		}
		if q, ok := c.Resources.Limits[corev1.ResourceMemory]; ok {
			mem += q.Value()
		} else {
			allHaveMem = false
		}
	}
	if !allHaveCPU {
		cpu = 0
	}
	if !allHaveMem {
		mem = 0
	}
	return cpu, mem
}

// percentOf returns (percent, isOfLimit). When limit > 0, the percent
// is usage/limit and isOfLimit=true. When limit is unset, falls back to
// usage/clusterAllocatable so the UI has something to render — caller
// uses isOfLimit to label the percentage correctly.
func percentOf(usage, limit, clusterTotal int64) (float64, bool) {
	if limit > 0 {
		return pct(usage, limit), true
	}
	if clusterTotal > 0 {
		return pct(usage, clusterTotal), false
	}
	return -1, false
}

func nonEmpty(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func providerLabel(backend string) string {
	switch strings.ToLower(backend) {
	case clusters.BackendKubeconfig:
		return "Kubeconfig"
	default:
		return "EKS"
	}
}


// classifyAccess maps a list-call error to the AccessStatus value
// to record on the ClusterSummary. nil err → AccessOK.
func classifyAccess(err error) AccessStatus {
	if err == nil {
		return AccessOK
	}
	if k8serrors.IsForbidden(err) {
		return AccessForbidden
	}
	return AccessUnavailable
}
