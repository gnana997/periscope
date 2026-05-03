// HelmReleasePage — /clusters/:cluster/helm/:namespace/:name
//
// Per-release detail. The unified-detail backend endpoint returns
// values + manifest + chart metadata + parsed resources in one blob,
// so the three tabs (values / manifest / history) slice from a single
// cached query — one fetch per (release, revision) navigation.

import { useMemo, useState } from "react";
import { useNavigate, useParams, useSearchParams } from "react-router-dom";
import { useHelmHistory, useHelmRelease } from "../hooks/useHelm";
import type { HelmHistoryEntry, HelmManifestObject } from "../lib/types";
import { ageFrom } from "../lib/format";
import { PageHeader } from "../components/page/PageHeader";
import { ErrorState, ForbiddenState, LoadingState } from "../components/table/states";
import { isForbidden } from "../components/table/isForbidden";
import { MonacoYAML } from "../components/helm/MonacoYAML";
import { cn } from "../lib/cn";

type Tab = "values" | "manifest" | "history";

export function HelmReleasePage() {
  const { cluster, namespace, name } = useParams<{
    cluster: string;
    namespace: string;
    name: string;
  }>();
  const navigate = useNavigate();
  const [params, setParams] = useSearchParams();
  const tab = (params.get("tab") as Tab | null) ?? "values";
  const revisionParam = parseInt(params.get("revision") ?? "0", 10) || 0;

  const setParam = (key: string, value: string | null) => {
    const next = new URLSearchParams(params);
    if (value === null || value === "") next.delete(key);
    else next.set(key, value);
    setParams(next, { replace: true });
  };

  const cl = cluster ?? "";
  const ns = namespace ?? "";
  const nm = name ?? "";

  const detailQuery = useHelmRelease(cl, ns, nm, revisionParam);
  const historyQuery = useHelmHistory(cl, ns, nm);

  if (detailQuery.isLoading) return <LoadingState resource="release" />;
  if (detailQuery.isError) {
    if (isForbidden(detailQuery.error)) {
      return <ForbiddenState resource="this helm release" />;
    }
    return (
      <ErrorState
        title="couldn't load release"
        message={(detailQuery.error as Error).message}
      />
    );
  }
  const detail = detailQuery.data;
  if (!detail) return null;

  const tabs: { id: Tab; label: string }[] = [
    { id: "values", label: "values" },
    { id: "manifest", label: "manifest" },
    { id: "history", label: "history" },
  ];

  return (
    <div className="flex h-full min-h-0 flex-col">
      <PageHeader
        title={detail.name}
        subtitle={`${detail.namespace} · ${detail.chartName}${
          detail.chartVersion ? `-${detail.chartVersion}` : ""
        } · r${detail.revision}${
          detail.appVersion ? ` · app ${detail.appVersion}` : ""
        }`}
      />
      <div className="flex items-center gap-3 border-b border-border bg-bg/80 px-6 py-2 backdrop-blur-md">
        <ResourceSummary resources={detail.resources} />
        <span className="ml-auto font-mono text-[11px] text-ink-faint tabular">
          {detail.updated ? `updated ${ageFrom(detail.updated)}` : null}
        </span>
      </div>
      <div className="flex items-center gap-1 border-b border-border bg-bg px-6">
        {tabs.map((t) => (
          <button
            key={t.id}
            type="button"
            onClick={() => setParam("tab", t.id === "values" ? null : t.id)}
            className={cn(
              "border-b-2 px-3 py-2 font-mono text-[12px] transition-colors",
              tab === t.id
                ? "border-accent text-accent"
                : "border-transparent text-ink-muted hover:text-ink",
            )}
          >
            {t.label}
          </button>
        ))}
      </div>
      <div className="flex min-h-0 flex-1">
        {tab === "values" && (
          <MonacoYAML
            value={detail.valuesYaml}
            emptyLabel="no values overrides for this revision"
          />
        )}
        {tab === "manifest" && <MonacoYAML value={detail.manifestYaml} />}
        {tab === "history" && (
          <HistoryTab
            cluster={cl}
            namespace={ns}
            name={nm}
            entries={historyQuery.data?.revisions ?? []}
            currentRevision={detail.revision}
            isLoading={historyQuery.isLoading}
            isError={historyQuery.isError}
            error={historyQuery.error}
            onSelectRevision={(rev) => setParam("revision", String(rev))}
            onCompare={(from, to) =>
              navigate(
                `/clusters/${encodeURIComponent(cl)}/helm/${encodeURIComponent(
                  ns,
                )}/${encodeURIComponent(nm)}/diff?from=${from}&to=${to}`,
              )
            }
          />
        )}
      </div>
    </div>
  );
}

