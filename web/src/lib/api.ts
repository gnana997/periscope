import type {
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
  NamespaceList,
  PodDetail,
  PodList,
  SecretDetail,
  SecretList,
  ServiceDetail,
  ServiceList,
  StatefulSetDetail,
  StatefulSetList,
  Whoami,
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
  | "namespaces";

export const api = {
  whoami: (signal?: AbortSignal) => getJSON<Whoami>("/api/whoami", signal),

  clusters: (signal?: AbortSignal) =>
    getJSON<ClustersResponse>("/api/clusters", signal),

  // --- LIST ---

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

  getNamespace: (c: string, name: string, signal?: AbortSignal) =>
    getJSON<NamespaceDetail>(clusterScopedURL(c, "namespaces", name), signal),

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
    kind: Exclude<YamlKind, "namespaces">,
    ns: string,
    name: string,
    signal?: AbortSignal,
  ) => getText(nsURL(c, kind, ns, name, "yaml"), signal),

  namespaceYaml: (c: string, name: string, signal?: AbortSignal) =>
    getText(clusterScopedURL(c, "namespaces", name, "yaml"), signal),

  // --- Events ---

  events: (
    c: string,
    kind: Exclude<YamlKind, "namespaces">,
    ns: string,
    name: string,
    signal?: AbortSignal,
  ) => getJSON<EventList>(nsURL(c, kind, ns, name, "events"), signal),

  namespaceEvents: (c: string, name: string, signal?: AbortSignal) =>
    getJSON<EventList>(clusterScopedURL(c, "namespaces", name, "events"), signal),
};

export { ApiError };
