import { useMemo } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { useClusterEvents } from "../hooks/useResource";
import { ageFrom } from "../lib/format";
import { cn } from "../lib/cn";
import type { ClusterEvent } from "../lib/types";
import { PageHeader } from "../components/page/PageHeader";
import { FilterStrip } from "../components/page/FilterStrip";
import { EmptyState, ErrorState, LoadingState } from "../components/table/states";
import { NamespacePicker } from "../components/shell/NamespacePicker";

const EVENT_TYPE_OPTIONS = ["Warning", "Normal"];

// Maps K8s object kind → Periscope route segment for cross-linking.
function resourcePath(
  cluster: string,
  kind: string,
  ns: string,
  name: string,
): string | null {
  const kindMap: Record<string, string> = {
    Pod: "pods",
    Deployment: "deployments",
    StatefulSet: "statefulsets",
    DaemonSet: "daemonsets",
    Job: "jobs",
    CronJob: "cronjobs",
    Service: "services",
    Ingress: "ingresses",
    ConfigMap: "configmaps",
    Secret: "secrets",
    PersistentVolumeClaim: "pvcs",
    PersistentVolume: "pvs",
  };
  const route = kindMap[kind];
  if (!route) return null;
  return (
    `/clusters/${encodeURIComponent(cluster)}/` +
    `${route}?sel=${encodeURIComponent(name)}` +
    `&selNs=${encodeURIComponent(ns)}&tab=describe`
  );
}

// Heartbeat pulse — only this page auto-refreshes, so it earns the indicator.
function LivePulse() {
  return (
    <div className="flex items-center gap-1.5">
      <span className="relative flex size-2">
        <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-accent opacity-60" />
        <span className="relative inline-flex size-2 rounded-full bg-accent" />
      </span>
      <span className="font-mono text-[10px] uppercase tracking-[0.06em] text-ink-faint">
        live
      </span>
    </div>
  );
}

// Shared grid template — header and rows must agree exactly.
const COLS =
  "20px minmax(180px, 260px) minmax(80px, 110px) minmax(90px, 120px) minmax(0, 1fr) 52px 90px 58px";

function FeedHeader() {
  return (
    <div
      className="sticky top-0 z-10 grid items-center border-b border-border bg-bg/95 backdrop-blur-sm"
      style={{ gridTemplateColumns: COLS }}
    >
      <div />
      {(["object", "namespace", "reason", "message"] as const).map((col) => (
        <div
          key={col}
          className="py-2 font-mono text-[10px] font-medium uppercase tracking-[0.08em] text-ink-faint"
        >
          {col}
        </div>
      ))}
      <div className="py-2 pr-2 text-right font-mono text-[10px] font-medium uppercase tracking-[0.08em] text-ink-faint">
        ×
      </div>
      <div className="py-2 font-mono text-[10px] font-medium uppercase tracking-[0.08em] text-ink-faint">
        source
      </div>
      <div className="py-2 pr-4 text-right font-mono text-[10px] font-medium uppercase tracking-[0.08em] text-ink-faint">
        age
      </div>
    </div>
  );
}

function EventRow({
  event: e,
  cluster,
}: {
  event: ClusterEvent;
  cluster: string;
}) {
  const isWarning = e.type === "Warning";
  const path = resourcePath(cluster, e.kind, e.namespace, e.name);

  return (
    <li
      className={cn(
        "group grid items-center border-b border-border/50 border-l-2 transition-colors",
        isWarning
          ? "border-l-yellow bg-yellow-soft/20 hover:bg-yellow-soft/40"
          : "border-l-transparent hover:bg-surface-2/40",
      )}
      style={{ gridTemplateColumns: COLS }}
    >
      {/* Severity dot */}
      <div className="flex items-center justify-center">
        <span
          className={cn(
            "block size-[5px] rounded-full",
            isWarning ? "bg-yellow" : "bg-ink-faint/40",
          )}
        />
      </div>

      {/* Kind badge + name link */}
      <div className="flex min-w-0 items-center gap-1.5 py-2.5 pr-3">
        <span className="shrink-0 rounded border border-border bg-surface-2 px-1 py-px font-mono text-[9.5px] uppercase tracking-[0.04em] text-ink-faint">
          {e.kind}
        </span>
        {path ? (
          <Link
            to={path}
            className="min-w-0 truncate font-mono text-[12px] text-ink hover:text-accent hover:underline"
            title={e.name}
          >
            {e.name}
          </Link>
        ) : (
          <span
            className="min-w-0 truncate font-mono text-[12px] text-ink"
            title={e.name}
          >
            {e.name}
          </span>
        )}
      </div>

      {/* Namespace */}
      <div className="py-2.5 pr-3">
        <span
          className="block truncate font-mono text-[11.5px] text-ink-muted"
          title={e.namespace}
        >
          {e.namespace}
        </span>
      </div>

      {/* Reason */}
      <div className="py-2.5 pr-3">
        <span
          className={cn(
            "block truncate font-mono text-[11.5px]",
            isWarning ? "text-yellow" : "text-ink-muted",
          )}
          title={e.reason}
        >
          {e.reason}
        </span>
      </div>

      {/* Message */}
      <div className="min-w-0 py-2.5 pr-4">
        <span
          className="block truncate text-[12px] leading-[1.4] text-ink-muted"
          title={e.message}
        >
          {e.message}
        </span>
      </div>

      {/* Count — only shown when > 1, high counts go yellow */}
      <div className="py-2.5 pr-2 text-right">
        {e.count > 1 && (
          <span
            className={cn(
              "font-mono text-[11px] tabular",
              e.count > 100 ? "text-yellow" : "text-ink-faint",
            )}
          >
            ×{e.count > 9999 ? "9999+" : e.count}
          </span>
        )}
      </div>

      {/* Source */}
      <div className="py-2.5 pr-3">
        <span
          className="block truncate font-mono text-[10.5px] text-ink-faint"
          title={e.source}
        >
          {e.source}
        </span>
      </div>

      {/* Age */}
      <div className="py-2.5 pr-4 text-right">
        <span className="font-mono text-[11px] tabular text-ink-faint">
          {ageFrom(e.last)}
        </span>
      </div>
    </li>
  );
}

