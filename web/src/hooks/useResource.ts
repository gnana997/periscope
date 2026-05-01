import { useQuery } from "@tanstack/react-query";
import { api, type YamlKind } from "../lib/api";
import type {
  ConfigMapDetail,
  DaemonSetDetail,
  DeploymentDetail,
  EventList,
  NamespaceDetail,
  PodDetail,
  ResourceKind,
  ResourceListResponse,
  ServiceDetail,
  StatefulSetDetail,
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
        case "configmaps":
          return api.configmaps(cluster!, namespace, signal);
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

export function useConfigMapDetail(cluster: string, ns: string, name: string | null) {
  return useQuery<ConfigMapDetail>({
    queryKey: ["configmap-detail", cluster, ns, name],
    queryFn: ({ signal }) => api.getConfigMap(cluster, ns, name!, signal),
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
      kind === "namespaces"
        ? api.namespaceYaml(cluster, name!, signal)
        : api.yaml(cluster, kind, ns, name!, signal),
    enabled: enabled && Boolean(name),
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
      kind === "namespaces"
        ? api.namespaceEvents(cluster, name!, signal)
        : api.events(cluster, kind, ns, name!, signal),
    enabled: enabled && Boolean(name),
  });
}
