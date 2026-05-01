package k8s

import (
	"context"
	"fmt"
	"sort"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

// jobChildPodLimit caps the number of child pods rendered inline on
// JobDetail. Most Jobs have 1 — if a user has dozens of retry pods, we
// show the most recent and let them visit the Pods view filtered.
const jobChildPodLimit = 20

type ListJobsArgs struct {
	Cluster   clusters.Cluster
	Namespace string
}

func ListJobs(ctx context.Context, p credentials.Provider, args ListJobsArgs) (JobList, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return JobList{}, fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.BatchV1().Jobs(args.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return JobList{}, fmt.Errorf("list jobs: %w", err)
	}

	out := JobList{Jobs: make([]Job, 0, len(raw.Items))}
	for _, j := range raw.Items {
		out.Jobs = append(out.Jobs, jobSummary(&j))
	}
	return out, nil
}

type GetJobArgs struct {
	Cluster   clusters.Cluster
	Namespace string
	Name      string
}

func GetJob(ctx context.Context, p credentials.Provider, args GetJobArgs) (JobDetail, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return JobDetail{}, fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.BatchV1().Jobs(args.Namespace).Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		return JobDetail{}, fmt.Errorf("get job %s/%s: %w", args.Namespace, args.Name, err)
	}

	containers := make([]ContainerSpec, 0, len(raw.Spec.Template.Spec.Containers))
	for _, c := range raw.Spec.Template.Spec.Containers {
		containers = append(containers, ContainerSpec{Name: c.Name, Image: c.Image})
	}

	conds := make([]JobCondition, 0, len(raw.Status.Conditions))
	for _, c := range raw.Status.Conditions {
		conds = append(conds, JobCondition{
			Type:    string(c.Type),
			Status:  string(c.Status),
			Reason:  c.Reason,
			Message: c.Message,
		})
	}

	var selector map[string]string
	if raw.Spec.Selector != nil {
		selector = raw.Spec.Selector.MatchLabels
	}

	pods, err := jobChildPods(ctx, cs, args.Namespace, raw.Spec.Selector)
	if err != nil {
		// Don't fail the whole detail on pod-fetch issues — render
		// what we have and let the user retry.
		pods = nil
	}

	var parallelism, backoffLimit int32
	if raw.Spec.Parallelism != nil {
		parallelism = *raw.Spec.Parallelism
	}
	if raw.Spec.BackoffLimit != nil {
		backoffLimit = *raw.Spec.BackoffLimit
	}

	var suspend bool
	if raw.Spec.Suspend != nil {
		suspend = *raw.Spec.Suspend
	}

	var startTime, completionTime *time.Time
	if raw.Status.StartTime != nil {
		t := raw.Status.StartTime.Time
		startTime = &t
	}
	if raw.Status.CompletionTime != nil {
		t := raw.Status.CompletionTime.Time
		completionTime = &t
	}

	return JobDetail{
		Job:            jobSummary(raw),
		Parallelism:    parallelism,
		BackoffLimit:   backoffLimit,
		Active:         raw.Status.Active,
		Succeeded:      raw.Status.Succeeded,
		Failed:         raw.Status.Failed,
		Suspend:        suspend,
		StartTime:      startTime,
		CompletionTime: completionTime,
		Containers:     containers,
		Conditions:     conds,
		Selector:       selector,
		Pods:           pods,
		Labels:         raw.Labels,
		Annotations:    raw.Annotations,
	}, nil
}

func GetJobYAML(ctx context.Context, p credentials.Provider, args GetJobArgs) (string, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return "", fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.BatchV1().Jobs(args.Namespace).Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get job %s/%s: %w", args.Namespace, args.Name, err)
	}
	raw.APIVersion = "batch/v1"
	raw.Kind = "Job"
	return formatYAML(raw)
}

func jobSummary(j *batchv1.Job) Job {
	var desired int32 = 1
	if j.Spec.Completions != nil {
		desired = *j.Spec.Completions
	}
	completions := fmt.Sprintf("%d/%d", j.Status.Succeeded, desired)

	return Job{
		Name:        j.Name,
		Namespace:   j.Namespace,
		Completions: completions,
		Status:      jobStatus(j),
		Duration:    jobDuration(j),
		CreatedAt:   j.CreationTimestamp.Time,
	}
}