function EventFeed({
  events,
  cluster,
}: {
  events: ClusterEvent[];
  cluster: string;
}) {
  return (
    <div className="min-w-0">
      <FeedHeader />
      <ul>
        {events.map((e, i) => (
          <EventRow
            key={`${e.namespace}/${e.kind}/${e.name}/${e.reason}/${i}`}
            event={e}
            cluster={cluster}
          />
        ))}
      </ul>
    </div>
  );
}

export function EventsPage({ cluster }: { cluster: string }) {
  const [params, setParams] = useSearchParams();
  const namespace = params.get("ns");
  const search = params.get("q") ?? "";
  const typeFilter = params.get("type");

  const setParam = (key: string, value: string | null) => {
    const next = new URLSearchParams(params);
    if (value === null || value === "") next.delete(key);
    else next.set(key, value);
    setParams(next, { replace: true });
  };

  const { data, isLoading, isError, error, streamStatus } = useClusterEvents(
    cluster,
    namespace ?? undefined,
  );

  const all = useMemo(() => data?.events ?? [], [data]);
  const warnings = all.filter((e) => e.type === "Warning").length;

  const filtered = useMemo(() => {
    let rows = all;
    if (typeFilter) rows = rows.filter((e) => e.type === typeFilter);
    if (search) {
      const q = search.toLowerCase();
      rows = rows.filter(
        (e) =>
          e.name.toLowerCase().includes(q) ||
          e.reason.toLowerCase().includes(q) ||
          e.message.toLowerCase().includes(q),
      );
    }
    return rows;
  }, [all, typeFilter, search]);

  return (
    <div className="flex h-full min-h-0 flex-col">
      <PageHeader
        title="Events"
        subtitle={
          data
            ? `${all.length} ${all.length === 1 ? "event" : "events"}${
                namespace ? ` in ${namespace}` : ""
              }`
            : undefined
        }
        chips={[
          {
            label: "warnings",
            count: warnings,
            tone: "yellow",
            active: typeFilter === "Warning",
            onClick: () =>
              setParam("type", typeFilter === "Warning" ? null : "Warning"),
          },
        ]}
        streamStatus={streamStatus}
        trailing={
          <div className="flex items-center gap-3">
            <LivePulse />
            <NamespacePicker />
          </div>
        }
      />
      <FilterStrip
        search={search}
        onSearch={(v) => setParam("q", v)}
        statusFilter={typeFilter}
        statusOptions={EVENT_TYPE_OPTIONS}
        onStatusFilter={(v) => setParam("type", v)}
        resultCount={filtered.length}
        totalCount={all.length}
      />
      <div className="flex min-h-0 flex-1 flex-col overflow-y-auto">
        {isLoading ? (
          <LoadingState resource="events" />
        ) : isError ? (
          <ErrorState
            title="couldn't load events"
            message={(error as Error)?.message ?? "unknown"}
          />
        ) : filtered.length === 0 ? (
          <EmptyState resource="events" namespace={namespace} />
        ) : (
          <EventFeed events={filtered} cluster={cluster} />
        )}
      </div>
    </div>
  );
}
