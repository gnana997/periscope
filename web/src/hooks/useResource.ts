import { useMutation, useQuery, type UseQueryResult } from "@tanstack/react-query";
import { api, type ClusterScopedKind, type OpenAPIDoc, type ResourceMeta, type WatchStreamKind, type YamlKind } from "../lib/api";
import { useIsWatchStreamEnabled } from "../lib/features";
import { queryKeys } from "../lib/queryKeys";
import { editorYamlQueryKey, type EditorSource } from "../lib/customResources";
import {
  useChildPodWatch,
  useResourceStream,
  type StreamStatus,
} from "./useResourceStream";
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
  HPADetail,
  PDBDetail,
  ReplicaSetDetail,
  NetworkPolicyDetail,
  EndpointSliceDetail,
  IngressClassDetail,
  ResourceQuota,
  LimitRangeDetail,
  PriorityClassDetail,
  RuntimeClassDetail,
} from "../lib/types";

interface ResourceQueryArgs {
  cluster: string | undefined;
  resource: ResourceKind;
  namespace?: string;
}

// Per-kind background-refresh cadence. Used for kinds that don't have
// a watch SSE route AND for streaming kinds that have fallen back to
// polling after repeated reconnect failures. Kinds not listed here
// stay refresh-on-demand.
//
// The streaming kinds keep their entries here on purpose — they're the
// polling fallback path when useResourceStream surfaces
// streamStatus="polling_fallback". Cadences trade churn vs. apiserver
// load: pods/events at 15s match the live cluster signal users expect
// during incidents; workload controllers at 30s reflect their slower
// change rate.
const LIST_REFETCH_INTERVAL: Partial<Record<ResourceKind, number>> = {
  pods: 15_000,
  events: 15_000,
  deployments: 30_000,
  statefulsets: 30_000,
  daemonsets: 30_000,
  replicasets: 30_000,
  jobs: 30_000,
  cronjobs: 30_000,
  horizontalpodautoscalers: 30_000,
  poddisruptionbudgets: 30_000,
  // Networking — services and ingresses are mostly stable; endpointslices
  // churn rapidly during rollouts (one delta per pod-readiness flip),
  // which matches pods/events at 15s when the stream falls back.
  services: 30_000,
  ingresses: 30_000,
  networkpolicies: 30_000,
  endpointslices: 15_000,
  ingressclasses: 60_000,
  // Storage — PVCs follow pod lifecycle (Bound/Pending transitions on
  // workload startup), so 30s polling. PVs/StorageClasses are largely
  // static cluster admin objects; 60s is fine.
  pvs: 60_000,
  pvcs: 30_000,
  storageclasses: 60_000,
  // Cluster admin — Nodes change on autoscaling/maintenance windows
  // (poll at 30s as a meaningful fallback during cluster ops);
  // Namespaces and PriorityClasses/RuntimeClasses are essentially
  // static (60s is plenty when the stream is down).
  nodes: 30_000,
  namespaces: 60_000,
  priorityclasses: 60_000,
  runtimeclasses: 60_000,
};

// WATCH_STREAM_KINDS mirrors the WatchStreamKind union; lifted here so
// useResource can branch on it without referencing api.ts internals.
const WATCH_STREAM_KINDS: ReadonlyArray<ResourceKind> = [
  "pods",
  "events",
  "deployments",
  "statefulsets",
  "daemonsets",
  "replicasets",
  "jobs",
  "cronjobs",
  "horizontalpodautoscalers",
  "poddisruptionbudgets",
  "services",
  "ingresses",
  "networkpolicies",
  "endpointslices",
  "ingressclasses",
  "pvs",
  "pvcs",
  "storageclasses",
  "nodes",
  "namespaces",
  "priorityclasses",
  "runtimeclasses",
];

function isWatchStreamKind(k: ResourceKind): k is WatchStreamKind {
  return (WATCH_STREAM_KINDS as ResourceKind[]).includes(k);
}

// Extension of UseQueryResult adding streamStatus for kinds that have
// a watch SSE feed enabled. undefined for everything else (polling
// kinds, or streaming kinds while the feature flag is loading) — the
// StreamHealthBadge renders nothing for undefined.
export type ResourceQueryResult = UseQueryResult<ResourceListResponse> & {
  streamStatus?: StreamStatus;
};

