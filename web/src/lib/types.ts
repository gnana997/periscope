/**
 * DTO types matching the backend Periscope API responses.
 * Source of truth: internal/k8s/types.go and internal/clusters/cluster.go.
 * Keep in sync manually for v1; codegen later if drift becomes painful.
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

export interface Namespace {
  name: string;
  phase: string;
  createdAt: string;
}

export interface NamespaceList {
  namespaces: Namespace[];
}

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

export interface ConfigMap {
  name: string;
  namespace: string;
  keyCount: number;
  createdAt: string;
}

export interface ConfigMapList {
  configMaps: ConfigMap[];
}

export type ResourceKind =
  | "namespaces"
  | "pods"
  | "deployments"
  | "services"
  | "configmaps";

export type ResourceListResponse =
  | NamespaceList
  | PodList
  | DeploymentList
  | ServiceList
  | ConfigMapList;
