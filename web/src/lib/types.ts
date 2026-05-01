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
}

export interface ClustersResponse {
  clusters: Cluster[];
}

export interface Whoami {
  actor: string;
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

// --- Resource catalog ---

export type ResourceKind =
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
  | "events";

export type ResourceListResponse =
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
  | ClusterEventList;
