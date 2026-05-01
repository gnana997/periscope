// Shared state shape + (de)serialization for the pod logs view.
//
// Both wrappers (in-tab local state, full-page URL state) read/write a
// LogsViewState. The full-page wrapper round-trips through query params
// using stateToParams/paramsToState so URLs are shareable.

export interface LogsViewState {
  container: string;
  tailLines: number;
  sinceSeconds: number | null;
  previous: boolean;
  follow: boolean;
  timestamps: boolean;
  wrap: boolean;
  search: string;
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
  };
}

export function buildLogsPagePath(
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
