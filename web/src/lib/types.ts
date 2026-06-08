/**
 * DTO types matching the backend Periscope API responses.
 * Source of truth: internal/k8s/types.go and internal/clusters/cluster.go.
 * Keep in sync manually for v1.
 */

export type ClusterBackend = "eks" | "kubeconfig";

export interface Cluster {
  name: string;
  backend?: ClusterBackend;
  arn?: string;
  region?: string;
  kubeconfigPath?: string;
  kubeconfigContext?: string;
  /** PR4 — false when the operator set `exec.enabled: false` in
   *  clusters.yaml. The UI hides the Open Shell action and filters this
   *  cluster out of the empty-state picker. Optional/undefined for
   *  forward-compatibility with backends that haven't shipped PR4. */
  execEnabled?: boolean;
  /** #104 — true when the server has clusterShell.enabled set. The
   *  cluster header hides the cluster-shell button when false. Optional
   *  for forward-compat with backends that haven't shipped #104 yet. */
  clusterShellEnabled?: boolean;
  /** #104 — server-default mode ("bash" | "kubectl-only"). Only set when
   *  clusterShellEnabled is true. */
  clusterShellMode?: string;
  /** #105 — true when the server has nodeShell.enabled set. The node
   *  detail header hides the node-shell button when false. Optional for
   *  forward-compat with backends that haven't shipped #105 yet. */
  nodeShellEnabled?: boolean;
}

export interface ClustersResponse {
  clusters: Cluster[];
}

export interface Whoami {
  /** Audit pipeline persistence is enabled. Hide audit nav when false. */
  auditEnabled?: boolean;
  /** "self" — user sees only own actions; "all" — user is audit-admin. */
  auditScope?: "self" | "all";
  actor: string;
}

// --- Node ---

export interface Node {
  name: string;
  status: string; // "Ready" | "NotReady" | "Unknown"
  roles: string[];
  kubeletVersion: string;
  internalIP: string;
  /** From node.kubernetes.io/instance-type label. Empty on bare-metal. */
  instanceType?: string;
  /** From topology.kubernetes.io/zone label. */
  zone?: string;
  /** EKS "ON_DEMAND"|"SPOT" or Karpenter "on-demand"|"spot". Empty on unmanaged. */
  capacityType?: string;
  cpuCapacity: string;
  memoryCapacity: string;
  createdAt: string;
  unschedulable: boolean;
  /** Cloud-provider instance handle, e.g. "aws:///us-east-1a/i-0abc12345".
   *  Empty on bare-metal / kind / pre-Initialized nodes. The SPA extracts
   *  the EC2 instance id from this to join Node rows against the CVE
   *  per-instance API (#166). */
  providerID?: string;
}

export interface NodeList {
  nodes: Node[];
}

export interface NodeCondition {
  type: string;
  status: string;
  reason?: string;
  message?: string;
}

export interface NodeTaint {
  key: string;
  value?: string;
  effect: string;
}

export interface NodeInfo {
  osImage: string;
  kernelVersion: string;
  containerRuntime: string;
  kubeletVersion: string;
  kubeProxyVersion: string;
}

export interface NodeDetail extends Node {
  conditions: NodeCondition[];
  taints?: NodeTaint[];
  labels?: Record<string, string>;
  annotations?: Record<string, string>;
  nodeInfo: NodeInfo;
  cpuAllocatable: string;
  memoryAllocatable: string;
}

// --- Namespace ---

export interface Namespace {
  name: string;
  phase: string;
  createdAt: string;
}

export interface NamespaceList {
  namespaces: Namespace[];
}

export interface NamespaceDetail extends Namespace {
  labels?: Record<string, string>;
  annotations?: Record<string, string>;
}

// --- Pod ---

export interface Pod {
  name: string;
  namespace: string;
  phase: string;
  nodeName?: string;
  podIP?: string;
  ready: string;
  restarts: number;
  /** First container's image; "" when the pod has no containers. */
  image?: string;
  /** Total container count (only set when > 1; SPA renders "+N" suffix). */
  imageCount?: number;
  /** QoS class — Guaranteed | Burstable | BestEffort. Empty during scheduling. */
  qos?: string;
  createdAt: string;
}

export interface PodList {
  pods: Pod[];
}

export interface PodCondition {
  type: string;
  status: string;
  reason?: string;
  message?: string;
}

export interface ContainerStatus {
  name: string;
  image: string;
  state: string;
  reason?: string;
  message?: string;
  ready: boolean;
  restartCount: number;
  cpuRequest?: string;
  cpuLimit?: string;
  memoryRequest?: string;
  memoryLimit?: string;
}

// --- Metrics ---

export interface NodeMetrics {
  available: boolean;
  cpuPercent?: number;
  memoryPercent?: number;
  cpuUsage?: string;
  memoryUsage?: string;
}

export interface ContainerMetrics {
  name: string;
  cpuUsage?: string;
  memoryUsage?: string;
  cpuLimitPercent: number;  // -1 = no limit set
  memLimitPercent: number;  // -1 = no limit set
}

export interface PodMetrics {
  available: boolean;
  containers?: ContainerMetrics[];
}

export interface PodDetail extends Pod {
  hostIP?: string;
  qosClass?: string;
  conditions?: PodCondition[];
  containers: ContainerStatus[];
  initContainers?: ContainerStatus[];
  labels?: Record<string, string>;
  annotations?: Record<string, string>;
}

// --- Stuck rollout (cross-kind) ---

/** StuckReason mirrors internal/k8s/stuck.go StuckReason constants. */
export type StuckReason = "progress-deadline-exceeded" | "stalled";

/** StuckState — backend-computed signal that a rollout is wedged.
 *  Pointer-valued on the backend; absent here means healthy. */
export interface StuckState {
  reason: StuckReason;
  sinceMs: number;
}

// --- Deployment ---

export interface Deployment {
  name: string;
  namespace: string;
  replicas: number;
  readyReplicas: number;
  updatedReplicas: number;
  availableReplicas: number;
  /** First container's image from the pod template. */
  image?: string;
  imageCount?: number;
  createdAt: string;
  /** Backend-computed; absent when healthy. */
  stuck?: StuckState;
}

export interface DeploymentList {
  deployments: Deployment[];
}

export interface DeploymentCondition {
  type: string;
  status: string;
  reason?: string;
  message?: string;
}

export interface ContainerSpec {
  name: string;
  image: string;
}

export interface DeploymentDetail extends Deployment {
  strategy: string;
  selector?: Record<string, string>;
  containers: ContainerSpec[];
  conditions?: DeploymentCondition[];
  pods?: JobChildPod[];
  labels?: Record<string, string>;
  annotations?: Record<string, string>;
}

// --- StatefulSet ---

export interface StatefulSet {
  name: string;
  namespace: string;
  replicas: number;
  readyReplicas: number;
  updatedReplicas: number;
  currentReplicas: number;
  /** First container's image from the pod template. */
  image?: string;
  imageCount?: number;
  createdAt: string;
  /** Backend-computed; absent when healthy. */
  stuck?: StuckState;
}

export interface StatefulSetList {
  statefulSets: StatefulSet[];
}

export interface StatefulSetDetail extends StatefulSet {
  serviceName?: string;
  updateStrategy: string;
  selector?: Record<string, string>;
  containers: ContainerSpec[];
  conditions?: DeploymentCondition[];
  pods?: JobChildPod[];
  labels?: Record<string, string>;
  annotations?: Record<string, string>;
}