export function useResource({
  cluster,
  resource,
  namespace,
}: ResourceQueryArgs): ResourceQueryResult {
  // Hook order is fixed; isWatchStreamKind is a pure runtime check, not
  // a hook gate. Pass a deterministic fallback "pods" to the stream
  // hook for non-watch kinds — it short-circuits internally on
  // enabled=false so the stream never opens.
  const watchKind: WatchStreamKind = isWatchStreamKind(resource) ? resource : "pods";
  const featureEnabled = useIsWatchStreamEnabled(watchKind);
  const stream = useResourceStream({
    cluster,
    resource: watchKind,
    namespace,
    enabled: isWatchStreamKind(resource) && featureEnabled,
  });

  // useStreaming = the SPA is currently relying on the SSE stream for
  // updates. When the stream falls back to polling, we flip enabled +
  // refetchInterval back on so the standard list endpoint takes over.
  const useStreaming =
    isWatchStreamKind(resource) &&
    featureEnabled &&
    stream.streamStatus !== "polling_fallback";

  const query = useQuery<ResourceListResponse>({
    queryKey: queryKeys.cluster(cluster ?? "").kind(resource).list(namespace ?? ""),
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
        case "horizontalpodautoscalers":
          return api.horizontalPodAutoscalers(cluster!, namespace, signal);
        case "poddisruptionbudgets":
          return api.podDisruptionBudgets(cluster!, namespace, signal);
        case "replicasets":
          return api.replicaSets(cluster!, namespace, signal);
        case "networkpolicies":
          return api.networkPolicies(cluster!, namespace, signal);
        case "endpointslices":
          return api.endpointSlices(cluster!, namespace, signal);
        case "ingressclasses":
          return api.ingressClasses(cluster!, signal);
        case "resourcequotas":
          return api.resourceQuotas(cluster!, namespace, signal);
        case "limitranges":
          return api.limitRanges(cluster!, namespace, signal);
        case "priorityclasses":
          return api.priorityClasses(cluster!, signal);
        case "runtimeclasses":
          return api.runtimeClasses(cluster!, signal);
        default:
          throw new Error(`Unknown resource kind: ${resource}`);
      }
    },
    enabled: Boolean(cluster) && !useStreaming,
    refetchInterval: useStreaming ? false : (LIST_REFETCH_INTERVAL[resource] ?? false),
    refetchIntervalInBackground: false,
  });

  // streamStatus surfaces only when the kind is a watch kind AND the
  // server has it enabled. Polling-only kinds (deployments, services,
  // configmaps, …) get undefined — the StreamHealthBadge renders nothing.
  return Object.assign(query, {
    streamStatus:
      isWatchStreamKind(resource) && featureEnabled
        ? stream.streamStatus
        : undefined,
  }) as ResourceQueryResult;
}

// --- Detail fetchers (lazy: only run when enabled by the caller) ---

export function usePodDetail(cluster: string, ns: string, name: string | null) {
  return useQuery<PodDetail>({
    queryKey: queryKeys.cluster(cluster).kind("pods").detail(ns, name ?? ""),
    queryFn: ({ signal }) => api.getPod(cluster, ns, name!, signal),
    enabled: Boolean(name),
  });
}

export function useDeploymentDetail(cluster: string, ns: string, name: string | null) {
  // Auxiliary Pod stream keeps the embedded child-pods table fresh
  // while the panel is open: pod state transitions don't touch the
  // Deployment object's RV, so the deployments stream alone can't see
  // them. See useChildPodWatch.
  useChildPodWatch(cluster, ns, Boolean(name));
  return useQuery<DeploymentDetail>({
    queryKey: queryKeys.cluster(cluster).kind("deployments").detail(ns, name ?? ""),
    queryFn: ({ signal }) => api.getDeployment(cluster, ns, name!, signal),
    enabled: Boolean(name),
  });
}

export function useStatefulSetDetail(cluster: string, ns: string, name: string | null) {
  useChildPodWatch(cluster, ns, Boolean(name));
  return useQuery<StatefulSetDetail>({
    queryKey: queryKeys.cluster(cluster).kind("statefulsets").detail(ns, name ?? ""),
    queryFn: ({ signal }) => api.getStatefulSet(cluster, ns, name!, signal),
    enabled: Boolean(name),
  });
}

export function useDaemonSetDetail(cluster: string, ns: string, name: string | null) {
  useChildPodWatch(cluster, ns, Boolean(name));
  return useQuery<DaemonSetDetail>({
    queryKey: queryKeys.cluster(cluster).kind("daemonsets").detail(ns, name ?? ""),
    queryFn: ({ signal }) => api.getDaemonSet(cluster, ns, name!, signal),
    enabled: Boolean(name),
  });
}

