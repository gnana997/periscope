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

// --- Events ---

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

// --- Resource catalog ---

export type ResourceKind =
  | "namespaces"
  | "pods"
  | "deployments"
  | "statefulsets"
  | "daemonsets"
  | "services"
  | "configmaps";

export type ResourceListResponse =
  | NamespaceList
  | PodList
  | DeploymentList
  | StatefulSetList
  | DaemonSetList
  | ServiceList
  | ConfigMapList;