// --- DaemonSet ---

export interface DaemonSet {
  name: string;
  namespace: string;
  desiredNumberScheduled: number;
  numberReady: number;
  updatedNumberScheduled: number;
  numberAvailable: number;
  numberMisscheduled: number;
  /** First container's image from the pod template. */
  image?: string;
  imageCount?: number;
  createdAt: string;
  /** Backend-computed; absent when healthy. */
  stuck?: StuckState;
}

export interface DaemonSetList {
  daemonSets: DaemonSet[];
}

export interface DaemonSetDetail extends DaemonSet {
  updateStrategy: string;
  selector?: Record<string, string>;
  nodeSelector?: Record<string, string>;
  containers: ContainerSpec[];
  conditions?: DeploymentCondition[];
  pods?: JobChildPod[];
  labels?: Record<string, string>;
  annotations?: Record<string, string>;
}

// --- Service ---

export interface ServicePort {
  name?: string;
  protocol: string;
  port: number;
  targetPort: string;
  nodePort?: number;
}

export interface Service {
  name: string;
  namespace: string;
  type: string;
  clusterIP?: string;
  externalIP?: string;
  ports: ServicePort[];
  /** Total endpoints across all EndpointSlices labeled
   *  kubernetes.io/service-name = <svc>. 0 calls out the
   *  "selector matches no pods" failure mode. */
  endpointCount: number;
  readyEndpointCount: number;
  createdAt: string;
}

export interface ServiceList {
  services: Service[];
}

export interface ServiceDetail extends Service {
  selector?: Record<string, string>;
  sessionAffinity?: string;
  pods?: JobChildPod[];
  labels?: Record<string, string>;
  annotations?: Record<string, string>;
}

// --- Ingress ---

export interface Ingress {
  name: string;
  namespace: string;
  class?: string;
  hosts: string[];
  address?: string;
  /** RFC3339. Soonest expiry across all spec.tls[] secrets;
   *  absent when ingress has no TLS or when the actor lacks
   *  secret.list permission (handler soft-fails). */
  tlsExpiresAt?: string;
  createdAt: string;
}

export interface IngressList {
  ingresses: Ingress[];
}

export interface IngressBackend {
  serviceName: string;
  servicePort: string;
}

export interface IngressPath {
  path: string;
  pathType: string;
  backend: IngressBackend;
}

export interface IngressRule {
  host: string;
  paths: IngressPath[];
}

export interface IngressTLS {
  hosts: string[];
  secretName?: string;
}

export interface IngressDetail extends Ingress {
  rules: IngressRule[];
  tls?: IngressTLS[];
  labels?: Record<string, string>;
  annotations?: Record<string, string>;
}

// --- ConfigMap ---

export interface ConfigMap {
  name: string;
  namespace: string;
  keyCount: number;
  createdAt: string;
}

export interface ConfigMapList {
  configMaps: ConfigMap[];
}

export interface ConfigMapDetail extends ConfigMap {
  data?: Record<string, string>;
  binaryDataKeys?: string[];
  labels?: Record<string, string>;
  annotations?: Record<string, string>;
}

// --- Secret (NEVER include data values in any DTO) ---

export interface Secret {
  name: string;
  namespace: string;
  type: string;
  keyCount: number;
  /** RFC3339. NotAfter of leaf cert in data["tls.crt"] for
   *  Secrets of type kubernetes.io/tls. Absent for non-TLS. */
  tlsExpiresAt?: string;
  createdAt: string;
}

export interface SecretList {
  secrets: Secret[];
}

export interface SecretKey {
  name: string;
  size: number; // bytes — metadata only
}

export interface SecretDetail extends Secret {
  keys: SecretKey[];
  labels?: Record<string, string>;
  annotations?: Record<string, string>;
  immutable?: boolean;
}

// --- Job ---

export type JobStatus = "Complete" | "Failed" | "Running" | "Suspended" | "Pending";

export interface Job {
  name: string;
  namespace: string;
  completions: string;
  status: JobStatus;
  duration?: string;
  createdAt: string;
}

export interface JobList {
  jobs: Job[];
}

export interface JobCondition {
  type: string;
  status: string;
  reason?: string;
  message?: string;
}

export interface JobChildPod {
  name: string;
  phase: string;
  ready: string;
  restarts: number;
  createdAt: string;
}

export interface JobDetail extends Job {
  parallelism: number;
  backoffLimit: number;
  active: number;
  succeeded: number;
  failed: number;
  suspend: boolean;
  startTime?: string;
  completionTime?: string;
  containers: ContainerSpec[];
  conditions?: JobCondition[];
  selector?: Record<string, string>;
  pods: JobChildPod[];
  labels?: Record<string, string>;
  annotations?: Record<string, string>;
}

// --- CronJob ---

export interface CronJob {
  name: string;
  namespace: string;
  schedule: string;
  suspend: boolean;
  active: number;
  lastScheduleTime?: string;
  createdAt: string;
}

export interface CronJobList {
  cronJobs: CronJob[];
}

export interface CronJobChildJob {
  name: string;
  status: JobStatus;
  completions: string;
  startTime?: string;
  completionTime?: string;
  duration?: string;
}

export interface CronJobDetail extends CronJob {
  concurrencyPolicy: string;
  startingDeadlineSeconds?: number;
  successfulJobsHistoryLimit: number;
  failedJobsHistoryLimit: number;
  lastSuccessfulTime?: string;
  containers: ContainerSpec[];
  jobs: CronJobChildJob[];
  labels?: Record<string, string>;
  annotations?: Record<string, string>;
}

// --- Events (per-object, used in detail-pane tabs) ---

export interface Event {
  type: string;
  reason: string;
  message: string;
  count: number;
  first: string;
  last: string;
  source: string;
}

export interface EventList {
  events: Event[];
}

// --- ClusterEvent (cluster-wide events list page) ---

export interface ClusterEvent {
  /** K8s Event resource metadata.uid — stable identity across watch deltas. */
  uid?: string;
  namespace: string;
  kind: string;
  name: string;
  type: string;
  reason: string;
  message: string;
  count: number;
  first: string;
  last: string;
  source: string;
}

export interface ClusterEventList {
  events: ClusterEvent[];
}

// --- PersistentVolumeClaim ---

export interface PVC {
  name: string;
  namespace: string;
  status: string;
  storageClass?: string;
  capacity?: string;
  accessModes: string[];
  createdAt: string;
}

export interface PVCList {
  pvcs: PVC[];
}

export interface PVCCondition {
  type: string;
  status: string;
  reason?: string;
  message?: string;
}

export interface PVCDetail extends PVC {
  volumeName?: string;
  conditions?: PVCCondition[];
  labels?: Record<string, string>;
  annotations?: Record<string, string>;
}

// --- PersistentVolume ---

export interface PVClaimRef {
  namespace: string;
  name: string;
}

export interface PV {
  name: string;
  status: string;
  storageClass?: string;
  capacity?: string;
  accessModes: string[];
  reclaimPolicy?: string;
  createdAt: string;
}

export interface PVList {
  pvs: PV[];
}

