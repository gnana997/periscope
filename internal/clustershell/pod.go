package clustershell

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// Constants for fields a few helpers share. Exposed so tests and the
// session lifecycle can reference the same values without re-deriving.
const (
	// ServiceAccountPrefix + tier yields the per-tier SA name the
	// Helm chart pre-creates in the cluster-shell namespace. Pod
	// .Spec.ServiceAccountName is set to ServiceAccountPrefix +
	// tier.
	ServiceAccountPrefix = "periscope-shell-"

	// KubeconfigMountPath is where BuildPodSpec mounts the per-
	// session kubeconfig Secret inside the shell container. The
	// periscope-shell entrypoint sets KUBECONFIG to <this>/kubeconfig
	// before exec'ing bash.
	KubeconfigMountPath = "/etc/periscope"

	// AuditFilePath is the path the periscope-audit-kubectl wrapper
	// appends JSON lines to. session.Close exec-cats this path on
	// teardown to read back per-command attribution.
	AuditFilePath = "/tmp/periscope-shell/audit.jsonl"

	// ContainerName is the container's name inside the shell pod.
	// Periscope's ExecPod call uses this when attaching.
	ContainerName = "shell"
)

// PodName returns the deterministic Pod name for a session. Derived
// from the session UUID rather than randomly generated so reconcile
// loops can find an in-flight pod by name even after a Periscope
// restart mid-session (cleanup path).
func PodName(sessionID string) string {
	return "periscope-shell-" + sessionID
}

// ServiceAccountName returns the per-tier SA name BuildPodSpec
// stamps on .Spec.ServiceAccountName. The Helm chart pre-creates
// one SA per allowed tier in the cluster-shell namespace.
func ServiceAccountName(tier string) string {
	return ServiceAccountPrefix + tier
}

// ActorHash returns a label-safe 12-hex-char prefix of the SHA-256
// of the actor sub. K8s label values can't contain '@' or '/' so a
// raw email or OIDC sub fails validation; the hash gives us
// stable-per-actor selector-friendly labels while the full sub goes
// in an annotation.
func ActorHash(actorSub string) string {
	sum := sha256.Sum256([]byte(actorSub))
	return hex.EncodeToString(sum[:])[:12]
}

// PodSpecParams is the typed input for BuildPodSpec.
type PodSpecParams struct {
	SessionID string
	Namespace string
	Tier      string
	Mode      Mode
	Actor     string // full OIDC sub — goes into annotation
	Image     string
	PullPolicy corev1.PullPolicy
}