export function useServiceDetail(cluster: string, ns: string, name: string | null) {
  // Service detail embeds selected-by-selector pods; pod transitions
  // need to flow into the embedded table the same as workload
  // controllers above.
  useChildPodWatch(cluster, ns, Boolean(name));
  return useQuery<ServiceDetail>({
    queryKey: queryKeys.cluster(cluster).kind("services").detail(ns, name ?? ""),
    queryFn: ({ signal }) => api.getService(cluster, ns, name!, signal),
    enabled: Boolean(name),
  });
}

export function useIngressDetail(cluster: string, ns: string, name: string | null) {
  return useQuery<IngressDetail>({
    queryKey: queryKeys.cluster(cluster).kind("ingresses").detail(ns, name ?? ""),
    queryFn: ({ signal }) => api.getIngress(cluster, ns, name!, signal),
    enabled: Boolean(name),
  });
}

export function useConfigMapDetail(cluster: string, ns: string, name: string | null) {
  return useQuery<ConfigMapDetail>({
    queryKey: queryKeys.cluster(cluster).kind("configmaps").detail(ns, name ?? ""),
    queryFn: ({ signal }) => api.getConfigMap(cluster, ns, name!, signal),
    enabled: Boolean(name),
  });
}

export function useSecretDetail(cluster: string, ns: string, name: string | null) {
  return useQuery<SecretDetail>({
    queryKey: queryKeys.cluster(cluster).kind("secrets").detail(ns, name ?? ""),
    queryFn: ({ signal }) => api.getSecret(cluster, ns, name!, signal),
    enabled: Boolean(name),
  });
}

export function useJobDetail(cluster: string, ns: string, name: string | null) {
  useChildPodWatch(cluster, ns, Boolean(name));
  return useQuery<JobDetail>({
    queryKey: queryKeys.cluster(cluster).kind("jobs").detail(ns, name ?? ""),
    queryFn: ({ signal }) => api.getJob(cluster, ns, name!, signal),
    enabled: Boolean(name),
  });
}

export function useCronJobDetail(cluster: string, ns: string, name: string | null) {
  // CronJob detail surfaces recent Jobs (which surface child pods);
  // pod-driven invalidation keeps the panel current without depending
  // on the cronjob's own RV ticking.
  useChildPodWatch(cluster, ns, Boolean(name));
  return useQuery<CronJobDetail>({
    queryKey: queryKeys.cluster(cluster).kind("cronjobs").detail(ns, name ?? ""),
    queryFn: ({ signal }) => api.getCronJob(cluster, ns, name!, signal),
    enabled: Boolean(name),
  });
}

export function useClusterEvents(cluster: string, namespace?: string) {
  // Thin wrapper around useResource so EventsPage benefits from the
  // same drop-in streaming dispatch as Pods/Jobs/ReplicaSets. Type-
  // asserts data back to ClusterEventList — for events that's exactly
  // what the underlying ResourceListResponse union narrows to.
  const result = useResource({
    cluster,
    resource: "events",
    namespace,
  });
  return {
    ...result,
    data: result.data as ClusterEventList | undefined,
  };
}

export function usePVCDetail(cluster: string, ns: string, name: string | null) {
  return useQuery<PVCDetail>({
    queryKey: queryKeys.cluster(cluster).kind("pvcs").detail(ns, name ?? ""),
    queryFn: ({ signal }) => api.getPVC(cluster, ns, name!, signal),
    enabled: Boolean(name),
  });
}

export function usePVDetail(cluster: string, name: string | null) {
  return useQuery<PVDetail>({
    queryKey: queryKeys.cluster(cluster).kind("pvs").detail("", name ?? ""),
    queryFn: ({ signal }) => api.getPV(cluster, name!, signal),
    enabled: Boolean(name),
  });
}

export function useStorageClassDetail(cluster: string, name: string | null) {
  return useQuery<StorageClassDetail>({
    queryKey: queryKeys.cluster(cluster).kind("storageclasses").detail("", name ?? ""),
    queryFn: ({ signal }) => api.getStorageClass(cluster, name!, signal),
    enabled: Boolean(name),
  });
}

