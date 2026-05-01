// Package k8s implements typed operations against an EKS-backed (or
// kubeconfig-backed) Kubernetes API server. Per GROUND_RULES, every
// operation has the signature (ctx, p Provider, args) → (result, error).
// Operations return Periscope-defined DTOs (this file), not raw
// Kubernetes API types — stable surface for v3 MCP exposure.
package k8s

import "time"

// --- Namespace ---

type Namespace struct {
	Name      string    `json:"name"`
	Phase     string    `json:"phase"`
	CreatedAt time.Time `json:"createdAt"`
}

type NamespaceList struct {
	Namespaces []Namespace `json:"namespaces"`
}

type NamespaceDetail struct {
	Namespace
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// --- Pod ---

type Pod struct {
	Name      string    `json:"name"`
	Namespace string    `json:"namespace"`
	Phase     string    `json:"phase"`
	NodeName  string    `json:"nodeName,omitempty"`
	PodIP     string    `json:"podIP,omitempty"`
	// Ready is the kubectl-style "ready/total" container count, e.g. "2/3".
	Ready     string    `json:"ready"`
	Restarts  int32     `json:"restarts"`
	CreatedAt time.Time `json:"createdAt"`
}

type PodList struct {
	Pods []Pod `json:"pods"`
}

type PodDetail struct {
	Pod
	HostIP         string            `json:"hostIP,omitempty"`
	QOSClass       string            `json:"qosClass,omitempty"`
	Conditions     []PodCondition    `json:"conditions,omitempty"`
	Containers     []ContainerStatus `json:"containers"`
	InitContainers []ContainerStatus `json:"initContainers,omitempty"`
	Labels         map[string]string `json:"labels,omitempty"`
	Annotations    map[string]string `json:"annotations,omitempty"`
}

type PodCondition struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
}

type ContainerStatus struct {
	Name         string `json:"name"`
	Image        string `json:"image"`
	State        string `json:"state"`
	Reason       string `json:"reason,omitempty"`
	Message      string `json:"message,omitempty"`
	Ready        bool   `json:"ready"`
	RestartCount int32  `json:"restartCount"`
}

// --- Deployment ---

type Deployment struct {
	Name              string    `json:"name"`
	Namespace         string    `json:"namespace"`
	Replicas          int32     `json:"replicas"`
	ReadyReplicas     int32     `json:"readyReplicas"`
	UpdatedReplicas   int32     `json:"updatedReplicas"`
	AvailableReplicas int32     `json:"availableReplicas"`
	CreatedAt         time.Time `json:"createdAt"`
}

type DeploymentList struct {
	Deployments []Deployment `json:"deployments"`
}

type DeploymentDetail struct {
	Deployment
	Strategy    string                `json:"strategy"`
	Selector    map[string]string     `json:"selector,omitempty"`
	Containers  []ContainerSpec       `json:"containers"`
	Conditions  []DeploymentCondition `json:"conditions,omitempty"`
	Labels      map[string]string     `json:"labels,omitempty"`
	Annotations map[string]string     `json:"annotations,omitempty"`
}

type DeploymentCondition struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
}

type ContainerSpec struct {
	Name  string `json:"name"`
	Image string `json:"image"`
}

// --- Service ---

type Service struct {
	Name       string        `json:"name"`
	Namespace  string        `json:"namespace"`
	Type       string        `json:"type"`
	ClusterIP  string        `json:"clusterIP,omitempty"`
	ExternalIP string        `json:"externalIP,omitempty"`
	Ports      []ServicePort `json:"ports"`
	CreatedAt  time.Time     `json:"createdAt"`
}

type ServicePort struct {
	Name     string `json:"name,omitempty"`
	Protocol string `json:"protocol"`
	Port     int32  `json:"port"`
	// TargetPort is a string because Kubernetes uses intstr.IntOrString.
	TargetPort string `json:"targetPort"`
	NodePort   int32  `json:"nodePort,omitempty"`
}

type ServiceList struct {
	Services []Service `json:"services"`
}

type ServiceDetail struct {
	Service
	Selector        map[string]string `json:"selector,omitempty"`
	SessionAffinity string            `json:"sessionAffinity,omitempty"`
	Labels          map[string]string `json:"labels,omitempty"`
	Annotations     map[string]string `json:"annotations,omitempty"`
}

// --- ConfigMap ---
//
// List view exposes only the key count — never names or values.
// Detail view exposes keys and values; ConfigMap data is config, not
// secret. Secrets (when they land) follow stricter redaction rules.