export interface PVDetail extends PV {
  claimRef?: PVClaimRef;
  volumeMode?: string;
  source?: string;
  labels?: Record<string, string>;
  annotations?: Record<string, string>;
}

// --- StorageClass ---

export interface StorageClass {
  name: string;
  provisioner: string;
  reclaimPolicy?: string;
  volumeBindingMode?: string;
  allowVolumeExpansion: boolean;
  /** Mirrors storageclass.kubernetes.io/is-default-class annotation. */
  isDefault: boolean;
  createdAt: string;
}

export interface StorageClassList {
  storageClasses: StorageClass[];
}

export interface StorageClassDetail extends StorageClass {
  parameters?: Record<string, string>;
  mountOptions?: string[];
  labels?: Record<string, string>;
  annotations?: Record<string, string>;
}

// --- RBAC ---

export interface PolicyRule {
  verbs: string[];
  apiGroups?: string[];
  resources?: string[];
  resourceNames?: string[];
  nonResourceURLs?: string[];
}

export interface RoleRef {
  kind: string;
  name: string;
}

export interface RBACSubject {
  kind: string;
  name: string;
  namespace?: string;
}

export interface Role {
  name: string;
  namespace: string;
  ruleCount: number;
  createdAt: string;
}

export interface RoleList {
  roles: Role[];
}

export interface RoleDetail extends Role {
  rules: PolicyRule[];
  labels?: Record<string, string>;
  annotations?: Record<string, string>;
}

export interface ClusterRole {
  name: string;
  ruleCount: number;
  createdAt: string;
}

export interface ClusterRoleList {
  clusterRoles: ClusterRole[];
}

export interface ClusterRoleDetail extends ClusterRole {
  rules: PolicyRule[];
  aggregationLabels?: string[];
  labels?: Record<string, string>;
  annotations?: Record<string, string>;
}

export interface RoleBinding {
  name: string;
  namespace: string;
  roleRef: string;
  subjectCount: number;
  createdAt: string;
}

export interface RoleBindingList {
  roleBindings: RoleBinding[];
}

export interface RoleBindingDetail {
  name: string;
  namespace: string;
  createdAt: string;
  roleRef: RoleRef;
  subjects: RBACSubject[];
  labels?: Record<string, string>;
  annotations?: Record<string, string>;
}

export interface ClusterRoleBinding {
  name: string;
  roleRef: string;
  subjectCount: number;
  createdAt: string;
}

export interface ClusterRoleBindingList {
  clusterRoleBindings: ClusterRoleBinding[];
}

export interface ClusterRoleBindingDetail {
  name: string;
  createdAt: string;
  roleRef: RoleRef;
  subjects: RBACSubject[];
  labels?: Record<string, string>;
  annotations?: Record<string, string>;
}

export interface ServiceAccount {
  name: string;
  namespace: string;
  secrets: number;
  createdAt: string;
}

export interface ServiceAccountList {
  serviceAccounts: ServiceAccount[];
}

export interface ServiceAccountDetail extends ServiceAccount {
  secretNames?: string[];
  labels?: Record<string, string>;
  annotations?: Record<string, string>;
}

// --- Cluster Overview ---

export interface WorkloadCount {
  total: number;
  healthy: number;
}

export interface WorkloadCounts {
  deployments: WorkloadCount;
  statefulSets: WorkloadCount;
  daemonSets: WorkloadCount;
  jobs: WorkloadCount;
  cronJobs: WorkloadCount;
}

export interface PodPhaseCounts {
  running: number;
  pending: number;
  succeeded: number;
  failed: number;
  unknown: number;
  /** Synthesized bucket for pods reporting CrashLoopBackOff /
   *  ImagePullBackOff / OOMKilled / etc. — kubectl shows these
   *  separately even when the K8s phase enum is still Pending or
   *  Running. */
  stuck: number;
}

export interface FailingPod {
  name: string;
  namespace: string;
  reason: string;
  container?: string;
  message?: string;
  restartCount?: number;
  phase: string;
}

export interface TopPod {
  name: string;
  namespace: string;
  usage: string;
  percent?: number;
  /** true → percent is "% of pod limit"; false → "% of cluster allocatable". */
  ofLimit: boolean;
}

export interface StorageInfo {
  pvCount: number;
  pvcBound: number;
  pvcPending: number;
  totalProvisioned?: string;
}

export interface ClusterSummary {
  kubernetesVersion: string;
  provider: string;

  /** EKS lifecycle window — RFC3339 strings. Populated only for
   *  EKSCapable clusters with eks:DescribeClusterVersions; absent
   *  on kubeconfig backends or when the IAM perm is missing. */
  endOfStandardSupportDate?: string;
  endOfExtendedSupportDate?: string;
  nodeCount: number;
  nodeReadyCount: number;
  podCount: number;
  namespaceCount: number;
  cpuAllocatable: string;
  memoryAllocatable: string;
  metricsAvailable: boolean;
  accessibility: {
    nodes: AccessStatus;
    pods: AccessStatus;
    namespaces: AccessStatus;
    metrics: AccessStatus;
  };
  cpuUsed?: string;
  memoryUsed?: string;
  cpuPercent?: number;
  memoryPercent?: number;

  // PR1 (Overview redesign) additions
  workloads: WorkloadCounts;
  podPhases: PodPhaseCounts;
  needsAttention: FailingPod[];
  topByCpu?: TopPod[];
  topByMemory?: TopPod[];
  storage: StorageInfo;
}

// --- Search (Cmd+K palette) ---

export type SearchKind =
  | "pods"
  | "deployments"
  | "statefulsets"
  | "daemonsets"
  | "services"
  | "configmaps"
  | "secrets"
  | "namespaces";

export interface SearchResult {
  kind: SearchKind;
  name: string;
  namespace?: string;
  score: number;
}

export interface SearchResultList {
  results: SearchResult[];
}

// --- CRDs (Custom Resource Definitions) ---

/** One column the CRD's author wants surfaced in `kubectl get` and our
 *  list view. JSONPath is the kubectl-style expression evaluated against
 *  the unstructured custom resource. */
export interface PrinterColumn {
  name: string;
  type: string;
  format?: string;
  description?: string;
  jsonPath: string;
  /** kubectl shows priority>0 only with `-o wide`. We mirror that —
   *  default list view skips them. */
  priority?: number;
}

export interface CRDVersion {
  name: string;
  served: boolean;
  storage: boolean;
  deprecated?: boolean;
  printerColumns?: PrinterColumn[];
}

export interface CRD {
  /** "<plural>.<group>" — the CRD's own metadata.name. */
  name: string;
  group: string;
  kind: string;
  plural: string;
  singular?: string;
  shortNames?: string[];
  scope: "Namespaced" | "Cluster";
  versions: CRDVersion[];
  /** The version we'll query against — the storage version when it's
   *  served, otherwise the first served version. */
  servedVersion: string;
  storageVersion: string;
  createdAt: string;
}

export interface CRDList {
  crds: CRD[];
}

// --- Custom resources (instances of a CRD) ---

export interface CustomResource {
  name: string;
  namespace?: string;
  createdAt: string;
  /** Pre-formatted printer-column values, keyed by column name. */
  columns: Record<string, string>;
}

export interface CustomResourceList {
  items: CustomResource[];
  /** The columns the rows were rendered against — frontend builds the
   *  DataTable from this so each CRD gets its own column layout
   *  without compile-time knowledge. */
  columns: PrinterColumn[];
  scope: "Namespaced" | "Cluster";
}

