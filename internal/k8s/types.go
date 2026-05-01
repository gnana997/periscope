// Package k8s implements typed operations against an EKS-backed (or
// kubeconfig-backed) Kubernetes API server. Per GROUND_RULES, every
// operation has the signature (ctx, p Provider, args) → (result, error).
// Operations return Periscope-defined DTOs (this file), not raw
// Kubernetes API types — stable surface for v3 MCP exposure.
package k8s

import "time"

// --- Node ---

type Node struct {
	Name           string    `json:"name"`
	Status         string    `json:"status"` // "Ready" | "NotReady" | "Unknown"
	Roles          []string  `json:"roles"`
	KubeletVersion string    `json:"kubeletVersion"`
	InternalIP     string    `json:"internalIP"`
	CPUCapacity    string    `json:"cpuCapacity"`
	MemoryCapacity string    `json:"memoryCapacity"`
	CreatedAt      time.Time `json:"createdAt"`
}

type NodeList struct {
	Nodes []Node `json:"nodes"`
}

type NodeCondition struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
}

type NodeTaint struct {
	Key    string `json:"key"`
	Value  string `json:"value,omitempty"`
	Effect string `json:"effect"`
}

type NodeInfo struct {
	OSImage          string `json:"osImage"`
	KernelVersion    string `json:"kernelVersion"`
	ContainerRuntime string `json:"containerRuntime"`
	KubeletVersion   string `json:"kubeletVersion"`
	KubeProxyVersion string `json:"kubeProxyVersion"`
}

type NodeDetail struct {
	Node
	Conditions        []NodeCondition   `json:"conditions"`
	Taints            []NodeTaint       `json:"taints,omitempty"`
	Labels            map[string]string `json:"labels,omitempty"`
	Annotations       map[string]string `json:"annotations,omitempty"`
	NodeInfo          NodeInfo          `json:"nodeInfo"`
	CPUAllocatable    string            `json:"cpuAllocatable"`
	MemoryAllocatable string            `json:"memoryAllocatable"`
}

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
	Name          string `json:"name"`
	Image         string `json:"image"`
	State         string `json:"state"`
	Reason        string `json:"reason,omitempty"`
	Message       string `json:"message,omitempty"`
	Ready         bool   `json:"ready"`
	RestartCount  int32  `json:"restartCount"`
	CPURequest    string `json:"cpuRequest,omitempty"`
	CPULimit      string `json:"cpuLimit,omitempty"`
	MemoryRequest string `json:"memoryRequest,omitempty"`
	MemoryLimit   string `json:"memoryLimit,omitempty"`
}

// --- Metrics ---

type NodeMetrics struct {
	Available     bool    `json:"available"`
	CPUPercent    float64 `json:"cpuPercent,omitempty"`
	MemoryPercent float64 `json:"memoryPercent,omitempty"`
	CPUUsage      string  `json:"cpuUsage,omitempty"`
	MemoryUsage   string  `json:"memoryUsage,omitempty"`
}

type ContainerMetrics struct {
	Name            string  `json:"name"`
	CPUUsage        string  `json:"cpuUsage,omitempty"`
	MemoryUsage     string  `json:"memoryUsage,omitempty"`
	CPULimitPercent float64 `json:"cpuLimitPercent"`  // usage/limit*100; -1 = no limit set
	MemLimitPercent float64 `json:"memLimitPercent"`  // usage/limit*100; -1 = no limit set
}

type PodMetrics struct {
	Available  bool               `json:"available"`
	Containers []ContainerMetrics `json:"containers,omitempty"`
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
	Pods           []JobChildPod         `json:"pods,omitempty"`
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
	Pods            []JobChildPod     `json:"pods,omitempty"`
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
	Pods           []JobChildPod         `json:"pods,omitempty"`
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
	Pods           []JobChildPod         `json:"pods,omitempty"`
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

// --- PersistentVolumeClaim ---

type PVC struct {
	Name         string    `json:"name"`
	Namespace    string    `json:"namespace"`
	Status       string    `json:"status"`
	StorageClass string    `json:"storageClass,omitempty"`
	Capacity     string    `json:"capacity,omitempty"`
	AccessModes  []string  `json:"accessModes"`
	CreatedAt    time.Time `json:"createdAt"`
}

type PVCList struct {
	PVCs []PVC `json:"pvcs"`
}

type PVCCondition struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
}

type PVCDetail struct {
	PVC
	VolumeName  string            `json:"volumeName,omitempty"`
	Conditions  []PVCCondition    `json:"conditions,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// --- PersistentVolume ---

type PVClaimRef struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

type PV struct {
	Name          string    `json:"name"`
	Status        string    `json:"status"`
	StorageClass  string    `json:"storageClass,omitempty"`
	Capacity      string    `json:"capacity,omitempty"`
	AccessModes   []string  `json:"accessModes"`
	ReclaimPolicy string    `json:"reclaimPolicy,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
}

type PVList struct {
	PVs []PV `json:"pvs"`
}

type PVDetail struct {
	PV
	ClaimRef    *PVClaimRef       `json:"claimRef,omitempty"`
	VolumeMode  string            `json:"volumeMode,omitempty"`
	Source      string            `json:"source,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// --- StorageClass ---

type StorageClass struct {
	Name                 string    `json:"name"`
	Provisioner          string    `json:"provisioner"`
	ReclaimPolicy        string    `json:"reclaimPolicy,omitempty"`
	VolumeBindingMode    string    `json:"volumeBindingMode,omitempty"`
	AllowVolumeExpansion bool      `json:"allowVolumeExpansion"`
	CreatedAt            time.Time `json:"createdAt"`
}

type StorageClassList struct {
	StorageClasses []StorageClass `json:"storageClasses"`
}

type StorageClassDetail struct {
	StorageClass
	Parameters   map[string]string `json:"parameters,omitempty"`
	MountOptions []string          `json:"mountOptions,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
	Annotations  map[string]string `json:"annotations,omitempty"`
}

