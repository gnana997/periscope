package k8s

import (
	"context"
	"fmt"
	"sort"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

// cronJobChildJobLimit is the cap for inline-rendered child jobs on
// CronJobDetail. Matches the typical successfulJobsHistoryLimit (3)
// plus failedJobsHistoryLimit (1) headroom — but we accept up to 10
// because some clusters bump those limits.
const cronJobChildJobLimit = 10

type ListCronJobsArgs struct {
	Cluster   clusters.Cluster
	Namespace string
}

func ListCronJobs(ctx context.Context, p credentials.Provider, args ListCronJobsArgs) (CronJobList, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return CronJobList{}, fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.BatchV1().CronJobs(args.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return CronJobList{}, fmt.Errorf("list cronjobs: %w", err)
	}

	out := CronJobList{CronJobs: make([]CronJob, 0, len(raw.Items))}
	for _, c := range raw.Items {
		out.CronJobs = append(out.CronJobs, cronJobSummary(&c))
	}
	return out, nil
}

type GetCronJobArgs struct {
	Cluster   clusters.Cluster
	Namespace string
	Name      string
}

func GetCronJob(ctx context.Context, p credentials.Provider, args GetCronJobArgs) (CronJobDetail, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return CronJobDetail{}, fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.BatchV1().CronJobs(args.Namespace).Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		return CronJobDetail{}, fmt.Errorf("get cronjob %s/%s: %w", args.Namespace, args.Name, err)
	}

	containers := make([]ContainerSpec, 0, len(raw.Spec.JobTemplate.Spec.Template.Spec.Containers))
	for _, c := range raw.Spec.JobTemplate.Spec.Template.Spec.Containers {
		containers = append(containers, ContainerSpec{Name: c.Name, Image: c.Image})
	}

	var lastSuccessful *time.Time
	if raw.Status.LastSuccessfulTime != nil {
		t := raw.Status.LastSuccessfulTime.Time
		lastSuccessful = &t
	}

	var successHistory, failHistory int32
	if raw.Spec.SuccessfulJobsHistoryLimit != nil {
		successHistory = *raw.Spec.SuccessfulJobsHistoryLimit
	}
	if raw.Spec.FailedJobsHistoryLimit != nil {
		failHistory = *raw.Spec.FailedJobsHistoryLimit
	}

	jobs, err := cronJobChildren(ctx, cs, args.Namespace, raw.UID)
	if err != nil {
		// Same posture as JobDetail.Pods — surface the rest of the
		// detail and let the user retry.
		jobs = nil
	}

	return CronJobDetail{
		CronJob:                    cronJobSummary(raw),
		ConcurrencyPolicy:          string(raw.Spec.ConcurrencyPolicy),
		StartingDeadlineSeconds:    raw.Spec.StartingDeadlineSeconds,
		SuccessfulJobsHistoryLimit: successHistory,
		FailedJobsHistoryLimit:     failHistory,
		LastSuccessfulTime:         lastSuccessful,
		Containers:                 containers,
		Jobs:                       jobs,
		Labels:                     raw.Labels,
		Annotations:                raw.Annotations,
	}, nil
}

func GetCronJobYAML(ctx context.Context, p credentials.Provider, args GetCronJobArgs) (string, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return "", fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.BatchV1().CronJobs(args.Namespace).Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get cronjob %s/%s: %w", args.Namespace, args.Name, err)
	}
	raw.APIVersion = "batch/v1"
	raw.Kind = "CronJob"
	return formatYAML(raw)
}

func cronJobSummary(c *batchv1.CronJob) CronJob {
	var suspend bool
	if c.Spec.Suspend != nil {
		suspend = *c.Spec.Suspend
	}
	var lastSchedule *time.Time
	if c.Status.LastScheduleTime != nil {
		t := c.Status.LastScheduleTime.Time
		lastSchedule = &t
	}
	return CronJob{
		Name:             c.Name,
		Namespace:        c.Namespace,
		Schedule:         c.Spec.Schedule,
		Suspend:          suspend,
		Active:           int32(len(c.Status.Active)),
		LastScheduleTime: lastSchedule,
		CreatedAt:        c.CreationTimestamp.Time,
	}
}

// cronJobChildren returns the most-recent child Jobs of a CronJob,
// matched via owner reference (the CronJob's UID). We list all jobs in
// the namespace and filter client-side — there's no field selector for
// ownerReferences.
func cronJobChildren(
	ctx context.Context,
	cs kubernetes.Interface,
	namespace string,
	cronJobUID types.UID,
) ([]CronJobChildJob, error) {
	raw, err := cs.BatchV1().Jobs(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list child jobs: %w", err)
	}

	matched := make([]batchv1.Job, 0)
	for _, j := range raw.Items {
		for _, ref := range j.OwnerReferences {
			if ref.UID == cronJobUID {
				matched = append(matched, j)
				break
			}
		}
	}

	// Newest first by start time, falling back to creation.
	sort.Slice(matched, func(i, k int) bool {
		ti := childSortTime(&matched[i])
		tk := childSortTime(&matched[k])
		return ti.After(tk)
	})

	if len(matched) > cronJobChildJobLimit {
		matched = matched[:cronJobChildJobLimit]
	}

	out := make([]CronJobChildJob, 0, len(matched))
	for _, j := range matched {
		var completions string
		var desired int32 = 1
		if j.Spec.Completions != nil {
			desired = *j.Spec.Completions
		}
		completions = fmt.Sprintf("%d/%d", j.Status.Succeeded, desired)

		var startTime, completionTime *time.Time
		if j.Status.StartTime != nil {
			t := j.Status.StartTime.Time
			startTime = &t
		}
		if j.Status.CompletionTime != nil {
			t := j.Status.CompletionTime.Time
			completionTime = &t
		}

		out = append(out, CronJobChildJob{
			Name:           j.Name,
			Status:         jobStatus(&j),
			Completions:    completions,
			StartTime:      startTime,
			CompletionTime: completionTime,
			Duration:       jobDuration(&j),
		})
	}
	return out, nil
}

func childSortTime(j *batchv1.Job) time.Time {
	if j.Status.StartTime != nil {
		return j.Status.StartTime.Time
	}
	return j.CreationTimestamp.Time
}