export interface CustomResourceDetail {
  name: string;
  namespace?: string;
  kind: string;
  apiVersion: string;
  createdAt: string;
  /** Full unstructured object — render as YAML, pull individual fields
   *  for describe view, etc. */
  object: Record<string, unknown>;
}

// --- Resource catalog ---

export type ResourceKind =
  | "overview"
  | "nodes"
  | "namespaces"
  | "pods"
  | "deployments"
  | "statefulsets"
  | "daemonsets"
  | "services"
  | "ingresses"
  | "configmaps"
  | "secrets"
  | "jobs"
  | "cronjobs"
  | "events"
  | "pvcs"
  | "pvs"
  | "storageclasses"
  | "roles"
  | "clusterroles"
  | "rolebindings"
  | "clusterrolebindings"
  | "serviceaccounts"
  | "horizontalpodautoscalers"
  | "poddisruptionbudgets"
  | "replicasets"
  | "networkpolicies"
  | "endpointslices"
  | "ingressclasses"
  | "resourcequotas"
  | "limitranges"
  | "priorityclasses"
  | "runtimeclasses"
  | "crds";

export type ResourceListResponse =
  | NodeList
  | NamespaceList
  | PodList
  | DeploymentList
  | StatefulSetList
  | DaemonSetList
  | ServiceList
  | IngressList
  | ConfigMapList
  | SecretList
  | JobList
  | CronJobList
  | ClusterEventList
  | PVCList
  | PVList
  | StorageClassList
  | RoleList
  | ClusterRoleList
  | RoleBindingList
  | ClusterRoleBindingList
  | ServiceAccountList
  | HPAList
  | PDBList
  | ReplicaSetList
  | NetworkPolicyList
  | EndpointSliceList
  | IngressClassList
  | ResourceQuotaList
  | LimitRangeList
  | PriorityClassList
  | RuntimeClassList;

// --- HPA ---

export interface HPA {
  name: string;
  namespace: string;
  createdAt: string;
  target: string;
  minReplicas: number;
  maxReplicas: number;
  currentReplicas: number;
  desiredReplicas: number;
  ready: boolean;
}

export interface HPAList {
  hpas: HPA[];
}

export interface HPADetail extends HPA {
  conditions?: DeploymentCondition[];
  labels?: Record<string, string>;
  annotations?: Record<string, string>;
}

// --- PodDisruptionBudget ---

export interface PDB {
  name: string;
  namespace: string;
  createdAt: string;
  minAvailable: string;
  maxUnavailable: string;
  currentHealthy: number;
  desiredHealthy: number;
  expectedPods: number;
  disruptionsAllowed: number;
}

export interface PDBList {
  pdbs: PDB[];
}

export interface PDBDetail extends PDB {
  selector: string;
  labels?: Record<string, string>;
  annotations?: Record<string, string>;
}

// --- ReplicaSet ---

export interface ReplicaSet {
  name: string;
  namespace: string;
  createdAt: string;
  desired: number;
  current: number;
  ready: number;
  /** First container's image from the pod template. */
  image?: string;
  imageCount?: number;
  owner: string;
}

export interface ReplicaSetList {
  replicaSets: ReplicaSet[];
}

export interface ReplicaSetDetail extends ReplicaSet {
  selector?: Record<string, string>;
  labels?: Record<string, string>;
  annotations?: Record<string, string>;
  conditions?: DeploymentCondition[];
}

// --- EndpointSlice (discovery.k8s.io/v1) ---

export interface EndpointSlicePort {
  name?: string;
  protocol?: string;
  port: number;
  appProtocol?: string;
}

export interface EndpointSliceTarget {
  kind: string;
  name: string;
  namespace?: string;
}

export interface EndpointSliceEndpoint {
  addresses: string[];
  hostname?: string;
  nodeName?: string;
  zone?: string;
  ready: boolean;
  serving: boolean;
  terminating: boolean;
  targetRef?: EndpointSliceTarget;
}

export interface EndpointSlice {
  name: string;
  namespace: string;
  /** "IPv4" | "IPv6" | "FQDN". */
  addressType: string;
  ports: EndpointSlicePort[];
  /** Parent Service name from the kubernetes.io/service-name label. */
  serviceName?: string;
  readyCount: number;
  totalCount: number;
  createdAt: string;
}

export interface EndpointSliceList {
  endpointSlices: EndpointSlice[];
}

export interface EndpointSliceDetail extends EndpointSlice {
  endpoints?: EndpointSliceEndpoint[];
  labels?: Record<string, string>;
  annotations?: Record<string, string>;
}

// --- NetworkPolicy ---

export interface NetworkPolicyRule {
  ports: string[];
  peers: string[];
}

export interface NetworkPolicy {
  name: string;
  namespace: string;
  createdAt: string;
  podSelector: string;
  policyTypes: string[];
}

export interface NetworkPolicyList {
  networkPolicies: NetworkPolicy[];
}

export interface NetworkPolicyDetail extends NetworkPolicy {
  ingressRules?: NetworkPolicyRule[];
  egressRules?: NetworkPolicyRule[];
  labels?: Record<string, string>;
  annotations?: Record<string, string>;
}

// --- IngressClass ---

export interface IngressClass {
  name: string;
  createdAt: string;
  controller: string;
  isDefault: boolean;
}

export interface IngressClassList {
  ingressClasses: IngressClass[];
}

export interface IngressClassDetail extends IngressClass {
  parameters: string;
  labels?: Record<string, string>;
  annotations?: Record<string, string>;
}

// --- ResourceQuota ---

export interface QuotaEntry {
  hard: string;
  used: string;
}

export interface ResourceQuota {
  name: string;
  namespace: string;
  createdAt: string;
  items: Record<string, QuotaEntry>;
}

export interface ResourceQuotaList {
  resourceQuotas: ResourceQuota[];
}

// --- LimitRange ---

export interface LimitRangeItem {
  type: string;
  default?: Record<string, string>;
  defaultRequest?: Record<string, string>;
  max?: Record<string, string>;
  min?: Record<string, string>;
  maxLimitRequestRatio?: Record<string, string>;
}

export interface LimitRange {
  name: string;
  namespace: string;
  createdAt: string;
  limitCount: number;
}

export interface LimitRangeList {
  limitRanges: LimitRange[];
}

export interface LimitRangeDetail extends LimitRange {
  limits: LimitRangeItem[];
  labels?: Record<string, string>;
  annotations?: Record<string, string>;
}

// --- PriorityClass ---

export interface PriorityClass {
  name: string;
  createdAt: string;
  value: number;
  globalDefault: boolean;
  preemptionPolicy: string;
}

export interface PriorityClassList {
  priorityClasses: PriorityClass[];
}

export interface PriorityClassDetail extends PriorityClass {
  description: string;
  labels?: Record<string, string>;
  annotations?: Record<string, string>;
}

// --- RuntimeClass ---

export interface RuntimeClass {
  name: string;
  createdAt: string;
  handler: string;
  cpuOverhead: string;
  memoryOverhead: string;
}

export interface RuntimeClassList {
  runtimeClasses: RuntimeClass[];
}

export interface RuntimeClassDetail extends RuntimeClass {
  nodeSelector?: Record<string, string>;
  tolerations?: string[];
  labels?: Record<string, string>;
  annotations?: Record<string, string>;
}