// --- ClusterEvent ---
//
// Used by the cluster-wide events list page. Distinct from the per-object
// Event type (used in detail-pane EventsView tabs) — this one carries
// namespace and involvedObject context so the frontend can render a
// meaningful row and cross-link to the affected resource.

type ClusterEvent struct {
	Namespace string    `json:"namespace"`
	Kind      string    `json:"kind"`    // Pod, Deployment, Job, etc.
	Name      string    `json:"name"`    // object name
	Type      string    `json:"type"`    // Normal | Warning
	Reason    string    `json:"reason"`
	Message   string    `json:"message"`
	Count     int32     `json:"count"`
	First     time.Time `json:"first"`
	Last      time.Time `json:"last"`
	Source    string    `json:"source"`
}

type ClusterEventList struct {
	Events []ClusterEvent `json:"events"`
}

// --- RBAC ---

type PolicyRule struct {
	Verbs           []string `json:"verbs"`
	APIGroups       []string `json:"apiGroups,omitempty"`
	Resources       []string `json:"resources,omitempty"`
	ResourceNames   []string `json:"resourceNames,omitempty"`
	NonResourceURLs []string `json:"nonResourceURLs,omitempty"`
}

type RoleRef struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

type RBACSubject struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
}

// Role (namespace-scoped)

type Role struct {
	Name      string    `json:"name"`
	Namespace string    `json:"namespace"`
	RuleCount int       `json:"ruleCount"`
	CreatedAt time.Time `json:"createdAt"`
}

type RoleList struct {
	Roles []Role `json:"roles"`
}

type RoleDetail struct {
	Role
	Rules       []PolicyRule      `json:"rules"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// ClusterRole (cluster-scoped)

type ClusterRole struct {
	Name      string    `json:"name"`
	RuleCount int       `json:"ruleCount"`
	CreatedAt time.Time `json:"createdAt"`
}

type ClusterRoleList struct {
	ClusterRoles []ClusterRole `json:"clusterRoles"`
}

type ClusterRoleDetail struct {
	ClusterRole
	Rules             []PolicyRule      `json:"rules"`
	AggregationLabels []string          `json:"aggregationLabels,omitempty"`
	Labels            map[string]string `json:"labels,omitempty"`
	Annotations       map[string]string `json:"annotations,omitempty"`
}

// RoleBinding (namespace-scoped)

type RoleBinding struct {
	Name         string    `json:"name"`
	Namespace    string    `json:"namespace"`
	RoleRef      string    `json:"roleRef"`
	SubjectCount int       `json:"subjectCount"`
	CreatedAt    time.Time `json:"createdAt"`
}

type RoleBindingList struct {
	RoleBindings []RoleBinding `json:"roleBindings"`
}

type RoleBindingDetail struct {
	Name        string            `json:"name"`
	Namespace   string            `json:"namespace"`
	CreatedAt   time.Time         `json:"createdAt"`
	RoleRef     RoleRef           `json:"roleRef"`
	Subjects    []RBACSubject     `json:"subjects"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// ClusterRoleBinding (cluster-scoped)

type ClusterRoleBinding struct {
	Name         string    `json:"name"`
	RoleRef      string    `json:"roleRef"`
	SubjectCount int       `json:"subjectCount"`
	CreatedAt    time.Time `json:"createdAt"`
}

type ClusterRoleBindingList struct {
	ClusterRoleBindings []ClusterRoleBinding `json:"clusterRoleBindings"`
}

type ClusterRoleBindingDetail struct {
	Name        string            `json:"name"`
	CreatedAt   time.Time         `json:"createdAt"`
	RoleRef     RoleRef           `json:"roleRef"`
	Subjects    []RBACSubject     `json:"subjects"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// ServiceAccount (namespace-scoped)

type ServiceAccount struct {
	Name      string    `json:"name"`
	Namespace string    `json:"namespace"`
	Secrets   int       `json:"secrets"`
	CreatedAt time.Time `json:"createdAt"`
}

type ServiceAccountList struct {
	ServiceAccounts []ServiceAccount `json:"serviceAccounts"`
}

type ServiceAccountDetail struct {
	ServiceAccount
	SecretNames []string          `json:"secretNames,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// --- Cluster Summary (Overview page) ---

type ClusterSummary struct {
	KubernetesVersion string  `json:"kubernetesVersion"`
	Provider          string  `json:"provider"` // "EKS" | "Kubeconfig"
	NodeCount         int     `json:"nodeCount"`
	NodeReadyCount    int     `json:"nodeReadyCount"`
	PodCount          int     `json:"podCount"`
	NamespaceCount    int     `json:"namespaceCount"`
	CPUAllocatable    string  `json:"cpuAllocatable"`
	MemoryAllocatable string  `json:"memoryAllocatable"`
	MetricsAvailable  bool    `json:"metricsAvailable"`
	CPUUsed           string  `json:"cpuUsed,omitempty"`
	MemoryUsed        string  `json:"memoryUsed,omitempty"`
	CPUPercent        float64 `json:"cpuPercent,omitempty"`
	MemoryPercent     float64 `json:"memoryPercent,omitempty"`
}
