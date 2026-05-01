import { useMutation, useQuery } from "@tanstack/react-query";
import { api, type ClusterScopedKind, type YamlKind } from "../lib/api";
import type {
  ClusterEventList,
  ClusterRoleBindingDetail,
  ClusterSummary,
  ClusterRoleDetail,
  ConfigMapDetail,
  CronJobDetail,
  DaemonSetDetail,
  DeploymentDetail,
  EventList,
  IngressDetail,
  JobDetail,
  NamespaceDetail,
  NodeDetail,
  NodeMetrics,
  PodDetail,
  PodMetrics,
  PVCDetail,
  PVDetail,
  ResourceKind,
  ResourceListResponse,
  RoleBindingDetail,
  RoleDetail,
  SecretDetail,
  ServiceAccountDetail,
  ServiceDetail,
  StatefulSetDetail,
  StorageClassDetail,
} from "../lib/types";

interface ResourceQueryArgs {
  cluster: string | undefined;
  resource: ResourceKind;
  namespace?: string;
}

export function useResource({ cluster, resource, namespace }: ResourceQueryArgs) {
  return useQuery<ResourceListResponse>({
    queryKey: ["resource", cluster, resource, namespace ?? ""],
    queryFn: ({ signal }): Promise<ResourceListResponse> => {
      switch (resource) {
        case "nodes":
          return api.nodes(cluster!, signal);
        case "namespaces":
          return api.namespaces(cluster!, signal);
        case "pods":
          return api.pods(cluster!, namespace, signal);
        case "deployments":
          return api.deployments(cluster!, namespace, signal);
        case "statefulsets":
          return api.statefulsets(cluster!, namespace, signal);
        case "daemonsets":
          return api.daemonsets(cluster!, namespace, signal);
        case "services":
          return api.services(cluster!, namespace, signal);
        case "ingresses":
          return api.ingresses(cluster!, namespace, signal);
        case "configmaps":
          return api.configmaps(cluster!, namespace, signal);
        case "secrets":
          return api.secrets(cluster!, namespace, signal);
        case "jobs":
          return api.jobs(cluster!, namespace, signal);
        case "cronjobs":
          return api.cronjobs(cluster!, namespace, signal);
        case "events":
          return api.clusterEvents(cluster!, namespace, signal);
        case "pvcs":
          return api.pvcs(cluster!, namespace, signal);
        case "pvs":
          return api.pvs(cluster!, signal);
        case "storageclasses":
          return api.storageClasses(cluster!, signal);
        case "roles":
          return api.roles(cluster!, namespace, signal);
        case "clusterroles":
          return api.clusterRoles(cluster!, signal);
        case "rolebindings":
          return api.roleBindings(cluster!, namespace, signal);
        case "clusterrolebindings":
          return api.clusterRoleBindings(cluster!, signal);
        case "serviceaccounts":
          return api.serviceAccounts(cluster!, namespace, signal);
        default:
          throw new Error(`Unknown resource kind: ${resource}`);
      }
    },
    enabled: Boolean(cluster),
  });
}

// --- Detail fetchers (lazy: only run when enabled by the caller) ---

export function usePodDetail(cluster: string, ns: string, name: string | null) {
  return useQuery<PodDetail>({
    queryKey: ["pod-detail", cluster, ns, name],
    queryFn: ({ signal }) => api.getPod(cluster, ns, name!, signal),
    enabled: Boolean(name),
  });
}

export function useDeploymentDetail(cluster: string, ns: string, name: string | null) {
  return useQuery<DeploymentDetail>({
    queryKey: ["deployment-detail", cluster, ns, name],
    queryFn: ({ signal }) => api.getDeployment(cluster, ns, name!, signal),
    enabled: Boolean(name),
  });
}

export function useStatefulSetDetail(cluster: string, ns: string, name: string | null) {
  return useQuery<StatefulSetDetail>({
    queryKey: ["statefulset-detail", cluster, ns, name],
    queryFn: ({ signal }) => api.getStatefulSet(cluster, ns, name!, signal),
    enabled: Boolean(name),
  });
}