export type AccessStatus = "ok" | "forbidden" | "unavailable";

// =====================================================================
// Fleet — multi-cluster home page
// =====================================================================

/** Stable status enum mirrored from cmd/periscope/fleet_handler.go. */
export type FleetStatus =
  | "healthy"
  | "degraded"
  | "unreachable"
  | "unknown"
  | "denied";

export interface FleetCount {
  ready: number;
  total: number;
}

export interface FleetPods {
  running: number;
  pending: number;
  failed: number;
  total: number;
}

export interface FleetSummary {
  nodes: FleetCount;
  pods: FleetPods;
  namespaces: number;
  /** Substitute for "warnings15m" in v1: PodPhases.Stuck + Failed. */
  stuckOrFailed: number;
}

export interface HotSignal {
  /** Raw NeedsAttention[].Reason value: CrashLoopBackOff, ImagePullBackOff, etc. */
  kind: string;
  count: number;
}

export interface FleetError {
  /** denied | auth_failed | timeout | apiserver_unreachable | unknown */
  code: string;
  message: string;
}

export interface FleetClusterEntry {
  name: string;
  backend: string;
  region?: string;
  /** AWS account ID parsed from the cluster ARN; empty for kubeconfig backends. */
  accountID?: string;
  environment?: string;
  /** Kubeconfig context name; only present for kubeconfig backends. */
  context?: string;
  tags?: Record<string, string>;

  /** EKS lifecycle window. Populated only for EKSCapable clusters with
   *  the eks:DescribeClusterVersions IAM permission. Drives the
   *  version + EoSS chip on the cluster card. */
  kubernetesVersion?: string;
  /** RFC3339. Empty when AWS has not yet announced EoSS for this version. */
  endOfStandardSupportDate?: string;
  /** RFC3339. Empty when extended support is not announced. */
  endOfExtendedSupportDate?: string;
  status: FleetStatus;
  /** RFC3339 timestamp. v1: "now" on every response (no historical ledger). */
  lastContact: string;
  /** Null when status is unreachable/denied/unknown. */
  summary?: FleetSummary | null;
  /** Always present, [] when no signals. */
  hotSignals: HotSignal[];
  /** Null on success. */
  error?: FleetError | null;
}

export interface FleetRollup {
  totalClusters: number;
  byStatus: Partial<Record<FleetStatus, number>>;
  /** Buckets keyed by environment label; "other" for untagged. */
  byEnvironment: Record<string, number>;
  generatedAt: string;
}

export interface FleetResponse {
  rollup: FleetRollup;
  clusters: FleetClusterEntry[];
}

// =====================================================================
// Audit log — /api/audit
// =====================================================================

export type AuditOutcome = "success" | "failure" | "denied";

export type AuditVerb =
  | "apply"
  | "delete"
  | "trigger"
  | "exec_open"
  | "exec_close"
  | "secret_reveal"
  | "log_open";

export interface AuditActor {
  sub: string;
  email?: string;
  groups?: string[];
}

export interface AuditResourceRef {
  group?: string;
  version?: string;
  resource?: string;
  namespace?: string;
  name?: string;
}

export interface AuditRow {
  id: number;
  timestamp: string;          // RFC3339Nano
  requestId?: string;
  route?: string;
  actor: AuditActor;
  verb: string;               // string not AuditVerb to survive future verbs gracefully
  outcome: AuditOutcome;
  cluster?: string;
  resource: AuditResourceRef;
  reason?: string;
  extra?: Record<string, unknown>;
}

export interface AuditQueryResult {
  items: AuditRow[];
  total: number;
  limit: number;
  offset: number;
}

/** All optional. Frontend always sends at least `cluster` from /clusters/:cluster/audit. */
export interface AuditQueryParams {
  cluster?: string;
  from?: string;              // RFC3339Nano
  to?: string;                // RFC3339Nano
  actor?: string;
  verb?: string;
  outcome?: AuditOutcome;
  namespace?: string;
  name?: string;
  requestId?: string;
  limit?: number;
  offset?: number;
}

// --- Helm release browser (read-only, issue #9) ----------------------

/** One row in the /helm/releases list. Slim — manifest/values fetched
 *  per detail click. */
export interface HelmReleaseSummary {
  name: string;
  namespace: string;
  revision: number;
  status: string;
  chartName: string;
  chartVersion: string;
  appVersion: string;
  /** RFC3339; "" when the release info block is missing (rare). */
  updated: string;
}

export interface HelmReleasesResponse {
  releases: HelmReleaseSummary[];
  /** True when the cluster has more than 200 releases — the SPA shows
   *  a banner so users know the list is incomplete. */
  truncated: boolean;
}

/** One (apiVersion, kind, namespace, name) tuple parsed from the
 *  rendered manifest. Populated on detail blobs; used for the resource
 *  summary in the detail header and for v2 SAR-gated write ops. */
export interface HelmManifestObject {
  apiVersion: string;
  kind: string;
  namespace?: string;
  name: string;
}

export interface HelmReleaseDetail {
  name: string;
  namespace: string;
  revision: number;
  status: string;
  description?: string;
  chartName: string;
  chartVersion: string;
  appVersion: string;
  /** Optional chart-supplied icon URL. Some charts ship one in
   *  Chart.yaml; many don't. */
  icon?: string;
  updated: string;
  notes?: string;
  /** Empty string when the release was installed without overrides;
   *  the SPA renders the empty state in that case rather than "{}\n". */
  valuesYaml: string;
  manifestYaml: string;
  resources: HelmManifestObject[];
  /** Install ref (oci|http|https://...) the operator used to install
   *  or last-upgrade this release via Periscope. Pre-fills the upgrade
   *  dialog. Empty for releases installed via the helm CLI or any
   *  non-Periscope tooling — Periscope only writes this annotation
   *  on its own install/upgrade actions. */
  installRef?: string;
  /** Chart-name component for HTTP repos. Empty for OCI refs (the
   *  chart name is implicit in the ref's last segment). */
  installChartName?: string;
}

export interface HelmHistoryEntry {
  revision: number;
  status: string;
  chartName: string;
  chartVersion: string;
  appVersion: string;
  description?: string;
  updated: string;
}

export interface HelmHistoryResponse {
  revisions: HelmHistoryEntry[];
}

export interface HelmDiffSide {
  revision: number;
  yaml: string;
}

/** One entry in the structured change list. `kind` follows dyff:
 *  "modify" | "add" | "remove" | "order". `path` uses dyff's
 *  go-patch-style notation. */
export interface HelmDiffItem {
  path: string;
  kind: "modify" | "add" | "remove" | "order" | string;
  before?: string;
  after?: string;
}

export interface HelmDiffResponse {
  from: HelmDiffSide;
  to: HelmDiffSide;
  changes: HelmDiffItem[];
}

// ─── Helm chart fetch (#73) ───────────────────────────────────────

export interface ChartMaintainer {
  name?: string;
  email?: string;
  url?: string;
}

export interface ChartDep {
  name: string;
  version?: string;
  repository?: string;
  alias?: string;
  condition?: string;
}

