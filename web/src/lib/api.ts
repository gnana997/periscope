import type {
  ClusterEventList,
  ClusterRoleBindingDetail,
  ClusterSummary,
  CRDList,
  CustomResourceDetail,
  CustomResourceList,
  SearchKind,
  SearchResultList,
  ClusterRoleBindingList,
  ClusterRoleDetail,
  ClusterRoleList,
  ClustersResponse,
  ConfigMapDetail,
  ConfigMapList,
  CronJobDetail,
  CronJobList,
  DaemonSetDetail,
  DaemonSetList,
  DeploymentDetail,
  DeploymentList,
  EventList,
  IngressDetail,
  IngressList,
  JobDetail,
  JobList,
  NamespaceDetail,
  NodeDetail,
  NodeList,
  NodeMetrics,
  PodMetrics,
  NamespaceList,
  PodDetail,
  PodList,
  PVCDetail,
  PVCList,
  PVDetail,
  PVList,
  RoleBindingDetail,
  RoleBindingList,
  RoleDetail,
  RoleList,
  SecretDetail,
  SecretList,
  ServiceAccountDetail,
  ServiceAccountList,
  ServiceDetail,
  ServiceList,
  StatefulSetDetail,
  StatefulSetList,
  StorageClassDetail,
  StorageClassList,
  Whoami,
  HPADetail,
  HPAList,
  PDBDetail,
  PDBList,
  ReplicaSetDetail,
  ReplicaSetList,
  NetworkPolicyDetail,
  NetworkPolicyList,
  IngressClassDetail,
  IngressClassList,
  ResourceQuota,
  ResourceQuotaList,
  LimitRangeDetail,
  LimitRangeList,
  PriorityClassDetail,
  PriorityClassList,
  RuntimeClassDetail,
  RuntimeClassList,
} from "./types";

class ApiError extends Error {
  status: number;
  bodyText?: string;

  constructor(message: string, status: number, bodyText?: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.bodyText = bodyText;
  }
}

async function getJSON<T>(path: string, signal?: AbortSignal): Promise<T> {
  const res = await fetch(path, {
    signal,
    headers: { Accept: "application/json" },
  });
  if (!res.ok) {
    const text = await res.text().catch(() => "");
    throw new ApiError(
      `${res.status} ${res.statusText} on ${path}`,
      res.status,
      text,
    );
  }
  return (await res.json()) as T;
}

async function getText(path: string, signal?: AbortSignal): Promise<string> {
  const res = await fetch(path, { signal });
  if (!res.ok) {
    const text = await res.text().catch(() => "");
    throw new ApiError(
      `${res.status} ${res.statusText} on ${path}`,
      res.status,
      text,
    );
  }
  return await res.text();
}

const enc = encodeURIComponent;

function nsURL(c: string, kind: string, ns: string, name: string, suffix?: string) {
  const base = `/api/clusters/${enc(c)}/${kind}/${enc(ns)}/${enc(name)}`;
  return suffix ? `${base}/${suffix}` : base;
}
function clusterScopedURL(c: string, kind: string, name: string, suffix?: string) {
  const base = `/api/clusters/${enc(c)}/${kind}/${enc(name)}`;
  return suffix ? `${base}/${suffix}` : base;
}

export type ClusterScopedKind = "namespaces" | "pvs" | "storageclasses" | "clusterroles" | "clusterrolebindings" | "ingressclasses" | "priorityclasses" | "runtimeclasses";

export type YamlKind =
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
  | "namespaces"
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
  | "runtimeclasses";

