// HelmDiffPage — /clusters/:cluster/helm/:namespace/:name/diff?from=N&to=M
//
// Side-by-side renderer for the structured diff produced by
// /helm/.../diff. Backend returns both raw YAMLs (rendered manifests
// for from + to revisions) and dyff's structured `changes` array.
// Monaco renders the YAMLs; the changes array drives a sidebar list
// of paths-that-changed and is the surface a future LLM-tool caller
// would consume directly.

import { useMemo, useState } from "react";
import { useNavigate, useParams, useSearchParams } from "react-router-dom";
import { useHelmDiff } from "../hooks/useHelm";
import type { HelmDiffItem } from "../lib/types";
import { PageHeader } from "../components/page/PageHeader";
import { ErrorState, ForbiddenState, LoadingState } from "../components/table/states";
import { isForbidden } from "../components/table/isForbidden";
import { InlineDiff } from "../components/detail/yaml/InlineDiff";
import { cn } from "../lib/cn";

export function HelmDiffPage() {
  const { cluster, namespace, name } = useParams<{
    cluster: string;
    namespace: string;
    name: string;
  }>();
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const fromRev = parseInt(params.get("from") ?? "0", 10) || 0;
  const toRev = parseInt(params.get("to") ?? "0", 10) || 0;

  const cl = cluster ?? "";
  const ns = namespace ?? "";
  const nm = name ?? "";

  const query = useHelmDiff(cl, ns, nm, fromRev, toRev);

  if (query.isLoading) return <LoadingState resource="diff" />;
  if (query.isError) {
    if (isForbidden(query.error)) {
      return <ForbiddenState resource="this diff" />;
    }
    return (
      <ErrorState
        title="couldn't compute diff"
        message={(query.error as Error).message}
      />
    );
  }
  const diff = query.data;
  if (!diff) return null;

  return (
    <div className="flex h-full min-h-0 flex-col">
      <PageHeader
        title={`${nm} diff`}
        subtitle={`${ns} · r${diff.from.revision} → r${diff.to.revision} · ${diff.changes.length} ${
          diff.changes.length === 1 ? "change" : "changes"
        }`}
      />
      <div className="flex items-center gap-2 border-b border-border bg-bg px-6 py-2">
        <button
          type="button"
          onClick={() =>
            navigate(
              `/clusters/${encodeURIComponent(cl)}/helm/${encodeURIComponent(
                ns,
              )}/${encodeURIComponent(nm)}?tab=history`,
            )
          }
          className="rounded-sm border border-border-strong px-2.5 py-1 font-mono text-[11.5px] text-ink-muted transition-colors hover:bg-surface-2 hover:text-ink"
        >
          ← back to history
        </button>
      </div>
      <div className="flex min-h-0 flex-1">
        <div className="flex min-w-0 flex-1 flex-col">
          <InlineDiff original={diff.from.yaml} proposed={diff.to.yaml} />
        </div>
        <ChangesSidebar changes={diff.changes} />
      </div>
    </div>
  );
}

// ChangesSidebar lists the structured paths that changed between
// the two revisions. Sourced from dyff on the backend; doubles as
// the agent-tool output for any future LLM call.
function ChangesSidebar({ changes }: { changes: HelmDiffItem[] }) {
  const [filter, setFilter] = useState("");
  const filtered = useMemo(() => {
    if (!filter) return changes;
    const f = filter.toLowerCase();
    return changes.filter(
      (c) =>
        c.path.toLowerCase().includes(f) ||
        c.kind.toLowerCase().includes(f) ||
        (c.before ?? "").toLowerCase().includes(f) ||
        (c.after ?? "").toLowerCase().includes(f),
    );
  }, [changes, filter]);

  return (
    <aside className="flex w-[320px] shrink-0 flex-col border-l border-border bg-surface">
      <div className="border-b border-border px-3 py-2">
        <input
          type="text"
          placeholder="filter changes…"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
          className="w-full rounded-sm border border-border bg-bg px-2 py-1 font-mono text-[11.5px] text-ink placeholder:text-ink-faint focus:border-accent focus:outline-none"
        />
        <div className="mt-1 font-mono text-[10px] uppercase tracking-[0.08em] text-ink-faint">
          {filtered.length} of {changes.length}
        </div>
      </div>
      <div className="flex min-h-0 flex-1 flex-col overflow-y-auto">
        {filtered.length === 0 ? (
          <div className="px-3 py-6 text-center font-mono text-[11.5px] text-ink-faint">
            {changes.length === 0
              ? "no semantic changes"
              : "no matches"}
          </div>
        ) : (
          filtered.map((c, i) => (
            <ChangeRow key={`${c.path}-${i}`} change={c} />
          ))
        )}
      </div>
    </aside>
  );
}

function ChangeRow({ change }: { change: HelmDiffItem }) {
  const tone = changeTone(change.kind);
  return (
    <div className="border-b border-border px-3 py-2">
      <div className="flex items-center gap-1.5">
        <span
          className={cn(
            "rounded-sm px-1 py-px font-mono text-[10px] uppercase",
            tone === "yellow" && "bg-yellow-soft text-yellow",
            tone === "green" && "bg-green-soft text-green",
            tone === "red" && "bg-red-soft text-red",
            tone === "muted" && "bg-surface-2 text-ink-muted",
          )}
        >
          {change.kind}
        </span>
        <span className="min-w-0 flex-1 truncate font-mono text-[11px] text-ink">
          {change.path || "(root)"}
        </span>
      </div>
      {change.kind === "modify" && (
        <div className="mt-1 grid grid-cols-1 gap-1 font-mono text-[10.5px]">
          <ValueBlock label="before" value={change.before} tone="red" />
          <ValueBlock label="after" value={change.after} tone="green" />
        </div>
      )}
      {change.kind === "add" && change.after && (
        <div className="mt-1 font-mono text-[10.5px]">
          <ValueBlock label="added" value={change.after} tone="green" />
        </div>
      )}
      {change.kind === "remove" && change.before && (
        <div className="mt-1 font-mono text-[10.5px]">
          <ValueBlock label="removed" value={change.before} tone="red" />
        </div>
      )}
    </div>
  );
}

function ValueBlock({
  label,
  value,
  tone,
}: {
  label: string;
  value: string | undefined;
  tone: "red" | "green";
}) {
  if (!value) return null;
  const trimmed = value.length > 200 ? value.slice(0, 200) + "…" : value;
  return (
    <div
      className={cn(
        "rounded-sm px-1.5 py-1",
        tone === "red" && "bg-red-soft/40 text-red",
        tone === "green" && "bg-green-soft/40 text-green",
      )}
    >
      <span className="opacity-60">{label}: </span>
      <span className="whitespace-pre-wrap break-all">{trimmed}</span>
    </div>
  );
}

function changeTone(kind: string): "yellow" | "green" | "red" | "muted" {
  switch (kind) {
    case "modify":
      return "yellow";
    case "add":
      return "green";
    case "remove":
      return "red";
    default:
      return "muted";
  }
}