type ConfigMap struct {
	Name      string    `json:"name"`
	Namespace string    `json:"namespace"`
	KeyCount  int       `json:"keyCount"`
	CreatedAt time.Time `json:"createdAt"`
}

type ConfigMapList struct {
	ConfigMaps []ConfigMap `json:"configMaps"`
}

type ConfigMapDetail struct {
	ConfigMap
	Data           map[string]string `json:"data,omitempty"`
	BinaryDataKeys []string          `json:"binaryDataKeys,omitempty"`
	Labels         map[string]string `json:"labels,omitempty"`
	Annotations    map[string]string `json:"annotations,omitempty"`
}

// --- Events (shared across resources) ---

type Event struct {
	// Type is "Normal" or "Warning".
	Type    string    `json:"type"`
	Reason  string    `json:"reason"`
	Message string    `json:"message"`
	Count   int32     `json:"count"`
	First   time.Time `json:"first"`
	Last    time.Time `json:"last"`
	Source  string    `json:"source"`
}

type EventList struct {
	Events []Event `json:"events"`
}

// --- StatefulSet ---

type StatefulSet struct {
	Name            string    `json:"name"`
	Namespace       string    `json:"namespace"`
	Replicas        int32     `json:"replicas"`
	ReadyReplicas   int32     `json:"readyReplicas"`
	UpdatedReplicas int32     `json:"updatedReplicas"`
	CurrentReplicas int32     `json:"currentReplicas"`
	CreatedAt       time.Time `json:"createdAt"`
}

type StatefulSetList struct {
	StatefulSets []StatefulSet `json:"statefulSets"`
}

type StatefulSetDetail struct {
	StatefulSet
	ServiceName    string                 `json:"serviceName,omitempty"`
	UpdateStrategy string                 `json:"updateStrategy"`
	Selector       map[string]string      `json:"selector,omitempty"`
	Containers     []ContainerSpec        `json:"containers"`
	Conditions     []DeploymentCondition  `json:"conditions,omitempty"`
	Labels         map[string]string      `json:"labels,omitempty"`
	Annotations    map[string]string      `json:"annotations,omitempty"`
}

// --- DaemonSet ---

type DaemonSet struct {
	Name                   string    `json:"name"`
	Namespace              string    `json:"namespace"`
	DesiredNumberScheduled int32     `json:"desiredNumberScheduled"`
	NumberReady            int32     `json:"numberReady"`
	UpdatedNumberScheduled int32     `json:"updatedNumberScheduled"`
	NumberAvailable        int32     `json:"numberAvailable"`
	NumberMisscheduled     int32     `json:"numberMisscheduled"`
	CreatedAt              time.Time `json:"createdAt"`
}

type DaemonSetList struct {
	DaemonSets []DaemonSet `json:"daemonSets"`
}

type DaemonSetDetail struct {
	DaemonSet
	UpdateStrategy string                `json:"updateStrategy"`
	Selector       map[string]string     `json:"selector,omitempty"`
	NodeSelector   map[string]string     `json:"nodeSelector,omitempty"`
	Containers     []ContainerSpec       `json:"containers"`
	Conditions     []DeploymentCondition `json:"conditions,omitempty"`
	Labels         map[string]string     `json:"labels,omitempty"`
	Annotations    map[string]string     `json:"annotations,omitempty"`
}

// --- Secret ---
//
// Per GROUND_RULES + the v1 reveal-with-audit decision: SecretDetail does
// NOT contain a `data` field. Anywhere. Adding one would require editing
// this type — making it a deliberate, reviewable change rather than a
// careless field addition. Reveal happens through a separate per-key
// endpoint that audit-logs each access.

type Secret struct {
	Name      string    `json:"name"`
	Namespace string    `json:"namespace"`
	Type      string    `json:"type"`
	KeyCount  int       `json:"keyCount"`
	CreatedAt time.Time `json:"createdAt"`
}

type SecretList struct {
	Secrets []Secret `json:"secrets"`
}

type SecretKey struct {
	Name string `json:"name"`
	Size int    `json:"size"` // bytes — metadata only, not the value
}