/** Chart.yaml projection — same field set Helm uses for v2. */
export interface ChartMeta {
  name: string;
  version: string;
  apiVersion: string;
  appVersion?: string;
  description?: string;
  /** Semver constraint, e.g. ">=1.24". Render a warning when the
   *  cluster's K8s version doesn't satisfy it. */
  kubeVersion?: string;
  type?: string;
  keywords?: string[];
  home?: string;
  sources?: string[];
  maintainers?: ChartMaintainer[];
  icon?: string;
  annotations?: Record<string, string>;
  /** v1.1 rejects charts with non-empty deps; the SPA still
   *  renders this list so operators understand WHY. */
  dependencies?: ChartDep[];
}

export interface ChartFetchResult {
  meta: ChartMeta;
  /** Verbatim values.yaml — feed straight to Monaco. */
  values: string;
  /** Decoded values.schema.json when shipped with the chart. */
  schema?: Record<string, unknown>;
}

export interface ChartVersionsResult {
  ref: string;
  versions: string[];
  latest?: string;
}

export interface ChartFetchRequest {
  ref: string;
  /** Required for HTTP repos (chart name in index.yaml). Empty for
   *  OCI refs (chart name is implicit in the ref's last segment). */
  chart?: string;
  version: string;
}

// ─── Helm preview + install + upgrade (#75 / #76) ─────────────────────

/** Pre-flight RBAC denial entry on a PreviewResponse. The SPA renders
 *  these inline as a "the apiserver would reject these" list. */
export interface PreviewDenial {
  group?: string;
  resource: string;
  namespace?: string;
  name?: string;
  verb: "create" | "patch" | "update";
  reason: string;
}

/** Preview response shape (#75). Diff is null for install mode (no
 *  current state to compare against); populated for upgrade. Denied
 *  is null when every kind passes the SAR pre-flight. */
export interface PreviewResponse {
  manifests: HelmManifestObject[];
  manifestYaml: string;
  diff: HelmDiffResponse | null;
  denied: PreviewDenial[] | null;
}

/** Install preview request body. */
export interface HelmInstallPreviewRequest {
  ref: string;
  chartName?: string;
  version: string;
  namespace: string;
  releaseName: string;
  values: string;
}

/** Upgrade preview request body — ns + releaseName are URL path. */
export interface HelmUpgradePreviewRequest {
  ref: string;
  chartName?: string;
  version: string;
  values: string;
}

/** Install action request body (#76). All knobs optional — handler
 *  defaults Atomic=true / Wait=true / IncludeCRDs=true. */
export interface HelmInstallRequest {
  ref: string;
  chartName?: string;
  version: string;
  namespace: string;
  releaseName: string;
  values: string;
  atomic?: boolean;
  wait?: boolean;
  waitForJobs?: boolean;
  includeCRDs?: boolean;
  timeoutSeconds?: number;
}

/** Upgrade action request body (#76). */
export interface HelmUpgradeRequest {
  ref: string;
  chartName?: string;
  version: string;
  values: string;
  atomic?: boolean;
  wait?: boolean;
  waitForJobs?: boolean;
  timeoutSeconds?: number;
  cleanupOnFail?: boolean;
  maxHistory?: number;
}

/** Result shape returned by both install + upgrade actions. */
/** Uninstall response shape (#123). RevisionsRemoved is the count of
 *  revisions helm pruned from storage — useful for the SPA's success
 *  toast and forensic audit. */
export interface HelmUninstallResult {
  release: HelmActionResult["release"];
  revisionsRemoved: number;
}

// HelmRollbackResult mirrors the backend (#77) HelmRollbackResult.
// helm always assigns a NEW revision on rollback rather than mutating
// the target — newRevision is what the SPA shows in the success toast,
// fromRevision/toRevision capture the audit-relevant transition.
export interface HelmRollbackResult {
  release: HelmActionResult["release"];
  newRevision: number;
  fromRevision: number;
  toRevision: number;
}

export interface HelmActionResult {
  release: {
    name: string;
    namespace: string;
    revision: number;
    status: string;
    chart: { name: string; version: string };
    deployedAt: string;
    notes?: string;
  };
  rolledBack?: boolean;
  rollbackError?: string;
}

// ─── Workload rollback (#71) ─────────────────────────────────────────

/** One entry in the rollout history of a Deployment / StatefulSet /
 *  DaemonSet. `podTemplate` is the full PodTemplateSpec serialized
 *  as a generic record so the dialog can drive the diff viewer
 *  without an extra round-trip per click. */
export interface Revision {
  revision: number;
  isCurrent: boolean;
  changeCause?: string;
  createdAt: string;
  podTemplateHash?: string;
  images: string[];
  podTemplate: Record<string, unknown>;
}

/** Detected upstream reconciler. The dialog renders a yellow banner
 *  warning the operator that a rollback may be reverted on the next
 *  reconcile cycle unless they also revert the source. */
export interface ManagedBy {
  controller: "argocd" | "helm" | "flux";
  instance?: string;
}

export interface RevisionHistory {
  currentRevision: number;
  revisions: Revision[];
  /** Deployment-only; null/undefined for STS / DS. When true, the
   *  dialog refuses the rollback path and offers a Resume button. */
  paused?: boolean;
  managedBy?: ManagedBy;
  /** Name of an HPA in the same namespace whose scaleTargetRef
   *  points at this workload. Empty when none. */
  hpaTarget?: string;
}

export interface RollbackRequest {
  revision: number;
  /** Optional human reason. Flows into the
   *  `kubernetes.io/change-cause` annotation on the new revision and
   *  the structured audit row. */
  reason?: string;
}

export interface RollbackResponse {
  newRevision: number;
  patchedAt: string;
}

// --- EKS Upgrade Insights (issue #103) ---------------------------------
//
// Mirrors cmd/periscope/eks_insights_handler.go. Three observations
// for the SPA developer:
//
//   1. `status` is one of PASSING / WARNING / ERROR / UNKNOWN — the
//      same string AWS returns. Render with the existing traffic-
//      light glyphs.
//   2. `editorPath` on a UpgradeInsightResource is empty when the
//      backend couldn't parse the kubernetesResourceUri. The SPA
//      should render the raw URI as monospace text in that case
//      rather than a broken link.
//   3. The 422 error envelope has `code: "E_BACKEND_NOT_EKS"` for
//      non-EKS clusters; branch on that to render the empty state.

export interface UpgradeInsightCounts {
  passing: number;
  warning: number;
  error: number;
  unknown: number;
}

export type UpgradeInsightStatus = "PASSING" | "WARNING" | "ERROR" | "UNKNOWN";

export interface UpgradeInsightSummary {
  id: string;
  name: string;
  category: string;
  kubernetesVersion?: string;
  status: UpgradeInsightStatus;
  statusReason?: string;
  lastRefreshTime?: string;
  lastTransitionTime?: string;
  description?: string;
}

export interface UpgradeInsightsListResponse {
  insights: UpgradeInsightSummary[];
  counts: UpgradeInsightCounts;
  targetKubernetesVersion?: string;
}

export interface UpgradeInsightResource {
  kubernetesResourceUri: string;
  arn?: string;
  group?: string;
  version?: string;
  resource?: string;
  namespace?: string;
  name?: string;
  /** Cluster-rooted SPA path that opens the resource's YAML editor.
   *  Empty when the backend couldn't map the URI to a known route. */
  editorPath?: string;
  status?: UpgradeInsightStatus;
  statusReason?: string;
}