export function useDaemonSetDetail(cluster: string, ns: string, name: string | null) {
  return useQuery<DaemonSetDetail>({
    queryKey: ["daemonset-detail", cluster, ns, name],
    queryFn: ({ signal }) => api.getDaemonSet(cluster, ns, name!, signal),
    enabled: Boolean(name),
  });
}

export function useServiceDetail(cluster: string, ns: string, name: string | null) {
  return useQuery<ServiceDetail>({
    queryKey: ["service-detail", cluster, ns, name],
    queryFn: ({ signal }) => api.getService(cluster, ns, name!, signal),
    enabled: Boolean(name),
  });
}

export function useIngressDetail(cluster: string, ns: string, name: string | null) {
  return useQuery<IngressDetail>({
    queryKey: ["ingress-detail", cluster, ns, name],
    queryFn: ({ signal }) => api.getIngress(cluster, ns, name!, signal),
    enabled: Boolean(name),
  });
}

export function useConfigMapDetail(cluster: string, ns: string, name: string | null) {
  return useQuery<ConfigMapDetail>({
    queryKey: ["configmap-detail", cluster, ns, name],
    queryFn: ({ signal }) => api.getConfigMap(cluster, ns, name!, signal),
    enabled: Boolean(name),
  });
}

export function useSecretDetail(cluster: string, ns: string, name: string | null) {
  return useQuery<SecretDetail>({
    queryKey: ["secret-detail", cluster, ns, name],
    queryFn: ({ signal }) => api.getSecret(cluster, ns, name!, signal),
    enabled: Boolean(name),
  });
}

export function useJobDetail(cluster: string, ns: string, name: string | null) {
  return useQuery<JobDetail>({
    queryKey: ["job-detail", cluster, ns, name],
    queryFn: ({ signal }) => api.getJob(cluster, ns, name!, signal),
    enabled: Boolean(name),
  });
}

export function useCronJobDetail(cluster: string, ns: string, name: string | null) {
  return useQuery<CronJobDetail>({
    queryKey: ["cronjob-detail", cluster, ns, name],
    queryFn: ({ signal }) => api.getCronJob(cluster, ns, name!, signal),
    enabled: Boolean(name),
  });
}

export function useClusterEvents(cluster: string, namespace?: string) {
  return useQuery<ClusterEventList>({
    queryKey: ["cluster-events", cluster, namespace ?? ""],
    queryFn: ({ signal }) => api.clusterEvents(cluster, namespace, signal),
    enabled: Boolean(cluster),
    refetchInterval: 15_000,
  });
}

export function usePVCDetail(cluster: string, ns: string, name: string | null) {
  return useQuery<PVCDetail>({
    queryKey: ["pvc-detail", cluster, ns, name],
    queryFn: ({ signal }) => api.getPVC(cluster, ns, name!, signal),
    enabled: Boolean(name),
  });
}

export function usePVDetail(cluster: string, name: string | null) {
  return useQuery<PVDetail>({
    queryKey: ["pv-detail", cluster, name],
    queryFn: ({ signal }) => api.getPV(cluster, name!, signal),
    enabled: Boolean(name),
  });
}

export function useStorageClassDetail(cluster: string, name: string | null) {
  return useQuery<StorageClassDetail>({
    queryKey: ["storageclass-detail", cluster, name],
    queryFn: ({ signal }) => api.getStorageClass(cluster, name!, signal),
    enabled: Boolean(name),
  });
}

export function useNamespaceDetail(cluster: string, name: string | null) {
  return useQuery<NamespaceDetail>({
    queryKey: ["namespace-detail", cluster, name],
    queryFn: ({ signal }) => api.getNamespace(cluster, name!, signal),
    enabled: Boolean(name),
  });
}

export function useNodeDetail(cluster: string, name: string | null) {
  return useQuery<NodeDetail>({
    queryKey: ["node-detail", cluster, name],
    queryFn: ({ signal }) => api.getNode(cluster, name!, signal),
    enabled: Boolean(name),
  });
}

export function useNodeMetrics(cluster: string, name: string | null) {
  return useQuery<NodeMetrics>({
    queryKey: ["node-metrics", cluster, name],
    queryFn: ({ signal }) => api.getNodeMetrics(cluster, name!, signal),
    enabled: Boolean(name),
    refetchInterval: 30_000,
  });
}