// jobStatus collapses the Job's condition list into a single label.
// Order matters: Failed wins over Complete (a job can have stale
// Complete=False before becoming Failed=True), and Suspend is a spec
// flag, not a condition.
func jobStatus(j *batchv1.Job) string {
	for _, c := range j.Status.Conditions {
		if c.Status != corev1.ConditionTrue {
			continue
		}
		switch c.Type {
		case batchv1.JobFailed:
			return "Failed"
		case batchv1.JobComplete:
			return "Complete"
		case batchv1.JobSuspended:
			return "Suspended"
		}
	}
	if j.Spec.Suspend != nil && *j.Spec.Suspend {
		return "Suspended"
	}
	if j.Status.Active > 0 {
		return "Running"
	}
	if j.Status.Failed > 0 && j.Status.Succeeded == 0 {
		return "Failed"
	}
	return "Pending"
}

// jobDuration is the wall-clock from start to completion (or now if
// still running). Returns "—" if the job hasn't started yet.
func jobDuration(j *batchv1.Job) string {
	if j.Status.StartTime == nil {
		return "—"
	}
	end := time.Now()
	if j.Status.CompletionTime != nil {
		end = j.Status.CompletionTime.Time
	}
	d := end.Sub(j.Status.StartTime.Time)
	return shortDuration(d)
}

// shortDuration formats a duration as "1m45s" / "2h13m" / "3d4h".
// Mirrors the kubectl-style compact form.
func shortDuration(d time.Duration) string {
	if d < time.Second {
		return "0s"
	}
	d = d.Round(time.Second)
	days := int(d / (24 * time.Hour))
	d -= time.Duration(days) * 24 * time.Hour
	hours := int(d / time.Hour)
	d -= time.Duration(hours) * time.Hour
	mins := int(d / time.Minute)
	d -= time.Duration(mins) * time.Minute
	secs := int(d / time.Second)

	switch {
	case days > 0:
		if hours > 0 {
			return fmt.Sprintf("%dd%dh", days, hours)
		}
		return fmt.Sprintf("%dd", days)
	case hours > 0:
		if mins > 0 {
			return fmt.Sprintf("%dh%dm", hours, mins)
		}
		return fmt.Sprintf("%dh", hours)
	case mins > 0:
		if secs > 0 {
			return fmt.Sprintf("%dm%ds", mins, secs)
		}
		return fmt.Sprintf("%dm", mins)
	default:
		return fmt.Sprintf("%ds", secs)
	}
}

// jobChildPods lists pods owned by this Job, newest first, capped at
// jobChildPodLimit. Selector is the Job's own selector — controller-uid
// is set automatically by the Job controller, so MatchLabels is
// authoritative.
func jobChildPods(
	ctx context.Context,
	cs kubernetes.Interface,
	namespace string,
	selector *metav1.LabelSelector,
) ([]JobChildPod, error) {
	if selector == nil {
		return nil, nil
	}
	sel, err := metav1.LabelSelectorAsSelector(selector)
	if err != nil {
		return nil, fmt.Errorf("convert selector: %w", err)
	}

	raw, err := cs.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: sel.String(),
	})
	if err != nil {
		return nil, fmt.Errorf("list job pods: %w", err)
	}

	pods := make([]JobChildPod, 0, len(raw.Items))
	for _, pod := range raw.Items {
		pods = append(pods, JobChildPod{
			Name:      pod.Name,
			Phase:     string(pod.Status.Phase),
			Ready:     readyCount(&pod),
			Restarts:  totalRestarts(&pod),
			CreatedAt: pod.CreationTimestamp.Time,
		})
	}
	// Newest first — most relevant for triage.
	sort.Slice(pods, func(i, j int) bool {
		return pods[i].CreatedAt.After(pods[j].CreatedAt)
	})
	if len(pods) > jobChildPodLimit {
		pods = pods[:jobChildPodLimit]
	}
	return pods, nil
}

func readyCount(p *corev1.Pod) string {
	var ready int32
	for _, cs := range p.Status.ContainerStatuses {
		if cs.Ready {
			ready++
		}
	}
	return fmt.Sprintf("%d/%d", ready, len(p.Spec.Containers))
}

func totalRestarts(p *corev1.Pod) int32 {
	var n int32
	for _, cs := range p.Status.ContainerStatuses {
		n += cs.RestartCount
	}
	return n
}
