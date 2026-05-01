package k8s

import (
	"context"
	"fmt"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

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
	out := make([]Job, 0, len(raw.Items))
	for _, j := range raw.Items {
		out = append(out, jobSummary(&j))
	}
	return JobList{Jobs: out}, nil
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

	pods, err := childPodsBySelector(ctx, cs, args.Namespace, raw.Spec.Selector)
	if err != nil {
		return JobDetail{}, fmt.Errorf("list job pods: %w", err)
	}

	var parallelism, backoffLimit int32
	if raw.Spec.Parallelism != nil {
		parallelism = *raw.Spec.Parallelism
	}
	if raw.Spec.BackoffLimit != nil {
		backoffLimit = *raw.Spec.BackoffLimit
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
		Suspend:        raw.Spec.Suspend != nil && *raw.Spec.Suspend,
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
		return "", fmt.Errorf("get job yaml %s/%s: %w", args.Namespace, args.Name, err)
	}
	raw.APIVersion = "batch/v1"
	raw.Kind = "Job"
	return formatYAML(raw)
}

func jobSummary(j *batchv1.Job) Job {
	var completions string
	if j.Spec.Completions != nil {
		completions = fmt.Sprintf("%d/%d", j.Status.Succeeded, *j.Spec.Completions)
	} else {
		completions = fmt.Sprintf("%d/1", j.Status.Succeeded)
	}
	return Job{
		Name:        j.Name,
		Namespace:   j.Namespace,
		Status:      jobStatus(j),
		Completions: completions,
		Duration:    jobDuration(j),
		CreatedAt: j.CreationTimestamp.Time,
	}
}

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
	if j.Status.Failed > 0 {
		return "Failed"
	}
	if j.Status.Active > 0 {
		return "Running"
	}
	return "Pending"
}

func shortDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60
	seconds := int(d.Seconds()) % 60
	if days > 0 {
		if hours > 0 {
			return fmt.Sprintf("%dd%dh", days, hours)
		}
		return fmt.Sprintf("%dd", days)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh%dm", hours, minutes)
	}
	if minutes > 0 {
		return fmt.Sprintf("%dm%ds", minutes, seconds)
	}
	return fmt.Sprintf("%ds", seconds)
}

func jobDuration(j *batchv1.Job) string {
	if j.Status.StartTime == nil {
		return "—"
	}
	end := time.Now()
	if j.Status.CompletionTime != nil {
		end = j.Status.CompletionTime.Time
	}
	return shortDuration(end.Sub(j.Status.StartTime.Time))
}