export function usePodMetrics(cluster: string, ns: string, name: string | null) {
  return useQuery<PodMetrics>({
    queryKey: ["pod-metrics", cluster, ns, name],
    queryFn: ({ signal }) => api.getPodMetrics(cluster, ns, name!, signal),
    enabled: Boolean(name),
    refetchInterval: 30_000,
  });
}

// --- Secret reveal — mutation, NOT a query.
// Modeled as a mutation so it only fires on explicit user action, never
// preloads or revalidates on focus. Each call audit-logs server-side.

export function useRevealSecretValue() {
  return useMutation({
    mutationFn: ({
      cluster,
      ns,
      name,
      key,
    }: {
      cluster: string;
      ns: string;
      name: string;
      key: string;
    }) => api.getSecretValue(cluster, ns, name, key),
  });
}

// --- YAML ---

export function useYaml(
  cluster: string,
  kind: YamlKind,
  ns: string,
  name: string | null,
  enabled: boolean,
) {
  return useQuery<string>({
    queryKey: ["yaml", cluster, kind, ns, name],
    queryFn: ({ signal }) =>
      (["namespaces", "pvs", "storageclasses", "clusterroles", "clusterrolebindings"] as ClusterScopedKind[]).includes(kind as ClusterScopedKind)
        ? api.clusterScopedYaml(cluster, kind as ClusterScopedKind, name!, signal)
        : api.yaml(cluster, kind as Exclude<YamlKind, ClusterScopedKind>, ns, name!, signal),
    enabled: enabled && Boolean(name),
  });
}

// --- Cluster overview ---

export function useClusterSummary(cluster: string) {
  return useQuery<ClusterSummary>({
    queryKey: ["cluster-summary", cluster],
    queryFn: ({ signal }) => api.getClusterSummary(cluster, signal),
    refetchInterval: 30_000,
  });
}

// --- RBAC detail hooks ---

export function useRoleDetail(cluster: string, ns: string, name: string | null) {
  return useQuery<RoleDetail>({
    queryKey: ["role-detail", cluster, ns, name],
    queryFn: ({ signal }) => api.getRoles(cluster, ns, name!, signal),
    enabled: Boolean(name),
  });
}

export function useClusterRoleDetail(cluster: string, name: string | null) {
  return useQuery<ClusterRoleDetail>({
    queryKey: ["clusterrole-detail", cluster, name],
    queryFn: ({ signal }) => api.getClusterRole(cluster, name!, signal),
    enabled: Boolean(name),
  });
}

export function useRoleBindingDetail(cluster: string, ns: string, name: string | null) {
  return useQuery<RoleBindingDetail>({
    queryKey: ["rolebinding-detail", cluster, ns, name],
    queryFn: ({ signal }) => api.getRoleBinding(cluster, ns, name!, signal),
    enabled: Boolean(name),
  });
}

export function useClusterRoleBindingDetail(cluster: string, name: string | null) {
  return useQuery<ClusterRoleBindingDetail>({
    queryKey: ["clusterrolebinding-detail", cluster, name],
    queryFn: ({ signal }) => api.getClusterRoleBinding(cluster, name!, signal),
    enabled: Boolean(name),
  });
}

export function useServiceAccountDetail(cluster: string, ns: string, name: string | null) {
  return useQuery<ServiceAccountDetail>({
    queryKey: ["serviceaccount-detail", cluster, ns, name],
    queryFn: ({ signal }) => api.getServiceAccount(cluster, ns, name!, signal),
    enabled: Boolean(name),
  });
}

// --- Events (per object) ---

export function useObjectEvents(
  cluster: string,
  kind: YamlKind,
  ns: string,
  name: string | null,
  enabled: boolean,
) {
  return useQuery<EventList>({
    queryKey: ["events", cluster, kind, ns, name],
    queryFn: ({ signal }) =>
      (["namespaces", "pvs", "storageclasses", "clusterroles", "clusterrolebindings"] as ClusterScopedKind[]).includes(kind as ClusterScopedKind)
        ? api.clusterScopedEvents(cluster, kind as ClusterScopedKind, name!, signal)
        : api.events(cluster, kind as Exclude<YamlKind, ClusterScopedKind>, ns, name!, signal),
    enabled: enabled && Boolean(name),
  });
}