export interface DeprecationClientStat {
  userAgent?: string;
  numberOfRequestsLast30Days?: number;
  lastRequestTime?: string;
}

export interface DeprecationDetail {
  usage?: string;
  replacedWith?: string;
  stopServingVersion?: string;
  startServingReplacementVersion?: string;
  clientStats?: DeprecationClientStat[];
}

export interface UpgradeInsightDetail extends UpgradeInsightSummary {
  recommendation?: string;
  additionalInfo?: Record<string, string>;
  resources: UpgradeInsightResource[];
  deprecationDetails?: DeprecationDetail[];
}

// --- EKS managed node groups (issue #103) ------------------------------
//
// Mirrors cmd/periscope/eks_nodegroups_handler.go.
//
// PR-2: drift fields are present in the type but always come back
// false / empty until PR-3 wires the SSM-based latest-AMI lookup.
// The SPA renders drift badges only when `driftComputed` is true.
//
// Custom AMIs (`customAmi: true`, AmiType="CUSTOM") never get drift
// computed by design — when an operator ships a custom image, AWS
// can't tell us what "latest" means.

export interface NodegroupSummary {
  name: string;
  status: string;
  amiType: string;
  capacityType?: string;
  kubernetesVersion?: string;
  releaseVersion?: string;
  customAmi: boolean;
  instanceTypesPreview?: string;
  desiredSize: number;
  minSize: number;
  maxSize: number;
  healthIssueCount: number;
  createdAt?: string;

  // Drift fields — populated only when driftComputed is true.
  driftComputed: boolean;
  latestReleaseVersion?: string;
  daysBehind?: number;
  isBehind?: boolean;
}

export interface NodegroupsCounts {
  total: number;
  behind: number;
  custom: number;
  healthy: number;
}

export interface NodegroupsListResponse {
  nodegroups: NodegroupSummary[];
  counts: NodegroupsCounts;
}

export interface NodegroupHealthIssue {
  code?: string;
  message?: string;
  resourceIds?: string[];
}

export interface LaunchTemplateRef {
  id?: string;
  name?: string;
  version?: string;
}

export interface NodegroupDetail extends NodegroupSummary {
  arn?: string;
  nodeRole?: string;
  instanceTypes?: string[];
  subnets?: string[];
  diskSize?: number;
  labels?: Record<string, string>;
  tags?: Record<string, string>;

  /** EKS lifecycle window. Populated only for EKSCapable clusters with
   *  the eks:DescribeClusterVersions IAM permission. Drives the
   *  version + EoSS chip on the cluster card. */
  kubernetesVersion?: string;
  /** RFC3339. Empty when AWS has not yet announced EoSS for this version. */
  endOfStandardSupportDate?: string;
  /** RFC3339. Empty when extended support is not announced. */
  endOfExtendedSupportDate?: string;
  healthIssues?: NodegroupHealthIssue[];
  launchTemplate?: LaunchTemplateRef;
  modifiedAt?: string;
  autoScalingGroups?: string[];
}

// --- EKS managed add-ons (issue #117) -------------------------------

export type AddonHealthGlyph = "ok" | "update" | "fail";

export interface AddonHealthIssue {
  code?: string;
  message?: string;
  resourceIds?: string[];
}

export interface AddonSummary {
  name: string;
  status: string;
  version?: string;
  kubernetesVersion?: string;
  healthIssueCount: number;
  /** Three-state symbol the SPA renders:
   *   "ok"     → ●  healthy & current
   *   "update" → ▲  newer version exists or blocks next minor
   *   "fail"   → ✕  health issues present */
  healthGlyph: AddonHealthGlyph;
  updateAvailable: boolean;
  latestVersion?: string;
  /** Min/max K8s minors the *installed* add-on version supports. Empty
   *  when the catalog had nothing to say (custom builds or soft-fail). */
  compatMinK8s?: string;
  compatMaxK8s?: string;
  /** True when the installed version's compat list does NOT include
   *  (cluster.k8s + 1). Surfaces as the "blocks 1.30" subtitle. */
  blocksNextMinor: boolean;
  createdAt?: string;
  modifiedAt?: string;
}

export interface AddonsCounts {
  total: number;
  healthy: number;
  updateAvailable: number;
  unhealthy: number;
  blocksNextMinor: number;
}

export interface AddonsListResponse {
  addons: AddonSummary[];
  counts: AddonsCounts;
  /** Cluster's K8s version as AWS reports it. Drives the table
   *  header — "EKS add-ons · prod-eu-west-1 (k8s 1.29)". */
  clusterKubernetesVersion?: string;
}

export interface AddonVersionEntry {
  version: string;
  compatibleK8sVersions: string[];
  defaultVersion: boolean;
}

export interface AddonDetail extends AddonSummary {
  arn?: string;
  serviceAccountRoleArn?: string;
  /** EKS Pod Identity association ARNs attached to the addon — the
   *  newer alternative to IRSA via `serviceAccountRoleArn`. Both can
   *  be present, neither, or one. Empty/absent means the addon
   *  doesn't use AWS-managed pod identity. */
  podIdentityAssociations?: string[];
  configurationValues?: string;
  /** AWS-published JSON schema for the addon's config. May be empty
   *  if DescribeAddonConfiguration soft-failed. */
  configurationSchema?: string;
  healthIssues?: AddonHealthIssue[];
  availableVersions?: AddonVersionEntry[];
  owner?: string;
  publisher?: string;
}

// --- EKS add-on catalog (issue #119, PR-1) --------------------------
//
// "What could I install?" — the unfiltered DescribeAddonVersions
// catalog scoped to the cluster's K8s version, with installed-state
// annotation layered in server-side from #117's per-cluster cache.
// Pairs with AddonsListResponse ("what's installed?").

export interface CatalogAddonVersion {
  version: string;
  kubernetesVersions: string[];
  default?: boolean;
}

export interface CatalogInstalled {
  version: string;
  status?: string;
}

export interface CatalogAddon {
  name: string;
  type?: string;
  /** AWS-reported ownership: "aws" / "amazon-web-services" mark
   *  AWS-authored add-ons; anything else is third-party. */
  owner?: string;
  publisher?: string;
  /** True when AWS reports the addon as a marketplace listing.
   *  Operators must accept the marketplace EULA outside Periscope
   *  before install will succeed; the catalog flags these rows. */
  marketplaceProduct?: boolean;
  compatibleVersions: CatalogAddonVersion[];
  /** Non-null when the addon is installed on this cluster.
   *  Best-effort: only populated when the per-cluster addons-list
   *  cache is warm; the SPA falls back to layering from useAddons()
   *  if the field is absent. */
  installed?: CatalogInstalled | null;
}

export interface AddonCatalogResponse {
  available: CatalogAddon[];
  /** Cluster's K8s version as AWS reports it. Drives the catalog
   *  filter and the table header. */
  kubernetesVersion?: string;
}

// --- EKS add-on writes (issue #119, PR-2/3) ------------------------

/** AWS-accepted resolveConflicts values. NONE preserves operator-set
 *  fields and fails on conflict; OVERWRITE replaces them; PRESERVE
 *  keeps existing values. Empty string lets AWS default (NONE). */
export type AddonResolveConflicts = "" | "NONE" | "OVERWRITE" | "PRESERVE";

