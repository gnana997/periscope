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
}

export interface ClustersResponse {
  clusters: Cluster[];
}

export interface Whoami {
  actor: string;
}

// --- Node ---

export interface Node {
  name: string;
  status: string; // "Ready" | "NotReady" | "Unknown"
  roles: string[];
  kubeletVersion: string;
  internalIP: string;
  cpuCapacity: string;
  memoryCapacity: string;
  createdAt: string;
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

// --- Deployment ---

export interface Deployment {
  name: string;
  namespace: string;
  replicas: number;
  readyReplicas: number;
  updatedReplicas: number;
  availableReplicas: number;
  createdAt: string;
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
  createdAt: string;
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
  createdAt: string;
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
  nodeCount: number;
  nodeReadyCount: number;
  podCount: number;
  namespaceCount: number;
  cpuAllocatable: string;
  memoryAllocatable: string;
  metricsAvailable: boolean;
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