export const api = {
  whoami: (signal?: AbortSignal) => getJSON<Whoami>("/api/whoami", signal),

  getClusterSummary: (cluster: string, signal?: AbortSignal) =>
    getJSON<ClusterSummary>(`/api/clusters/${enc(cluster)}/dashboard`, signal),

  search: (
    cluster: string,
    query: string,
    opts?: { kinds?: SearchKind[]; limit?: number },
    signal?: AbortSignal,
  ) => {
    const params = new URLSearchParams({ q: query });
    if (opts?.kinds && opts.kinds.length > 0) {
      params.set("kinds", opts.kinds.join(","));
    }
    if (opts?.limit) params.set("limit", String(opts.limit));
    return getJSON<SearchResultList>(
      `/api/clusters/${enc(cluster)}/search?${params.toString()}`,
      signal,
    );
  },

  clusters: (signal?: AbortSignal) =>
    getJSON<ClustersResponse>("/api/clusters", signal),

  // --- CRDs + custom resources -------------------------------------

  crds: (cluster: string, signal?: AbortSignal) =>
    getJSON<CRDList>(`/api/clusters/${enc(cluster)}/crds`, signal),

  /** List custom resources of a given GVR. namespace is optional —
   *  empty/undefined means "all namespaces" for namespaced CRDs (the
   *  backend ignores it for cluster-scoped). */
  customResources: (
    cluster: string,
    group: string,
    version: string,
    plural: string,
    namespace?: string,
    signal?: AbortSignal,
  ) => {
    const base = `/api/clusters/${enc(cluster)}/customresources/${enc(group)}/${enc(version)}/${enc(plural)}`;
    const url = namespace ? `${base}?namespace=${enc(namespace)}` : base;
    return getJSON<CustomResourceList>(url, signal);
  },

  /** Backend uses "_" as the URL placeholder for cluster-scoped
   *  resources — see clusterScopedNamespacePlaceholder in main.go. */
  getCustomResource: (
    cluster: string,
    group: string,
    version: string,
    plural: string,
    namespace: string | null,
    name: string,
    signal?: AbortSignal,
  ) => {
    const ns = namespace && namespace.length > 0 ? namespace : "_";
    return getJSON<CustomResourceDetail>(
      `/api/clusters/${enc(cluster)}/customresources/${enc(group)}/${enc(version)}/${enc(plural)}/${enc(ns)}/${enc(name)}`,
      signal,
    );
  },

  getCustomResourceYAML: (
    cluster: string,
    group: string,
    version: string,
    plural: string,
    namespace: string | null,
    name: string,
    signal?: AbortSignal,
  ) => {
    const ns = namespace && namespace.length > 0 ? namespace : "_";
    return getText(
      `/api/clusters/${enc(cluster)}/customresources/${enc(group)}/${enc(version)}/${enc(plural)}/${enc(ns)}/${enc(name)}/yaml`,
      signal,
    );
  },

  // --- LIST ---

  nodes: (cluster: string, signal?: AbortSignal) =>
    getJSON<NodeList>(`/api/clusters/${enc(cluster)}/nodes`, signal),

  namespaces: (cluster: string, signal?: AbortSignal) =>
    getJSON<NamespaceList>(`/api/clusters/${enc(cluster)}/namespaces`, signal),

  pods: (cluster: string, namespace?: string, signal?: AbortSignal) => {
    const qs = namespace ? `?namespace=${enc(namespace)}` : "";
    return getJSON<PodList>(`/api/clusters/${enc(cluster)}/pods${qs}`, signal);
  },

  deployments: (cluster: string, namespace?: string, signal?: AbortSignal) => {
    const qs = namespace ? `?namespace=${enc(namespace)}` : "";
    return getJSON<DeploymentList>(`/api/clusters/${enc(cluster)}/deployments${qs}`, signal);
  },

  statefulsets: (cluster: string, namespace?: string, signal?: AbortSignal) => {
    const qs = namespace ? `?namespace=${enc(namespace)}` : "";
    return getJSON<StatefulSetList>(`/api/clusters/${enc(cluster)}/statefulsets${qs}`, signal);
  },

  daemonsets: (cluster: string, namespace?: string, signal?: AbortSignal) => {
    const qs = namespace ? `?namespace=${enc(namespace)}` : "";
    return getJSON<DaemonSetList>(`/api/clusters/${enc(cluster)}/daemonsets${qs}`, signal);
  },

  services: (cluster: string, namespace?: string, signal?: AbortSignal) => {
    const qs = namespace ? `?namespace=${enc(namespace)}` : "";
    return getJSON<ServiceList>(`/api/clusters/${enc(cluster)}/services${qs}`, signal);
  },

  ingresses: (cluster: string, namespace?: string, signal?: AbortSignal) => {
    const qs = namespace ? `?namespace=${enc(namespace)}` : "";
    return getJSON<IngressList>(`/api/clusters/${enc(cluster)}/ingresses${qs}`, signal);
  },

  configmaps: (cluster: string, namespace?: string, signal?: AbortSignal) => {
    const qs = namespace ? `?namespace=${enc(namespace)}` : "";
    return getJSON<ConfigMapList>(`/api/clusters/${enc(cluster)}/configmaps${qs}`, signal);
  },

  secrets: (cluster: string, namespace?: string, signal?: AbortSignal) => {
    const qs = namespace ? `?namespace=${enc(namespace)}` : "";
    return getJSON<SecretList>(`/api/clusters/${enc(cluster)}/secrets${qs}`, signal);
  },

  jobs: (cluster: string, namespace?: string, signal?: AbortSignal) => {
    const qs = namespace ? `?namespace=${enc(namespace)}` : "";
    return getJSON<JobList>(`/api/clusters/${enc(cluster)}/jobs${qs}`, signal);
  },

  cronjobs: (cluster: string, namespace?: string, signal?: AbortSignal) => {
    const qs = namespace ? `?namespace=${enc(namespace)}` : "";
    return getJSON<CronJobList>(`/api/clusters/${enc(cluster)}/cronjobs${qs}`, signal);
  },

  clusterEvents: (cluster: string, namespace?: string, signal?: AbortSignal) => {
    const qs = namespace ? `?namespace=${enc(namespace)}` : "";
    return getJSON<ClusterEventList>(`/api/clusters/${enc(cluster)}/events${qs}`, signal);
  },

  pvcs: (cluster: string, namespace?: string, signal?: AbortSignal) => {
    const qs = namespace ? `?namespace=${enc(namespace)}` : "";
    return getJSON<PVCList>(`/api/clusters/${enc(cluster)}/pvcs${qs}`, signal);
  },

  pvs: (cluster: string, signal?: AbortSignal) =>
    getJSON<PVList>(`/api/clusters/${enc(cluster)}/pvs`, signal),

  storageClasses: (cluster: string, signal?: AbortSignal) =>
    getJSON<StorageClassList>(`/api/clusters/${enc(cluster)}/storageclasses`, signal),

  // --- GET (detail) ---

  getPod: (c: string, ns: string, name: string, signal?: AbortSignal) =>
    getJSON<PodDetail>(nsURL(c, "pods", ns, name), signal),

  getDeployment: (c: string, ns: string, name: string, signal?: AbortSignal) =>
    getJSON<DeploymentDetail>(nsURL(c, "deployments", ns, name), signal),

  getStatefulSet: (c: string, ns: string, name: string, signal?: AbortSignal) =>
    getJSON<StatefulSetDetail>(nsURL(c, "statefulsets", ns, name), signal),

  getDaemonSet: (c: string, ns: string, name: string, signal?: AbortSignal) =>
    getJSON<DaemonSetDetail>(nsURL(c, "daemonsets", ns, name), signal),

  getService: (c: string, ns: string, name: string, signal?: AbortSignal) =>
    getJSON<ServiceDetail>(nsURL(c, "services", ns, name), signal),

  getIngress: (c: string, ns: string, name: string, signal?: AbortSignal) =>
    getJSON<IngressDetail>(nsURL(c, "ingresses", ns, name), signal),

  getConfigMap: (c: string, ns: string, name: string, signal?: AbortSignal) =>
    getJSON<ConfigMapDetail>(nsURL(c, "configmaps", ns, name), signal),

  getSecret: (c: string, ns: string, name: string, signal?: AbortSignal) =>
    getJSON<SecretDetail>(nsURL(c, "secrets", ns, name), signal),

  getJob: (c: string, ns: string, name: string, signal?: AbortSignal) =>
    getJSON<JobDetail>(nsURL(c, "jobs", ns, name), signal),

  getCronJob: (c: string, ns: string, name: string, signal?: AbortSignal) =>
    getJSON<CronJobDetail>(nsURL(c, "cronjobs", ns, name), signal),

  getNode: (c: string, name: string, signal?: AbortSignal) =>
    getJSON<NodeDetail>(clusterScopedURL(c, "nodes", name), signal),

  getNodeMetrics: (c: string, name: string, signal?: AbortSignal) =>
    getJSON<NodeMetrics>(clusterScopedURL(c, "nodes", name, "metrics"), signal),

  getPodMetrics: (c: string, ns: string, name: string, signal?: AbortSignal) =>
    getJSON<PodMetrics>(nsURL(c, "pods", ns, name, "metrics"), signal),

  getNamespace: (c: string, name: string, signal?: AbortSignal) =>
    getJSON<NamespaceDetail>(clusterScopedURL(c, "namespaces", name), signal),

  getPVC: (c: string, ns: string, name: string, signal?: AbortSignal) =>
    getJSON<PVCDetail>(nsURL(c, "pvcs", ns, name), signal),

  getPV: (c: string, name: string, signal?: AbortSignal) =>
    getJSON<PVDetail>(clusterScopedURL(c, "pvs", name), signal),

  getStorageClass: (c: string, name: string, signal?: AbortSignal) =>
    getJSON<StorageClassDetail>(clusterScopedURL(c, "storageclasses", name), signal),

  roles: (cluster: string, namespace?: string, signal?: AbortSignal) => {
    const qs = namespace ? `?namespace=${enc(namespace)}` : "";
    return getJSON<RoleList>(`/api/clusters/${enc(cluster)}/roles${qs}`, signal);
  },

  getRoles: (c: string, ns: string, name: string, signal?: AbortSignal) =>
    getJSON<RoleDetail>(nsURL(c, "roles", ns, name), signal),

  clusterRoles: (cluster: string, signal?: AbortSignal) =>
    getJSON<ClusterRoleList>(`/api/clusters/${enc(cluster)}/clusterroles`, signal),

  getClusterRole: (c: string, name: string, signal?: AbortSignal) =>
    getJSON<ClusterRoleDetail>(clusterScopedURL(c, "clusterroles", name), signal),

  roleBindings: (cluster: string, namespace?: string, signal?: AbortSignal) => {
    const qs = namespace ? `?namespace=${enc(namespace)}` : "";
    return getJSON<RoleBindingList>(`/api/clusters/${enc(cluster)}/rolebindings${qs}`, signal);
  },

  getRoleBinding: (c: string, ns: string, name: string, signal?: AbortSignal) =>
    getJSON<RoleBindingDetail>(nsURL(c, "rolebindings", ns, name), signal),

  clusterRoleBindings: (cluster: string, signal?: AbortSignal) =>
    getJSON<ClusterRoleBindingList>(`/api/clusters/${enc(cluster)}/clusterrolebindings`, signal),

  getClusterRoleBinding: (c: string, name: string, signal?: AbortSignal) =>
    getJSON<ClusterRoleBindingDetail>(clusterScopedURL(c, "clusterrolebindings", name), signal),

  serviceAccounts: (cluster: string, namespace?: string, signal?: AbortSignal) => {
    const qs = namespace ? `?namespace=${enc(namespace)}` : "";
    return getJSON<ServiceAccountList>(`/api/clusters/${enc(cluster)}/serviceaccounts${qs}`, signal);
  },

  getServiceAccount: (c: string, ns: string, name: string, signal?: AbortSignal) =>
    getJSON<ServiceAccountDetail>(nsURL(c, "serviceaccounts", ns, name), signal),

  // --- Secret reveal: per-key value, audit-logged server-side. ---
  // Fetched on user click only. Never as part of any other endpoint.

  getSecretValue: (
    c: string,
    ns: string,
    name: string,
    key: string,
    signal?: AbortSignal,
  ) =>
    getText(
      `/api/clusters/${enc(c)}/secrets/${enc(ns)}/${enc(name)}/data/${enc(key)}`,
      signal,
    ),

  // --- YAML ---

  yaml: (
    c: string,
    kind: Exclude<YamlKind, ClusterScopedKind>,
    ns: string,
    name: string,
    signal?: AbortSignal,
  ) => getText(nsURL(c, kind, ns, name, "yaml"), signal),

  namespaceYaml: (c: string, name: string, signal?: AbortSignal) =>
    getText(clusterScopedURL(c, "namespaces", name, "yaml"), signal),

  clusterScopedYaml: (c: string, kind: ClusterScopedKind, name: string, signal?: AbortSignal) =>
    getText(clusterScopedURL(c, kind, name, "yaml"), signal),

  // --- Events ---

  events: (
    c: string,
    kind: Exclude<YamlKind, ClusterScopedKind>,
    ns: string,
    name: string,
    signal?: AbortSignal,
  ) => getJSON<EventList>(nsURL(c, kind, ns, name, "events"), signal),

  namespaceEvents: (c: string, name: string, signal?: AbortSignal) =>
    getJSON<EventList>(clusterScopedURL(c, "namespaces", name, "events"), signal),

  clusterScopedEvents: (c: string, kind: ClusterScopedKind, name: string, signal?: AbortSignal) =>
    getJSON<EventList>(clusterScopedURL(c, kind, name, "events"), signal),

  // --- Extras ---
  horizontalPodAutoscalers: (cluster: string, namespace?: string, signal?: AbortSignal) => {
    const qs = namespace ? `?namespace=${enc(namespace)}` : "";
    return getJSON<HPAList>(`/api/clusters/${enc(cluster)}/horizontalpodautoscalers${qs}`, signal);
  },

  podDisruptionBudgets: (cluster: string, namespace?: string, signal?: AbortSignal) => {
    const qs = namespace ? `?namespace=${enc(namespace)}` : "";
    return getJSON<PDBList>(`/api/clusters/${enc(cluster)}/poddisruptionbudgets${qs}`, signal);
  },

  replicaSets: (cluster: string, namespace?: string, signal?: AbortSignal) => {
    const qs = namespace ? `?namespace=${enc(namespace)}` : "";
    return getJSON<ReplicaSetList>(`/api/clusters/${enc(cluster)}/replicasets${qs}`, signal);
  },

  networkPolicies: (cluster: string, namespace?: string, signal?: AbortSignal) => {
    const qs = namespace ? `?namespace=${enc(namespace)}` : "";
    return getJSON<NetworkPolicyList>(`/api/clusters/${enc(cluster)}/networkpolicies${qs}`, signal);
  },

  resourceQuotas: (cluster: string, namespace?: string, signal?: AbortSignal) => {
    const qs = namespace ? `?namespace=${enc(namespace)}` : "";
    return getJSON<ResourceQuotaList>(`/api/clusters/${enc(cluster)}/resourcequotas${qs}`, signal);
  },

  limitRanges: (cluster: string, namespace?: string, signal?: AbortSignal) => {
    const qs = namespace ? `?namespace=${enc(namespace)}` : "";
    return getJSON<LimitRangeList>(`/api/clusters/${enc(cluster)}/limitranges${qs}`, signal);
  },

  ingressClasses: (cluster: string, signal?: AbortSignal) =>
    getJSON<IngressClassList>(`/api/clusters/${enc(cluster)}/ingressclasses`, signal),

  priorityClasses: (cluster: string, signal?: AbortSignal) =>
    getJSON<PriorityClassList>(`/api/clusters/${enc(cluster)}/priorityclasses`, signal),

  runtimeClasses: (cluster: string, signal?: AbortSignal) =>
    getJSON<RuntimeClassList>(`/api/clusters/${enc(cluster)}/runtimeclasses`, signal),


  // --- Extras detail ---
  getHPA: (c: string, ns: string, name: string, signal?: AbortSignal) =>
    getJSON<HPADetail>(nsURL(c, "horizontalpodautoscalers", ns, name), signal),

  getPDB: (c: string, ns: string, name: string, signal?: AbortSignal) =>
    getJSON<PDBDetail>(nsURL(c, "poddisruptionbudgets", ns, name), signal),

  getReplicaSet: (c: string, ns: string, name: string, signal?: AbortSignal) =>
    getJSON<ReplicaSetDetail>(nsURL(c, "replicasets", ns, name), signal),

  getNetworkPolicy: (c: string, ns: string, name: string, signal?: AbortSignal) =>
    getJSON<NetworkPolicyDetail>(nsURL(c, "networkpolicies", ns, name), signal),

  getResourceQuota: (c: string, ns: string, name: string, signal?: AbortSignal) =>
    getJSON<ResourceQuota>(nsURL(c, "resourcequotas", ns, name), signal),

  getLimitRange: (c: string, ns: string, name: string, signal?: AbortSignal) =>
    getJSON<LimitRangeDetail>(nsURL(c, "limitranges", ns, name), signal),

  getIngressClass: (c: string, name: string, signal?: AbortSignal) =>
    getJSON<IngressClassDetail>(clusterScopedURL(c, "ingressclasses", name), signal),

  getPriorityClass: (c: string, name: string, signal?: AbortSignal) =>
    getJSON<PriorityClassDetail>(clusterScopedURL(c, "priorityclasses", name), signal),

  getRuntimeClass: (c: string, name: string, signal?: AbortSignal) =>
    getJSON<RuntimeClassDetail>(clusterScopedURL(c, "runtimeclasses", name), signal),

  // ----- PR-D: write actions -----------------------------------------------
  //
  // Generic resource mutation endpoints. Group "" (core API) is sent as
  // literal "core" in the URL because URL segments can't be empty. Match
  // the backend handler in cmd/periscope/main.go.

  applyResource: (
    args: {
      cluster: string;
      group: string;
      version: string;
      resource: string;
      namespace?: string;
      name: string;
      yaml: string;
      dryRun?: boolean;
      force?: boolean;
    },
    signal?: AbortSignal,
  ) => applyResourceFetch(args, signal),

  deleteResource: (
    args: {
      cluster: string;
      group: string;
      version: string;
      resource: string;
      namespace?: string;
      name: string;
    },
    signal?: AbortSignal,
  ) => deleteResourceFetch(args, signal),

  revealSecretKey: (
    c: string,
    ns: string,
    name: string,
    key: string,
    signal?: AbortSignal,
  ) => getText(nsURL(c, "secrets", ns, name, `data/${enc(key)}`), signal),
};

