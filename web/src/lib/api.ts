import type {
  ClustersResponse,
  ConfigMapList,
  DeploymentList,
  NamespaceList,
  PodList,
  ServiceList,
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

export const api = {
  whoami: (signal?: AbortSignal) => getJSON<Whoami>("/api/whoami", signal),

  clusters: (signal?: AbortSignal) =>
    getJSON<ClustersResponse>("/api/clusters", signal),

  namespaces: (cluster: string, signal?: AbortSignal) =>
    getJSON<NamespaceList>(
      `/api/clusters/${encodeURIComponent(cluster)}/namespaces`,
      signal,
    ),

  pods: (cluster: string, namespace?: string, signal?: AbortSignal) => {
    const qs = namespace ? `?namespace=${encodeURIComponent(namespace)}` : "";
    return getJSON<PodList>(
      `/api/clusters/${encodeURIComponent(cluster)}/pods${qs}`,
      signal,
    );
  },

  deployments: (cluster: string, namespace?: string, signal?: AbortSignal) => {
    const qs = namespace ? `?namespace=${encodeURIComponent(namespace)}` : "";
    return getJSON<DeploymentList>(
      `/api/clusters/${encodeURIComponent(cluster)}/deployments${qs}`,
      signal,
    );
  },

  services: (cluster: string, namespace?: string, signal?: AbortSignal) => {
    const qs = namespace ? `?namespace=${encodeURIComponent(namespace)}` : "";
    return getJSON<ServiceList>(
      `/api/clusters/${encodeURIComponent(cluster)}/services${qs}`,
      signal,
    );
  },

  configmaps: (cluster: string, namespace?: string, signal?: AbortSignal) => {
    const qs = namespace ? `?namespace=${encodeURIComponent(namespace)}` : "";
    return getJSON<ConfigMapList>(
      `/api/clusters/${encodeURIComponent(cluster)}/configmaps${qs}`,
      signal,
    );
  },
};

export { ApiError };