export function useNamespaceDetail(cluster: string, name: string | null) {
  return useQuery<NamespaceDetail>({
    queryKey: queryKeys.cluster(cluster).kind("namespaces").detail("", name ?? ""),
    queryFn: ({ signal }) => api.getNamespace(cluster, name!, signal),
    enabled: Boolean(name),
  });
}

export function useNodeDetail(cluster: string, name: string | null) {
  return useQuery<NodeDetail>({
    queryKey: queryKeys.cluster(cluster).kind("nodes").detail("", name ?? ""),
    queryFn: ({ signal }) => api.getNode(cluster, name!, signal),
    enabled: Boolean(name),
  });
}

export function useNodeMetrics(cluster: string, name: string | null) {
  return useQuery<NodeMetrics>({
    queryKey: queryKeys.cluster(cluster).kind("nodes").metrics("", name ?? ""),
    queryFn: ({ signal }) => api.getNodeMetrics(cluster, name!, signal),
    enabled: Boolean(name),
    refetchInterval: 30_000,
  });
}

export function usePodMetrics(cluster: string, ns: string, name: string | null) {
  return useQuery<PodMetrics>({
    queryKey: queryKeys.cluster(cluster).kind("pods").metrics(ns, name ?? ""),
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


// --- Resource meta (managedFields + resourceVersion + generation) ---

export function useResourceMeta(
  cluster: string,
  resource: { group: string; version: string; resource: string; namespace?: string; name: string } | null,
  enabled: boolean,
) {
  // Cache key nests under .kind(resource.resource) — i.e. the URL
  // plural — for both built-ins and CRs. This means a single prefix
  // invalidation via queryKeys.cluster(c).kind(plural).all sweeps
  // meta along with list/detail/yaml/events for that resource type.
  // The plural-namespace risk between built-ins and CRDs is low (no
  // real-world overlap in the K8s/cert-manager/argo/flux ecosystem),
  // and the alternative — branching on whether the resource is a CR
  // here — would require threading the EditorSource through every
  // call site for marginal benefit.
  return useQuery<ResourceMeta>({
    queryKey: queryKeys
      .cluster(cluster)
      .kind(resource?.resource ?? "")
      .meta(resource?.namespace ?? "", resource?.name ?? ""),
    queryFn: ({ signal }) =>
      api.getMeta(
        {
          cluster,
          group: resource!.group,
          version: resource!.version,
          resource: resource!.resource,
          namespace: resource!.namespace,
          name: resource!.name,
        },
        signal,
      ),
    enabled: enabled && Boolean(resource && resource.name),
    // managedFields changes constantly under controllers — refetch on
    // each editor open. Don't auto-refetch on focus (already paying the
    // round trip on remount).
    staleTime: 0,
    refetchOnWindowFocus: false,
    // Phase 3 drift detection: poll every 15s while the editor is
    // open. react-query pauses the interval automatically when the tab
    // is hidden (refetchIntervalInBackground default false), so this
    // costs nothing when no eyes are on it.
    refetchInterval: enabled ? 15_000 : false,
    refetchIntervalInBackground: false,
  });
}

// --- OpenAPI v3 schema (per group/version) ---

export function useOpenAPISchema(
  cluster: string,
  group: string,
  version: string,
  enabled: boolean,
) {
  return useQuery<OpenAPIDoc>({
    queryKey: queryKeys.cluster(cluster).openapi(group, version),
    queryFn: ({ signal }) => api.getOpenAPISchema(cluster, group, version, signal),
    enabled: enabled && Boolean(cluster && version),
    // Schemas only change on cluster upgrade — and a backend restart
    // flushes the server-side cache too. Cache forever client-side.
    staleTime: Infinity,
    gcTime: Infinity,
    refetchOnWindowFocus: false,
  });
}
// --- Cluster overview ---

export function useClusterSummary(cluster: string) {
  return useQuery<ClusterSummary>({
    queryKey: queryKeys.cluster(cluster).summary(),
    queryFn: ({ signal }) => api.getClusterSummary(cluster, signal),
    refetchInterval: 30_000,
  });
}

// --- RBAC detail hooks ---

export function useRoleDetail(cluster: string, ns: string, name: string | null) {
  return useQuery<RoleDetail>({
    queryKey: queryKeys.cluster(cluster).kind("roles").detail(ns, name ?? ""),
    queryFn: ({ signal }) => api.getRoles(cluster, ns, name!, signal),
    enabled: Boolean(name),
  });
}

export function useClusterRoleDetail(cluster: string, name: string | null) {
  return useQuery<ClusterRoleDetail>({
    queryKey: queryKeys.cluster(cluster).kind("clusterroles").detail("", name ?? ""),
    queryFn: ({ signal }) => api.getClusterRole(cluster, name!, signal),
    enabled: Boolean(name),
  });
}

export function useRoleBindingDetail(cluster: string, ns: string, name: string | null) {
  return useQuery<RoleBindingDetail>({
    queryKey: queryKeys.cluster(cluster).kind("rolebindings").detail(ns, name ?? ""),
    queryFn: ({ signal }) => api.getRoleBinding(cluster, ns, name!, signal),
    enabled: Boolean(name),
  });
}

export function useClusterRoleBindingDetail(cluster: string, name: string | null) {
  return useQuery<ClusterRoleBindingDetail>({
    queryKey: queryKeys.cluster(cluster).kind("clusterrolebindings").detail("", name ?? ""),
    queryFn: ({ signal }) => api.getClusterRoleBinding(cluster, name!, signal),
    enabled: Boolean(name),
  });
}

export function useServiceAccountDetail(cluster: string, ns: string, name: string | null) {
  return useQuery<ServiceAccountDetail>({
    queryKey: queryKeys.cluster(cluster).kind("serviceaccounts").detail(ns, name ?? ""),
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
    queryKey: queryKeys.cluster(cluster).kind(kind).events(ns, name ?? ""),
    queryFn: ({ signal }) =>
      (["namespaces", "pvs", "storageclasses", "clusterroles", "clusterrolebindings", "ingressclasses", "priorityclasses", "runtimeclasses"] as ClusterScopedKind[]).includes(kind as ClusterScopedKind)
        ? api.clusterScopedEvents(cluster, kind as ClusterScopedKind, name!, signal)
        : api.events(cluster, kind as Exclude<YamlKind, ClusterScopedKind>, ns, name!, signal),
    enabled: enabled && Boolean(name),
  });
}

// --- Extras detail hooks ---

export function useHPADetail(cluster: string, ns: string, name: string | null) {
  return useQuery<HPADetail>({
    queryKey: queryKeys.cluster(cluster).kind("horizontalpodautoscalers").detail(ns, name ?? ""),
    queryFn: ({ signal }) => api.getHPA(cluster, ns, name!, signal),
    enabled: Boolean(name),
  });
}

export function usePDBDetail(cluster: string, ns: string, name: string | null) {
  return useQuery<PDBDetail>({
    queryKey: queryKeys.cluster(cluster).kind("poddisruptionbudgets").detail(ns, name ?? ""),
    queryFn: ({ signal }) => api.getPDB(cluster, ns, name!, signal),
    enabled: Boolean(name),
  });
}

export function useReplicaSetDetail(cluster: string, ns: string, name: string | null) {
  useChildPodWatch(cluster, ns, Boolean(name));
  return useQuery<ReplicaSetDetail>({
    queryKey: queryKeys.cluster(cluster).kind("replicasets").detail(ns, name ?? ""),
    queryFn: ({ signal }) => api.getReplicaSet(cluster, ns, name!, signal),
    enabled: Boolean(name),
  });
}

export function useNetworkPolicyDetail(cluster: string, ns: string, name: string | null) {
  return useQuery<NetworkPolicyDetail>({
    queryKey: queryKeys.cluster(cluster).kind("networkpolicies").detail(ns, name ?? ""),
    queryFn: ({ signal }) => api.getNetworkPolicy(cluster, ns, name!, signal),
    enabled: Boolean(name),
  });
}

export function useEndpointSliceDetail(cluster: string, ns: string, name: string | null) {
  return useQuery<EndpointSliceDetail>({
    queryKey: queryKeys.cluster(cluster).kind("endpointslices").detail(ns, name ?? ""),
    queryFn: ({ signal }) => api.getEndpointSlice(cluster, ns, name!, signal),
    enabled: Boolean(name),
  });
}

export function useIngressClassDetail(cluster: string, name: string | null) {
  return useQuery<IngressClassDetail>({
    queryKey: queryKeys.cluster(cluster).kind("ingressclasses").detail("", name ?? ""),
    queryFn: ({ signal }) => api.getIngressClass(cluster, name!, signal),
    enabled: Boolean(name),
  });
}

export function useResourceQuotaDetail(cluster: string, ns: string, name: string | null) {
  return useQuery<ResourceQuota>({
    queryKey: queryKeys.cluster(cluster).kind("resourcequotas").detail(ns, name ?? ""),
    queryFn: ({ signal }) => api.getResourceQuota(cluster, ns, name!, signal),
    enabled: Boolean(name),
  });
}

export function useLimitRangeDetail(cluster: string, ns: string, name: string | null) {
  return useQuery<LimitRangeDetail>({
    queryKey: queryKeys.cluster(cluster).kind("limitranges").detail(ns, name ?? ""),
    queryFn: ({ signal }) => api.getLimitRange(cluster, ns, name!, signal),
    enabled: Boolean(name),
  });
}

export function usePriorityClassDetail(cluster: string, name: string | null) {
  return useQuery<PriorityClassDetail>({
    queryKey: queryKeys.cluster(cluster).kind("priorityclasses").detail("", name ?? ""),
    queryFn: ({ signal }) => api.getPriorityClass(cluster, name!, signal),
    enabled: Boolean(name),
  });
}

export function useRuntimeClassDetail(cluster: string, name: string | null) {
  return useQuery<RuntimeClassDetail>({
    queryKey: queryKeys.cluster(cluster).kind("runtimeclasses").detail("", name ?? ""),
    queryFn: ({ signal }) => api.getRuntimeClass(cluster, name!, signal),
    enabled: Boolean(name),
  });
}

// --- CRDs + custom resources ---

export function useCRDs(cluster: string, enabled = true) {
  return useQuery({
    queryKey: queryKeys.cluster(cluster).crds(),
    queryFn: ({ signal }) => api.crds(cluster, signal),
    // CRDs change infrequently — cache hit is the common case.
    staleTime: 30_000,
    enabled: enabled && cluster.length > 0,
  });
}

export function useCustomResources(
  cluster: string,
  group: string,
  version: string,
  plural: string,
  namespace?: string,
) {
  return useQuery({
    queryKey: queryKeys.cluster(cluster).cr(group, version, plural).list(namespace ?? ""),
    queryFn: ({ signal }) =>
      api.customResources(cluster, group, version, plural, namespace, signal),
    enabled: Boolean(group && version && plural),
  });
}

export function useCustomResourceDetail(
  cluster: string,
  group: string,
  version: string,
  plural: string,
  namespace: string | null,
  name: string | null,
) {
  return useQuery({
    queryKey: queryKeys
      .cluster(cluster)
      .cr(group, version, plural)
      .detail(namespace ?? "", name ?? ""),
    queryFn: ({ signal }) =>
      api.getCustomResource(cluster, group, version, plural, namespace, name!, signal),
    enabled: Boolean(name && group && version && plural),
  });
}

// --- Editor YAML (built-in or custom resource) ---

// useEditorYaml fetches a resource's YAML for the editor + read view, used by
// inline editor surface (YamlEditor + DriftDiffOverlay). For built-in
// kinds it shares the same `["yaml", cluster, yamlKind, ns, name]`
// cache key as the editor invalidations target, so the read-only YamlView stays on one cache;
// for CRs it uses `["yaml-cr", cluster, group, version, plural, ns,
// name]` — fully segregated so plural collisions across CRDs are
// impossible.
export function useEditorYaml(
  source: EditorSource,
  cluster: string,
  ns: string,
  name: string | null,
  enabled: boolean,
) {
  return useQuery<string>({
    queryKey: editorYamlQueryKey(source, cluster, ns, name ?? ""),
    queryFn: ({ signal }) => {
      if (source.kind === "custom") {
        return api.getCustomResourceYAML(
          cluster,
          source.cr.group,
          source.cr.version,
          source.cr.resource,
          ns && ns.length > 0 ? ns : null,
          name!,
          signal,
        );
      }
      const k = source.yamlKind;
      const isClusterScoped = (
        [
          "namespaces",
          "pvs",
          "storageclasses",
          "clusterroles",
          "clusterrolebindings",
          "ingressclasses",
          "priorityclasses",
          "runtimeclasses",
        ] as ClusterScopedKind[]
      ).includes(k as ClusterScopedKind);
      return isClusterScoped
        ? api.clusterScopedYaml(cluster, k as ClusterScopedKind, name!, signal)
        : api.yaml(
            cluster,
            k as Exclude<YamlKind, ClusterScopedKind>,
            ns,
            name!,
            signal,
          );
    },
    enabled: enabled && Boolean(name),
  });
}