// ResourceSummary renders the parsed manifest object count, grouped
// by Kind ("12 resources: 3 Deployments, 4 Services, 5 ConfigMaps").
// Free for v1 — the data is already in the detail blob — and powers
// the v2 SAR-gated write path which needs the same parsed list.
function ResourceSummary({ resources }: { resources: HelmManifestObject[] }) {
  const counts = useMemo(() => {
    const m = new Map<string, number>();
    for (const r of resources) {
      m.set(r.kind, (m.get(r.kind) ?? 0) + 1);
    }
    return Array.from(m.entries()).sort((a, b) => b[1] - a[1]);
  }, [resources]);

  if (resources.length === 0) {
    return (
      <span className="font-mono text-[11px] text-ink-faint">
        no rendered resources
      </span>
    );
  }
  const top = counts.slice(0, 4);
  return (
    <span className="font-mono text-[11px] text-ink-muted">
      <span className="text-ink">{resources.length}</span>{" "}
      {resources.length === 1 ? "resource" : "resources"}
      {top.length > 0 && (
        <>
          : {top.map(([kind, n]) => `${n} ${kind}`).join(", ")}
          {counts.length > top.length && (
            <span className="text-ink-faint"> · +{counts.length - top.length} more</span>
          )}
        </>
      )}
    </span>
  );
}

// HistoryTab renders the revision table inline. Two checkboxes per
// row → "compare" button enables → diff route. Single-click on a row
// navigates the page to that revision.
function HistoryTab({
  entries,
  currentRevision,
  isLoading,
  isError,
  error,
  onSelectRevision,
  onCompare,
}: {
  cluster: string;
  namespace: string;
  name: string;
  entries: HelmHistoryEntry[];
  currentRevision: number;
  isLoading: boolean;
  isError: boolean;
  error: unknown;
  onSelectRevision: (rev: number) => void;
  onCompare: (from: number, to: number) => void;
}) {
  const [selected, setSelected] = useState<Set<number>>(new Set());

  if (isLoading) return <LoadingState resource="history" />;
  if (isError) {
    if (isForbidden(error)) return <ForbiddenState resource="release history" />;
    return (
      <ErrorState
        title="couldn't load history"
        message={(error as Error)?.message ?? "unknown"}
      />
    );
  }
  if (entries.length === 0) {
    return (
      <div className="flex h-full items-center justify-center px-6 py-10 text-center font-mono text-[12px] text-ink-faint">
        no revisions
      </div>
    );
  }

  const toggle = (rev: number) => {
    const next = new Set(selected);
    if (next.has(rev)) next.delete(rev);
    else if (next.size >= 2) {
      // Cap at 2 — this is a diff selector, not multi-select.
      const oldest = Array.from(next).sort()[0];
      next.delete(oldest);
      next.add(rev);
    } else {
      next.add(rev);
    }
    setSelected(next);
  };

  const compareEnabled = selected.size === 2;
  const sortedSel = Array.from(selected).sort((a, b) => a - b);

  return (
    <div className="flex h-full min-h-0 w-full flex-col">
      <div className="flex items-center justify-between border-b border-border px-6 py-2">
        <span className="font-mono text-[11px] text-ink-muted">
          {entries.length} {entries.length === 1 ? "revision" : "revisions"}
          {selected.size > 0 && (
            <span className="ml-2 text-ink-faint">
              · {selected.size} selected
            </span>
          )}
        </span>
        <button
          type="button"
          disabled={!compareEnabled}
          onClick={() => compareEnabled && onCompare(sortedSel[0], sortedSel[1])}
          className={cn(
            "rounded-sm border border-border-strong px-2.5 py-1 font-mono text-[11.5px] transition-colors",
            "hover:bg-surface-2 hover:text-ink",
            "disabled:cursor-not-allowed disabled:opacity-50 disabled:hover:bg-transparent",
          )}
        >
          compare selected
        </button>
      </div>
      <div className="flex min-h-0 flex-1 flex-col overflow-y-auto">
        {entries.map((e) => {
          const isCurrent = e.revision === currentRevision;
          const isSelected = selected.has(e.revision);
          return (
            <div
              key={e.revision}
              className={cn(
                "flex items-center gap-3 border-b border-border px-6 py-2 transition-colors",
                isCurrent && "bg-accent-soft/30",
                isSelected && "bg-accent-soft/60",
              )}
            >
              <input
                type="checkbox"
                checked={isSelected}
                onChange={() => toggle(e.revision)}
                aria-label={`select revision ${e.revision}`}
                className="size-3.5 cursor-pointer accent-accent"
              />
              <button
                type="button"
                onClick={() => onSelectRevision(e.revision)}
                className="flex flex-1 items-center gap-3 text-left"
              >
                <span className="w-12 shrink-0 font-mono text-[11.5px] text-ink-muted tabular">
                  r{e.revision}
                </span>
                <span className="w-24 shrink-0 font-mono text-[11px] text-ink-muted">
                  {e.status}
                </span>
                <span className="min-w-0 flex-1 truncate font-mono text-[11.5px] text-ink">
                  {e.chartName}
                  {e.chartVersion ? `-${e.chartVersion}` : ""}
                </span>
                <span className="hidden min-w-0 max-w-md truncate font-mono text-[11px] text-ink-faint md:block">
                  {e.description}
                </span>
                <span className="w-20 shrink-0 text-right font-mono text-[11px] text-ink-muted tabular">
                  {e.updated ? ageFrom(e.updated) : "—"}
                </span>
              </button>
            </div>
          );
        })}
      </div>
    </div>
  );
}
