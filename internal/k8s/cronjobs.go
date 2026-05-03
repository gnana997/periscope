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

// --- Phase 5: trigger-now -------------------------------------------

// TriggerCronJobArgs carries the identity of the source CronJob plus
// the impersonated cluster context.
type TriggerCronJobArgs struct {
	Cluster   clusters.Cluster
	Namespace string
	Name      string
}

// TriggerCronJobResult reports the new Job's name, so the SPA can
// pivot to its detail view if needed.
type TriggerCronJobResult struct {
	JobName string `json:"jobName"`
}

// TriggerCronJob clones a CronJob's spec.jobTemplate into a fresh Job,
// matching the semantics of `kubectl create job <name> --from=cronjob/<src>`:
//   - copies the JobTemplate's spec, labels, annotations
//   - inherits ownerReferences pointing back to the CronJob (so the
//     Job is garbage-collected with the CronJob)
//   - generates a unique name with a UNIX-second suffix to avoid
//     collisions when triggered repeatedly within a minute.
//
// The CronJob itself isn't mutated. The new Job runs through the
// normal Job controller path — same as one the schedule would have
// produced.
func TriggerCronJob(ctx context.Context, p credentials.Provider, args TriggerCronJobArgs) (TriggerCronJobResult, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return TriggerCronJobResult{}, fmt.Errorf("build clientset: %w", err)
	}
	cj, err := cs.BatchV1().CronJobs(args.Namespace).Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		return TriggerCronJobResult{}, fmt.Errorf("get cronjob: %w", err)
	}
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			// kubectl appends a 5-char hash; UNIX seconds is plenty
			// human-readable and unique within a CronJob's reasonable
			// trigger frequency. Truncated to fit the 63-char DNS-label
			// limit (CronJob name itself is bounded at 52 chars by
			// validation precisely so triggered Jobs fit).
			Name:        truncateName(args.Name+"-manual-"+fmt.Sprintf("%d", time.Now().Unix()), 63),
			Namespace:   args.Namespace,
			Labels:      cj.Spec.JobTemplate.Labels,
			Annotations: cj.Spec.JobTemplate.Annotations,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "batch/v1",
					Kind:       "CronJob",
					Name:       cj.Name,
					UID:        cj.UID,
					// Triggered jobs are first-class — not "controlled"
					// by the CronJob (the schedule didn't fire them) but
					// still owned for GC.
					Controller:         ptrTo(false),
					BlockOwnerDeletion: ptrTo(true),
				},
			},
		},
		Spec: cj.Spec.JobTemplate.Spec,
	}
	created, err := cs.BatchV1().Jobs(args.Namespace).Create(ctx, job, metav1.CreateOptions{
		FieldManager: "periscope-spa",
	})
	if err != nil {
		return TriggerCronJobResult{}, fmt.Errorf("create job: %w", err)
	}
	return TriggerCronJobResult{JobName: created.Name}, nil
}

// truncateName clips a generated name at the K8s DNS-label limit.
func truncateName(name string, max int) string {
	if len(name) <= max {
		return name
	}
	return name[:max]
}

// ptrTo is a small helper for taking a pointer to a literal — needed
// for OwnerReference.Controller / BlockOwnerDeletion fields.
func ptrTo[T any](v T) *T { return &v }

// Suppress unused-clientset warning when newClientFn is the only
// kubernetes-package dependency: we explicitly need it imported.
var _ = func(_ kubernetes.Interface) {}

// types-only reference so the new file's imports don't drift away
// from the package's existing set.
var _ types.PatchType = ""
