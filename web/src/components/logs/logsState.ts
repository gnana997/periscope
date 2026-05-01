// Shared state shape + (de)serialization for the pod and workload logs
// views. Both wrappers (in-tab local state, full-page URL state) read/write a
// LogsViewState. The full-page wrapper round-trips through query params so
// URLs are shareable.
//
// `podFilter` is only meaningful for workload streams (deployment / sts /
// ds / job); pod streams ignore it. It's part of the shared shape so the
// toolbar/state plumbing stays uniform across variants.

import type { WorkloadKind } from "./PodFilterStrip";

export interface LogsViewState {
  container: string;
  tailLines: number;
  sinceSeconds: number | null;
  previous: boolean;
  follow: boolean;
  timestamps: boolean;
  wrap: boolean;
  search: string;
  podFilter: string[];
}

export const TAIL_DEFAULT = 1000;

export const DEFAULT_LOGS_STATE: LogsViewState = {
  container: "",
  tailLines: TAIL_DEFAULT,
  sinceSeconds: null,
  previous: false,
  follow: true,
  timestamps: true,
  wrap: false,
  search: "",
  podFilter: [],
};

// Only non-default fields land in the URL — keeps shared links short.
export function stateToParams(state: LogsViewState): URLSearchParams {
  const p = new URLSearchParams();
  if (state.container) p.set("container", state.container);
  if (state.tailLines !== TAIL_DEFAULT) p.set("tail", String(state.tailLines));
  if (state.sinceSeconds !== null) p.set("since", String(state.sinceSeconds));
  if (state.previous) p.set("previous", "true");
  if (!state.follow) p.set("follow", "false");
  if (!state.timestamps) p.set("ts", "false");
  if (state.wrap) p.set("wrap", "true");
  if (state.search) p.set("q", state.search);
  if (state.podFilter.length > 0) p.set("pf", state.podFilter.join(","));
  return p;
}

export function paramsToState(params: URLSearchParams): LogsViewState {
  const tailParsed = parseInt(params.get("tail") ?? "", 10);
  const sinceParsed = parseInt(params.get("since") ?? "", 10);
  return {
    container: params.get("container") ?? "",
    tailLines:
      Number.isFinite(tailParsed) && tailParsed > 0 ? tailParsed : TAIL_DEFAULT,
    sinceSeconds:
      Number.isFinite(sinceParsed) && sinceParsed > 0 ? sinceParsed : null,
    previous: params.get("previous") === "true",
    follow: params.get("follow") !== "false",
    timestamps: params.get("ts") !== "false",
    wrap: params.get("wrap") === "true",
    search: params.get("q") ?? "",
    podFilter: (params.get("pf") ?? "").split(",").filter(Boolean),
  };
}

export function buildPodLogsPath(
  cluster: string,
  namespace: string,
  name: string,
  state: LogsViewState,
): string {
  const qs = stateToParams(state).toString();
  return (
    `/clusters/${encodeURIComponent(cluster)}` +
    `/pods/${encodeURIComponent(namespace)}` +
    `/${encodeURIComponent(name)}/logs` +
    (qs ? `?${qs}` : "")
  );
}

const WORKLOAD_PATHS: Record<WorkloadKind, string> = {
  deployment: "deployments",
  statefulset: "statefulsets",
  daemonset: "daemonsets",
  job: "jobs",
};

export function buildWorkloadLogsPath(
  kind: WorkloadKind,
  cluster: string,
  namespace: string,
  name: string,
  state: LogsViewState,
): string {
  const qs = stateToParams(state).toString();
  const segment = WORKLOAD_PATHS[kind];
  return (
    `/clusters/${encodeURIComponent(cluster)}` +
    `/${segment}/${encodeURIComponent(namespace)}` +
    `/${encodeURIComponent(name)}/logs` +
    (qs ? `?${qs}` : "")
  );
}

// Backwards-compat aliases for callers that pre-date the multi-kind work.
export const buildLogsPagePath = buildPodLogsPath;
export const buildDeploymentLogsPath = (
  cluster: string,
  namespace: string,
  name: string,
  state: LogsViewState,
) => buildWorkloadLogsPath("deployment", cluster, namespace, name, state);