// --- write helpers (kept out of `api` block so the call sites stay readable) ---

function resourceURL(args: {
  cluster: string;
  group: string;
  version: string;
  resource: string;
  namespace?: string;
  name: string;
}): string {
  const group = args.group === "" ? "core" : args.group;
  const base = `/api/clusters/${enc(args.cluster)}/resources/${enc(group)}/${enc(args.version)}/${enc(args.resource)}`;
  return args.namespace
    ? `${base}/${enc(args.namespace)}/${enc(args.name)}`
    : `${base}/${enc(args.name)}`;
}

export interface ApplyResult {
  object: Record<string, unknown>;
  dryRun: boolean;
}

async function applyResourceFetch(
  args: {
    cluster: string;
    group: string;
    version: string;
    resource: string;
    namespace?: string;
    name: string;
    yaml: string;
    dryRun?: boolean;
    force?: boolean;
  },
  signal?: AbortSignal,
): Promise<ApplyResult> {
  const params = new URLSearchParams();
  if (args.dryRun) params.set("dryRun", "true");
  if (args.force) params.set("force", "true");
  const url = resourceURL(args) + (params.toString() ? `?${params.toString()}` : "");
  const res = await fetch(url, {
    method: "PATCH",
    signal,
    headers: {
      "Content-Type": "application/yaml",
      Accept: "application/json",
    },
    body: args.yaml,
  });
  if (!res.ok) {
    const text = await res.text().catch(() => "");
    throw new ApiError(`${res.status} ${res.statusText} on ${url}`, res.status, text);
  }
  return (await res.json()) as ApplyResult;
}

async function deleteResourceFetch(
  args: {
    cluster: string;
    group: string;
    version: string;
    resource: string;
    namespace?: string;
    name: string;
  },
  signal?: AbortSignal,
): Promise<void> {
  const url = resourceURL(args);
  const res = await fetch(url, {
    method: "DELETE",
    signal,
    headers: { Accept: "application/json" },
  });
  if (!res.ok) {
    const text = await res.text().catch(() => "");
    throw new ApiError(`${res.status} ${res.statusText} on ${url}`, res.status, text);
  }
}

export { ApiError };