// BuildPodSpec returns the corev1.Pod that Periscope main creates
// per session. Resource limits and security context are intentionally
// hardcoded (not configurable) — operators tuning these breaks the
// security envelope.
func BuildPodSpec(p PodSpecParams) *corev1.Pod {
	actorHash := ActorHash(p.Actor)
	nonRoot := true
	allowPrivEsc := false
	uid := int64(1000)
	gid := int64(1000)
	gracePeriod := int64(5)
	automount := false

	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      PodName(p.SessionID),
			Namespace: p.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":       "periscope-shell",
				"app.kubernetes.io/managed-by": "periscope",
				"periscope.io/session-id":      p.SessionID,
				"periscope.io/tier":            p.Tier,
				"periscope.io/actor-hash":      actorHash,
			},
			Annotations: map[string]string{
				// Full actor sub goes here — annotations have no
				// length / charset constraints. Reviewers cross-
				// referencing this pod to a Periscope audit row look
				// at this annotation, not the label.
				"periscope.io/actor": p.Actor,
				"periscope.io/mode":  string(p.Mode),
			},
		},
		Spec: corev1.PodSpec{
			RestartPolicy:                 corev1.RestartPolicyNever,
			ServiceAccountName:            ServiceAccountName(p.Tier),
			AutomountServiceAccountToken:  &automount,
			TerminationGracePeriodSeconds: &gracePeriod,
			SecurityContext: &corev1.PodSecurityContext{
				RunAsNonRoot: &nonRoot,
				RunAsUser:    &uid,
				RunAsGroup:   &gid,
				FSGroup:      &gid,
			},
			Containers: []corev1.Container{{
				Name:            ContainerName,
				Image:           p.Image,
				ImagePullPolicy: p.PullPolicy,
				Stdin:           true,
				StdinOnce:       true,
				TTY:             true,
				Env: []corev1.EnvVar{
					{Name: "PERISCOPE_SHELL_SESSION_ID", Value: p.SessionID},
					{Name: "PERISCOPE_SHELL_MODE", Value: string(p.Mode)},
					{Name: "PERISCOPE_SHELL_AUDIT_FILE", Value: AuditFilePath},
					// KUBECONFIG is set on the container env (not via
					// the entrypoint's syscall.Exec) so the kubectl-
					// exec'd bash spawned by Periscope's WS handler
					// inherits it as a child of the container runtime
					// rather than depending on PID-1 having forwarded it.
					{Name: "KUBECONFIG", Value: KubeconfigMountPath + "/kubeconfig"},
				},
				VolumeMounts: []corev1.VolumeMount{{
					Name:      "kubeconfig",
					MountPath: KubeconfigMountPath,
					ReadOnly:  true,
				}},
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("100m"),
						corev1.ResourceMemory: resource.MustParse("128Mi"),
					},
					Limits: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("500m"),
						corev1.ResourceMemory: resource.MustParse("512Mi"),
					},
				},
				SecurityContext: &corev1.SecurityContext{
					AllowPrivilegeEscalation: &allowPrivEsc,
					Capabilities: &corev1.Capabilities{
						Drop: []corev1.Capability{"ALL"},
					},
				},
			}},
			Volumes: []corev1.Volume{{
				Name: "kubeconfig",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: SecretName(p.SessionID),
						Items: []corev1.KeyToPath{{
							Key:  SecretKubeconfigKey,
							Path: "kubeconfig",
						}},
					},
				},
			}},
		},
	}
}

// pullPolicyFromString maps the config string ("Always" | "IfNotPresent" |
// "Never") onto the typed corev1.PullPolicy. Unknown values fall back
// to IfNotPresent to mirror the chart default.
func pullPolicyFromString(s string) corev1.PullPolicy {
	switch corev1.PullPolicy(s) {
	case corev1.PullAlways, corev1.PullIfNotPresent, corev1.PullNever:
		return corev1.PullPolicy(s)
	}
	return corev1.PullIfNotPresent
}

// WaitForPodReady polls the named pod until it's Running with a
// Ready container, ctx expires, or the pod terminates prematurely.
//
// The 500ms cadence trades a tiny bit of apiserver chatter for
// crisp time-to-first-prompt — informer-backed waits would be more
// efficient but inflate the per-session memory footprint and add a
// startup-ordering dependency on a shared cache that v1.2 doesn't
// otherwise need.
func WaitForPodReady(ctx context.Context, cs kubernetes.Interface, namespace, name string) error {
	const interval = 500 * time.Millisecond
	for {
		pod, err := cs.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				// Race: the create call returned but the Get raced
				// ahead of the watch propagating to this apiserver
				// node. Retry — short interval makes this cheap.
				goto wait
			}
			return fmt.Errorf("get pod %s/%s: %w", namespace, name, err)
		}
		switch pod.Status.Phase {
		case corev1.PodRunning:
			for _, cs := range pod.Status.ContainerStatuses {
				if cs.Name == ContainerName && cs.Ready {
					return nil
				}
			}
		case corev1.PodFailed, corev1.PodSucceeded:
			return fmt.Errorf("pod %s/%s entered phase %s before becoming Ready", namespace, name, pod.Status.Phase)
		}
	wait:
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for pod %s/%s to become Ready: %w", namespace, name, ctx.Err())
		case <-time.After(interval):
			// Loop and re-poll.
		}
	}
}