type SecretDetail struct {
	Secret
	Keys        []SecretKey       `json:"keys"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
	Immutable   bool              `json:"immutable,omitempty"`
}

// --- Ingress ---

type Ingress struct {
	Name      string    `json:"name"`
	Namespace string    `json:"namespace"`
	Class     string    `json:"class,omitempty"`
	Hosts     []string  `json:"hosts"`
	Address   string    `json:"address,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

type IngressList struct {
	Ingresses []Ingress `json:"ingresses"`
}

type IngressBackend struct {
	ServiceName string `json:"serviceName"`
	ServicePort string `json:"servicePort"` // intstr stringified
}

type IngressPath struct {
	Path     string         `json:"path"`
	PathType string         `json:"pathType"`
	Backend  IngressBackend `json:"backend"`
}

type IngressRule struct {
	Host  string        `json:"host"` // empty for catch-all
	Paths []IngressPath `json:"paths"`
}

type IngressTLS struct {
	Hosts      []string `json:"hosts"`
	SecretName string   `json:"secretName,omitempty"`
}

type IngressDetail struct {
	Ingress
	Rules       []IngressRule     `json:"rules"`
	TLS         []IngressTLS      `json:"tls,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// --- Job ---
//
// Completions is the kubectl-style "succeeded/desired" string ("1/1",
// "2/3"). Status collapses the controller's condition list into a single
// label: Complete | Failed | Running | Suspended. Duration is the wall
// clock from start to completion (or now, if running) — pre-rendered by
// the backend so the frontend doesn't need a humanizer.

type Job struct {
	Name        string    `json:"name"`
	Namespace   string    `json:"namespace"`
	Completions string    `json:"completions"`
	Status      string    `json:"status"`
	Duration    string    `json:"duration,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
}

type JobList struct {
	Jobs []Job `json:"jobs"`
}

type JobCondition struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
}

// JobChildPod is the inline-rendered pod row on JobDetail. Compact
// shape — full pod info is one click away on the Pods page.
type JobChildPod struct {
	Name      string    `json:"name"`
	Phase     string    `json:"phase"`
	Ready     string    `json:"ready"`
	Restarts  int32     `json:"restarts"`
	CreatedAt time.Time `json:"createdAt"`
}

type JobDetail struct {
	Job
	Parallelism    int32             `json:"parallelism"`
	BackoffLimit   int32             `json:"backoffLimit"`
	Active         int32             `json:"active"`
	Succeeded      int32             `json:"succeeded"`
	Failed         int32             `json:"failed"`
	Suspend        bool              `json:"suspend"`
	StartTime      *time.Time        `json:"startTime,omitempty"`
	CompletionTime *time.Time        `json:"completionTime,omitempty"`
	Containers     []ContainerSpec   `json:"containers"`
	Conditions     []JobCondition    `json:"conditions,omitempty"`
	Selector       map[string]string `json:"selector,omitempty"`
	Pods           []JobChildPod     `json:"pods"`
	Labels         map[string]string `json:"labels,omitempty"`
	Annotations    map[string]string `json:"annotations,omitempty"`
}

// --- CronJob ---

type CronJob struct {
	Name             string     `json:"name"`
	Namespace        string     `json:"namespace"`
	Schedule         string     `json:"schedule"`
	Suspend          bool       `json:"suspend"`
	Active           int32      `json:"active"`
	LastScheduleTime *time.Time `json:"lastScheduleTime,omitempty"`
	CreatedAt        time.Time  `json:"createdAt"`
}

type CronJobList struct {
	CronJobs []CronJob `json:"cronJobs"`
}

// CronJobChildJob is the inline-rendered job row on CronJobDetail —
// last N jobs spawned by this CronJob, newest first.
type CronJobChildJob struct {
	Name           string     `json:"name"`
	Status         string     `json:"status"`
	Completions    string     `json:"completions"`
	StartTime      *time.Time `json:"startTime,omitempty"`
	CompletionTime *time.Time `json:"completionTime,omitempty"`
	Duration       string     `json:"duration,omitempty"`
}

type CronJobDetail struct {
	CronJob
	ConcurrencyPolicy          string            `json:"concurrencyPolicy"`
	StartingDeadlineSeconds    *int64            `json:"startingDeadlineSeconds,omitempty"`
	SuccessfulJobsHistoryLimit int32             `json:"successfulJobsHistoryLimit"`
	FailedJobsHistoryLimit     int32             `json:"failedJobsHistoryLimit"`
	LastSuccessfulTime         *time.Time        `json:"lastSuccessfulTime,omitempty"`
	Containers                 []ContainerSpec   `json:"containers"`
	Jobs                       []CronJobChildJob `json:"jobs"`
	Labels                     map[string]string `json:"labels,omitempty"`
	Annotations                map[string]string `json:"annotations,omitempty"`
}