export interface AddonInstallRequest {
  addonName: string;
  addonVersion: string;
  /** JSON or YAML string. AWS infers from content; the SPA emits
   *  whichever the editor produced (form mode → JSON, YAML mode →
   *  YAML). */
  configurationValues?: string;
  /** Optional IAM role ARN for the addon's service account. Requires
   *  iam:PassRole on the operator's IAM policy if set. */
  serviceAccountRoleArn?: string;
  resolveConflicts?: AddonResolveConflicts;
}

/** Body shape for PUT /api/clusters/{c}/eks/addons/{name} (#119, PR-3).
 *  Same fields as AddonInstallRequest minus addonName (URL param). */
export interface AddonUpgradeRequest {
  /** The *target* version. Required — the SPA must explicitly choose. */
  addonVersion: string;
  configurationValues?: string;
  serviceAccountRoleArn?: string;
  resolveConflicts?: AddonResolveConflicts;
}

export interface AddonConfigurationResponse {
  /** AWS-published JSON Schema for the (addon, version) pair as a
   *  raw string. Empty when AWS returned no schema for the version
   *  (older addon versions ship without one); the SPA falls back to
   *  the YAML editor in that case. */
  configurationSchema: string;
}

// ─── Karpenter dashboard (#118) ──────────────────────────────────────────

export interface KarpenterDashboard {
  available: boolean;
  nodepools?: NodePoolView[];
  nodeclaims?: NodeClaimView[];
  pendingPods?: PendingPodView[];
  truncated?: boolean;
  metricsAvailable: boolean;
}

export interface NodePoolView {
  name: string;
  weight?: number;
  disruption: NodePoolDisruption;
  limits?: Record<string, string>;
  usage?: Record<string, string>;
  nodeCount: number;
  conditions?: NodeCondition[];
  cost?: NodePoolCost;
}

export interface NodePoolDisruption {
  consolidationPolicy?: string;
  consolidateAfter?: string;
  expireAfter?: string;
  budgets?: NodePoolBudgetEntry[];
}

export interface NodePoolBudgetEntry {
  nodes?: string;
  schedule?: string;
  duration?: string;
  reasons?: string[];
}

export interface NodePoolCost {
  currentHourly: number;
  onDemandHourly: number;
  spotSavingsPct: number;
}

export interface NodeClaimView {
  name: string;
  nodepool: string;
  instanceType?: string;
  capacityType?: string;
  zone?: string;
  providerID?: string;
  ec2NodeClass?: string;
  conditions?: NodeCondition[];
  createdAt?: string;
}

export interface PendingPodView {
  namespace: string;
  name: string;
  pendingFor: string;
  reason?: string;
  incompatibilityReasons?: NodePoolIncompat[];
}

export interface NodePoolIncompat {
  nodepool: string;
  reason: string;
}

// ── CVE / Inspector v2 (#165 + #166) ─────────────────────────────────
//
// Wire shapes that mirror internal/cve/types.go on the backend. JSON
// tags on the Go side are camelCase (see PR #168), so these types
// match field-for-field. The SeverityCounts / ScanState helpers in
// `web/src/lib/severity.ts` operate on these shapes directly.

export interface CveSeverityCounts {
  critical: number;
  high: number;
  medium: number;
  low: number;
  informational: number;
}

export interface CveOwnerRef {
  /** `managed-nodegroup` | `karpenter-nodeclaim` | `unmanaged` */
  kind: string;
  name?: string;
}

export interface CveInstanceRow {
  instanceId: string;
  owner: CveOwnerRef;
  ami?: string;
  severityCounts: CveSeverityCounts;
  lastFetchedAt: string;
}

export interface CveInstancesResp {
  instances: CveInstanceRow[];
  inspectorEnabled: boolean;
  hydrated: boolean;
}

/** Single finding row — matches awsinspector.Finding on the backend
 *  (PR #168). The operator-actionable fields (description, remediation,
 *  epssScore, exploitAvailable, fixAvailable) drive the expandable row
 *  in SecurityTab. */
export interface CveFinding {
  resourceId: string;
  arn?: string;
  title?: string;
  cve?: string;
  severity?: string;
  cvssV3Score?: number;
  packageName?: string;
  packageVersion?: string;
  fixedVersion?: string;
  firstObservedAt?: string;
  lastObservedAt?: string;
  description?: string;
  remediation?: string;
  remediationUrl?: string;
  epssScore?: number;
  /** "YES" | "NO" | "" — Inspector exploit-availability flag */
  exploitAvailable?: string;
  /** "YES" | "NO" | "PARTIAL" | "" — categorical fix flag (the
   *  concrete version is on `fixedVersion`). */
  fixAvailable?: string;
  inspectorUrl: string;
}

export interface CveFindingsResp {
  findings: CveFinding[];
  lastFetchedAt: string;
  inspectorEnabled: boolean;
  hydrated: boolean;
}

export type CveScanState = "scanned" | "non-ecr" | "pending";
export type CveScanCoverage = "full" | "partial" | "none";

/** CvePackageGroup is one entry per (container, package) — the
 *  server-side grouped + prioritized projection of raw findings.
 *  Backend sorts by triage priority (exploits first, then severity
 *  desc, then CVSS desc); SPA renders as-is. Shared shape with the
 *  future MCP / AI-agent tool layer. */
export interface CvePackageGroup {
  packageName: string;
  currentVersion?: string;
  /** Highest fixedVersion across the group. Upgrading to this
   *  closes every CVE in the group. Empty when no fix exists. */
  suggestedFix?: string;
  findings: CveFinding[];
  counts: CveSeverityCounts;
  exploitCount: number;
  fixableCount: number;
}

export interface CveContainerRow {
  name: string;
  image: string;
  digest?: string;
  scanState: CveScanState;
  severityCounts?: CveSeverityCounts;
  packages?: CvePackageGroup[];
}

export interface CvePodRow {
  namespace: string;
  name: string;
  containers: CveContainerRow[];
  rolledUpSeverityCounts: CveSeverityCounts;
  scanCoverage: CveScanCoverage;
}

export interface CvePodsResp {
  pods: CvePodRow[];
  next?: string;
  inspectorEnabled: boolean;
  hydrated: boolean;
}

export interface CveWorkloadRef {
  kind: string; // "Deployment" | "StatefulSet" | "DaemonSet" | "ReplicaSet" | "Job"
  namespace: string;
  name: string;
}

export interface CveByWorkloadResp {
  workload: CveWorkloadRef;
  pods: CvePodRow[];
  rolledUpSeverityCounts: CveSeverityCounts;
  scanCoverage: CveScanCoverage;
  inspectorEnabled: boolean;
  hydrated: boolean;
}

export interface CveEntryCounts {
  digests: number;
  instances: number;
}

export interface CveStatusResp {
  inspectorEnabled: boolean;
  hydrated: boolean;
  /** Zero / missing before the first hydrate completes. */
  lastHydrate?: string;
  entryCounts: CveEntryCounts;
}

/** Per-pod summary derived by the SPA from CvePodsResp pages.
 *  Not a backend type — it's the lookup row the Pods column reads. */
export interface CvePodSummary {
  namespace: string;
  name: string;
  counts: CveSeverityCounts;
  coverage: CveScanCoverage;
}

export interface CveRefreshRequest {
  digests?: string[];
  instanceIds?: string[];
}
