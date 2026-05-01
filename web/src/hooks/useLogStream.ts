import { useCallback, useEffect, useState } from "react";

export interface LogLine {
  id: number;
  ts: string;
  text: string;
  pod?: string;
  container?: string;
}

// Per-pod attribution received via SSE `event: meta`. Node is empty when
// the pod isn't yet scheduled.
export interface PodAttribution {
  name: string;
  node: string;
}

// Discriminated union over the supported log endpoints. Adding a new
// source means adding a variant here and a matching case in buildUrl.
export type LogStreamSource =
  | { kind: "pod"; cluster: string; namespace: string; name: string }
  | { kind: "deployment"; cluster: string; namespace: string; name: string }
  | { kind: "statefulset"; cluster: string; namespace: string; name: string }
  | { kind: "daemonset"; cluster: string; namespace: string; name: string }
  | { kind: "job"; cluster: string; namespace: string; name: string };

export interface LogStreamConfig {
  source: LogStreamSource;
  container?: string;
  tailLines?: number;
  sinceSeconds?: number;
  previous?: boolean;
  follow?: boolean;
}

export type LogStreamStatus =
  | "idle"
  | "connecting"
  | "streaming"
  | "closed"
  | "error";

export interface UseLogStreamResult {
  lines: LogLine[];
  status: LogStreamStatus;
  error?: string;
  /** Total lines received, including any dropped from buffer overflow. */
  totalReceived: number;
  /** True when older lines were evicted to keep within BUFFER_CAP. */
  overflowed: boolean;
  /** Closes the current stream and re-opens it with the same config. */
  reload: () => void;
  /**
   * Active source pods reported via `event: meta`. Always [] for pod
   * streams; grows/shrinks live for workload streams as pods are
   * added/removed.
   */
  pods: PodAttribution[];
}

const BUFFER_CAP = 50_000;
const FLUSH_INTERVAL_MS = 50;

const SOURCE_PATHS: Record<LogStreamSource["kind"], string> = {
  pod: "pods",
  deployment: "deployments",
  statefulset: "statefulsets",
  daemonset: "daemonsets",
  job: "jobs",
};

function buildUrl(source: LogStreamSource, params: URLSearchParams): string {
  const enc = encodeURIComponent;
  const segment = SOURCE_PATHS[source.kind];
  const path =
    `/api/clusters/${enc(source.cluster)}/${segment}` +
    `/${enc(source.namespace)}/${enc(source.name)}/logs`;
  const qs = params.toString();
  return qs ? `${path}?${qs}` : path;
}

// useLogStream consumes a Periscope logs SSE endpoint (pod or workload).
//
// The hook keeps the most recent BUFFER_CAP lines in a ring; older lines
// are dropped (search-with-context still works on whatever is in buffer).
// Lines are batched on a 50ms timer to keep React re-renders bounded
// under high-volume streams.
export function useLogStream(config: LogStreamConfig): UseLogStreamResult {
  const [lines, setLines] = useState<LogLine[]>([]);
  const [status, setStatus] = useState<LogStreamStatus>("idle");
  const [error, setError] = useState<string | undefined>(undefined);
  const [totalReceived, setTotalReceived] = useState(0);
  const [overflowed, setOverflowed] = useState(false);
  const [pods, setPods] = useState<PodAttribution[]>([]);
  const [reloadTick, setReloadTick] = useState(0);

  const reload = useCallback(() => setReloadTick((t) => t + 1), []);

  useEffect(() => {
    const buffer: LogLine[] = [];
    const pending: LogLine[] = [];
    let nextId = 0;
    let receivedCount = 0;
    let didOverflow = false;
    let flushTimer: number | null = null;
    let closedManually = false;

    const flush = () => {
      flushTimer = null;
      if (pending.length === 0) return;
      buffer.push(...pending);
      pending.length = 0;
      if (buffer.length > BUFFER_CAP) {
        buffer.splice(0, buffer.length - BUFFER_CAP);
        if (!didOverflow) {
          didOverflow = true;
          setOverflowed(true);
        }
      }
      setLines(buffer.slice());
      setTotalReceived(receivedCount);
    };

    const scheduleFlush = () => {
      if (flushTimer !== null) return;
      flushTimer = window.setTimeout(flush, FLUSH_INTERVAL_MS);
    };

    setLines([]);
    setStatus("connecting");
    setError(undefined);
    setTotalReceived(0);
    setOverflowed(false);
    setPods([]);

    const params = new URLSearchParams();
    if (config.container) params.set("container", config.container);
    if (config.tailLines && config.tailLines > 0) {
      params.set("tailLines", String(config.tailLines));
    }
    if (config.sinceSeconds && config.sinceSeconds > 0) {
      params.set("sinceSeconds", String(config.sinceSeconds));
    }
    if (config.previous) params.set("previous", "true");
    if (config.follow !== undefined) {
      params.set("follow", String(config.follow));
    }
    const url = buildUrl(config.source, params);

    const es = new EventSource(url);

    es.onopen = () => {
      if (!closedManually) setStatus("streaming");
    };

    es.onmessage = (event) => {
      try {
        const parsed = JSON.parse(event.data) as {
          t?: string;
          l?: string;
          p?: string;
          c?: string;
        };
        receivedCount++;
        pending.push({
          id: nextId++,
          ts: parsed.t ?? "",
          text: parsed.l ?? "",
          pod: parsed.p,
          container: parsed.c,
        });
        scheduleFlush();
      } catch {
        // Malformed line — skip silently rather than tearing down the stream.
      }
    };

    es.addEventListener("meta", (event) => {
      const me = event as MessageEvent;
      try {
        const parsed = JSON.parse(me.data) as {
          pods?: Array<{ name?: string; node?: string }>;
        };
        if (Array.isArray(parsed.pods)) {
          setPods(
            parsed.pods.map((p) => ({
              name: p.name ?? "",
              node: p.node ?? "",
            })),
          );
        }
      } catch {
        // Ignore malformed meta events.
      }
    });

    es.addEventListener("done", () => {
      closedManually = true;
      es.close();
      flush();
      setStatus("closed");
    });

    es.addEventListener("error", (event) => {
      const me = event as MessageEvent;
      // Server-emitted error events carry a JSON body; native errors don't.
      if (typeof me.data === "string" && me.data.length > 0) {
        try {
          const { message } = JSON.parse(me.data) as { message?: string };
          setError(message ?? "stream error");
        } catch {
          setError("stream error");
        }
        closedManually = true;
        es.close();
        flush();
        setStatus("error");
        return;
      }
      // Native connection error. Close to suppress the auto-reconnect (which
      // would replay tailLines and produce duplicates). User can reload().
      if (!closedManually) {
        closedManually = true;
        es.close();
        flush();
        setStatus("error");
        setError("connection lost");
      }
    });

    return () => {
      closedManually = true;
      es.close();
      if (flushTimer !== null) window.clearTimeout(flushTimer);
    };
  }, [
    config.source.kind,
    config.source.cluster,
    config.source.namespace,
    config.source.name,
    config.container,
    config.tailLines,
    config.sinceSeconds,
    config.previous,
    config.follow,
    reloadTick,
  ]);

  return { lines, status, error, totalReceived, overflowed, reload, pods };
}
