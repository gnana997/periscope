import { useMemo, useState } from "react";
import { useParams, useSearchParams, useNavigate } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import {
  useCRDs,
  useCustomResources,
  useCustomResourceDetail,
} from "../hooks/useResource";
import { api } from "../lib/api";
import { ageFrom, nameMatches } from "../lib/format";
import { cn } from "../lib/cn";
import { PageHeader } from "../components/page/PageHeader";
import { SplitPane } from "../components/page/SplitPane";
import {
  DataTable,
  type Column,
} from "../components/table/DataTable";
import {
  EmptyState,
  ErrorState,
  LoadingState,
} from "../components/table/states";
import { DetailPane } from "../components/detail/DetailPane";
import {
  DetailEmpty,
  DetailError,
  DetailLoading,
} from "../components/detail/states";
import { NamespacePicker } from "../components/shell/NamespacePicker";
import type { CRD, CustomResource } from "../lib/types";

/**
 * CustomResourcesPage — generic list+detail view for any CRD.
 *
 * The page reads {group, version, plural} from the URL, looks up the
 * CRD definition for printer-column metadata, and renders a DataTable
 * whose columns match what `kubectl get <plural>` would show. Detail
 * tabs reuse the standard describe/yaml/events shape but talk to the
 * dynamic-client backend endpoints.
 *
 * Cluster-scoped CRDs render without a Namespace column or NS picker
 * (the list response carries scope info).
 */
export function CustomResourcesPage({ cluster }: { cluster: string }) {
  const { group, version, plural } = useParams<{
    group: string;
    version: string;
    plural: string;
  }>();
  const [params, setParams] = useSearchParams();
  const navigate = useNavigate();

  const namespace = params.get("ns");
  const search = params.get("q") ?? "";
  const selectedNs = params.get("selNs");
  const selectedName = params.get("sel");
  const activeTab = params.get("tab") ?? "describe";

  const setParam = (key: string, value: string | null) => {
    const next = new URLSearchParams(params);
    if (value === null || value === "") next.delete(key);
    else next.set(key, value);
    setParams(next, { replace: true });
  };
  const setMany = (updates: Record<string, string | null>) => {
    const next = new URLSearchParams(params);
    for (const [k, v] of Object.entries(updates)) {
      if (v === null || v === "") next.delete(k);
      else next.set(k, v);
    }
    setParams(next, { replace: true });
  };

  // CRD definition for the title and to know whether the resource is
  // cluster-scoped. Cached aggressively — CRD definitions change rarely.
  const crdsQuery = useCRDs(cluster);
  const crd: CRD | undefined = useMemo(
    () =>
      crdsQuery.data?.crds.find(
        (c) => c.group === group && c.plural === plural,
      ),
    [crdsQuery.data, group, plural],
  );

  const isClusterScoped = crd?.scope === "Cluster";

  const listQuery = useCustomResources(
    cluster,
    group ?? "",
    version ?? "",
    plural ?? "",
    isClusterScoped ? undefined : namespace ?? undefined,
  );

  const all: CustomResource[] = listQuery.data?.items ?? [];
  const filtered = useMemo(
    () => (search ? all.filter((r) => nameMatches(r.name, search)) : all),
    [all, search],
  );

  const printerColumns = listQuery.data?.columns ?? [];

  // Build DataTable columns from printer-column definitions plus the
  // standard Name + Namespace + Age columns we always inject. Dynamic
  // weights — flex-1 for each printer column so they share remaining
  // width after Name/Namespace.
  const columns: Column<CustomResource>[] = useMemo(() => {
    const cols: Column<CustomResource>[] = [
      {
        key: "name",
        header: "name",
        weight: 2.5,
        cellClassName: "font-mono text-ink",
        accessor: (r) => r.name,
      },
    ];
    if (!isClusterScoped) {
      cols.push({
        key: "namespace",
        header: "namespace",
        weight: 1.5,
        cellClassName: "font-mono text-ink-muted",
        accessor: (r) => r.namespace ?? "—",
      });
    }
    for (const pc of printerColumns) {
      cols.push({
        key: `pc:${pc.name}`,
        header: pc.name.toLowerCase(),
        weight: 1,
        cellClassName: "font-mono text-ink-muted",
        accessor: (r) => r.columns[pc.name] ?? "—",
      });
    }
    cols.push({
      key: "age",
      header: "age",
      weight: 0.6,
      align: "right",
      cellClassName: "font-mono text-ink-muted",
      accessor: (r) => ageFrom(r.createdAt),
    });
    return cols;
  }, [printerColumns, isClusterScoped]);

  // Detail key — for cluster-scoped CRDs we don't track ns.
  const selectedKey =
    selectedName && (isClusterScoped || selectedNs)
      ? `${selectedNs ?? ""}/${selectedName}`
      : null;

  const onRowClick = (r: CustomResource) => {
    setMany({
      sel: r.name,
      selNs: isClusterScoped ? null : r.namespace ?? "",
      tab: "describe",
    });
  };

  const detail =
    selectedName && (isClusterScoped || selectedNs) ? (
      <DetailPane
        title={selectedName}
        subtitle={isClusterScoped ? "cluster-scoped" : selectedNs ?? ""}
        activeTab={activeTab}
        onTabChange={(id) => setParam("tab", id)}
        onClose={() => setMany({ sel: null, selNs: null, tab: null })}
        tabs={[
          {
            id: "describe",
            label: "describe",
            ready: true,
            content: (
              <CustomResourceDescribe
                cluster={cluster}
                group={group ?? ""}
                version={version ?? ""}
                plural={plural ?? ""}
                namespace={isClusterScoped ? null : selectedNs ?? null}
                name={selectedName}
              />
            ),
          },
          {
            id: "yaml",
            label: "yaml",
            ready: true,
            content: (
              <CustomResourceYamlView
                cluster={cluster}
                group={group ?? ""}
                version={version ?? ""}
                plural={plural ?? ""}
                namespace={isClusterScoped ? null : selectedNs ?? null}
                name={selectedName}
              />
            ),
          },
          {
            id: "events",
            label: "events",
            ready: true,
            content: (
              <CustomResourceEventsView
                cluster={cluster}
                group={group ?? ""}
                version={version ?? ""}
                plural={plural ?? ""}
                namespace={isClusterScoped ? null : selectedNs ?? null}
                name={selectedName}
              />
            ),
          },
        ]}
      />
    ) : null;

  return (
    <div className="flex h-full min-h-0 flex-col">
      <PageHeader
        title={crd?.kind ?? plural ?? "Custom Resources"}
        subtitle={
          crd
            ? `${crd.group}/${crd.servedVersion} · ${
                listQuery.data?.items.length ?? 0
              } ${(listQuery.data?.items.length ?? 0) === 1 ? "item" : "items"}${
                isClusterScoped ? "" : namespace ? ` in ${namespace}` : ""
              }`
            : `${group}/${version}/${plural}`
        }
        trailing={
          <div className="flex items-center gap-2">
            <button
              type="button"
              onClick={() => navigate(`/clusters/${encodeURIComponent(cluster)}/crds`)}
              className="rounded-md border border-border bg-surface px-2 py-1 font-mono text-[11px] text-ink-muted hover:border-border-strong hover:text-ink"
              title="back to CRD catalog"
            >
              ← all CRDs
            </button>
            {!isClusterScoped && <NamespacePicker />}
          </div>
        }
      />

      <div className="flex items-center gap-3 border-b border-border bg-bg px-6 py-2.5">
        <div className="flex min-w-[280px] flex-1 items-center gap-2 rounded-md border border-border bg-surface px-3 py-1.5 text-[12.5px] focus-within:border-border-strong">
          <svg width="13" height="13" viewBox="0 0 13 13" className="shrink-0 text-ink-faint" aria-hidden>
            <circle cx="5.5" cy="5.5" r="3.6" stroke="currentColor" strokeWidth="1.3" fill="none" />
            <path d="M8.3 8.3l2.4 2.4" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" />
          </svg>
          <input
            value={search}
            onChange={(e) => setParam("q", e.target.value)}
            placeholder="filter by name"
            className="min-w-0 flex-1 bg-transparent font-mono text-[12.5px] text-ink outline-none placeholder:text-ink-faint"
            spellCheck={false}
            autoComplete="off"
          />
        </div>
        <span className="font-mono text-[11px] tabular-nums text-ink-muted">
          {filtered.length}
          <span className="text-ink-faint"> / </span>
          {all.length}
        </span>
      </div>

      <SplitPane
        storageKey="periscope.detailWidth.v4"
        left={
          listQuery.isLoading || crdsQuery.isLoading ? (
            <LoadingState resource={crd?.kind?.toLowerCase() ?? "custom resources"} />
          ) : listQuery.isError ? (
            <ErrorState
              title="couldn't load custom resources"
              message={(listQuery.error as Error)?.message ?? "unknown"}
            />
          ) : filtered.length === 0 ? (
            <EmptyState resource={crd?.kind?.toLowerCase() ?? "items"} namespace={namespace} />
          ) : (
            <DataTable<CustomResource>
              columns={columns}
              rows={filtered}
              rowKey={(r) => `${r.namespace ?? ""}/${r.name}`}
              onRowClick={onRowClick}
              selectedKey={selectedKey}
            />
          )
        }
        right={detail}
      />
    </div>
  );
}

// ---------------------------------------------------------------------
// Describe tab — generic structured rendering of an unstructured CR
// ---------------------------------------------------------------------

interface CRDetailRefProps {
  cluster: string;
  group: string;
  version: string;
  plural: string;
  namespace: string | null;
  name: string;
}

function CustomResourceDescribe(props: CRDetailRefProps) {
  const { cluster, group, version, plural, namespace, name } = props;
  const { data, isLoading, isError, error } = useCustomResourceDetail(
    cluster,
    group,
    version,
    plural,
    namespace,
    name,
  );
  if (isLoading) return <DetailLoading label="loading…" />;
  if (isError)
    return <DetailError message={(error as Error)?.message ?? "unknown"} />;
  if (!data) return null;

  const obj = data.object as Record<string, unknown>;
  const meta = (obj.metadata ?? {}) as Record<string, unknown>;
  const spec = (obj.spec ?? {}) as Record<string, unknown>;
  const status = (obj.status ?? {}) as Record<string, unknown>;
  const labels = (meta.labels ?? {}) as Record<string, string>;
  const annotations = (meta.annotations ?? {}) as Record<string, string>;
  const conditions = Array.isArray(status.conditions)
    ? (status.conditions as Array<Record<string, unknown>>)
    : [];

  return (
    <div className="px-5 py-4 font-mono text-[12px]">
      <Section title="metadata">
        <KV k="kind" v={`${data.apiVersion}/${data.kind}`} />
        {data.namespace && <KV k="namespace" v={data.namespace} />}
        <KV k="age" v={ageFrom(data.createdAt)} />
        {meta.uid ? <KV k="uid" v={String(meta.uid)} /> : null}
      </Section>

      {Object.keys(labels).length > 0 && (
        <Section title="labels">
          <Pills items={labels} />
        </Section>
      )}
      {Object.keys(annotations).length > 0 && (
        <Section title="annotations">
          <Pills items={annotations} />
        </Section>
      )}

      {conditions.length > 0 && (
        <Section title="conditions">
          <ConditionsList conditions={conditions} />
        </Section>
      )}

      {Object.keys(spec).length > 0 && (
        <Section title="spec">
          <KeyTree obj={spec} />
        </Section>
      )}

      {Object.keys(status).filter((k) => k !== "conditions").length > 0 && (
        <Section title="status">
          <KeyTree obj={omitKey(status, "conditions")} />
        </Section>
      )}
    </div>
  );
}

function Section({
  title,
  children,
}: {
  title: string;
  children: React.ReactNode;
}) {
  return (
    <section className="mb-4">
      <h3 className="mb-2 text-[10px] font-medium uppercase tracking-[0.08em] text-ink-faint">
        {title}
      </h3>
      <div>{children}</div>
    </section>
  );
}

function KV({ k, v }: { k: string; v: string }) {
  return (
    <div className="grid grid-cols-[110px_1fr] gap-3 py-0.5">
      <span className="text-ink-faint">{k}</span>
      <span className="break-all text-ink">{v}</span>
    </div>
  );
}

function Pills({ items }: { items: Record<string, string> }) {
  return (
    <div className="grid grid-cols-1 gap-1.5 md:grid-cols-2">
      {Object.entries(items).map(([k, v]) => (
        <div
          key={k}
          className="flex min-w-0 items-center gap-1 rounded-md border border-border bg-surface-2/40 px-2 py-0.5 text-[11px]"
        >
          <span className="shrink-0 text-ink-muted">{k}</span>
          <span className="shrink-0 text-ink-faint">=</span>
          <span className="min-w-0 truncate text-ink" title={v}>
            {v}
          </span>
        </div>
      ))}
    </div>
  );
}

function ConditionsList({
  conditions,
}: {
  conditions: Array<Record<string, unknown>>;
}) {
  return (
    <ul className="space-y-1.5">
      {conditions.map((c, i) => {
        const status = String(c.status ?? "");
        const ok = status === "True";
        const tone = ok ? "text-green" : status === "False" ? "text-yellow" : "text-ink-muted";
        const dot = ok ? "bg-green" : status === "False" ? "bg-yellow" : "bg-ink-faint";
        return (
          <li key={i}>
            <div className="flex items-baseline gap-2 text-[12px]">
              <span className={cn("mt-[3px] block size-1.5 shrink-0 self-center rounded-full", dot)} />
              <span className="text-ink">{String(c.type ?? "")}</span>
              {c.reason ? (
                <span className="text-ink-muted">· {String(c.reason)}</span>
              ) : null}
              <span className={cn("ml-auto", tone)}>{status}</span>
            </div>
            {c.message ? (
              <div className="ml-3.5 mt-0.5 break-words text-[11.5px] text-ink-muted">
                {String(c.message)}
              </div>
            ) : null}
          </li>
        );
      })}
    </ul>
  );
}

/** Tiny one-level tree renderer for spec/status. Scalars print
 *  inline; objects/arrays show count + a yaml-tab nudge. Keeps the
 *  describe view from becoming an unbounded JSON dump. */
function KeyTree({ obj }: { obj: Record<string, unknown> }) {
  const entries = Object.entries(obj);
  return (
    <ul className="space-y-0.5">
      {entries.map(([k, v]) => (
        <li
          key={k}
          className="grid grid-cols-[140px_1fr] gap-3 py-0.5 text-[12px]"
        >
          <span className="text-ink-faint">{k}</span>
          <span className="min-w-0 truncate text-ink" title={typeof v === "string" ? v : undefined}>
            {renderScalarOrSummary(v)}
          </span>
        </li>
      ))}
    </ul>
  );
}

function renderScalarOrSummary(v: unknown): React.ReactNode {
  if (v === null || v === undefined) return <span className="text-ink-faint">—</span>;
  if (typeof v === "string") return v;
  if (typeof v === "number" || typeof v === "boolean") return String(v);
  if (Array.isArray(v)) {
    return (
      <span className="text-ink-muted">
        [{v.length} {v.length === 1 ? "item" : "items"}] ·{" "}
        <span className="text-ink-faint">see yaml</span>
      </span>
    );
  }
  // Object
  const keys = Object.keys(v as object);
  return (
    <span className="text-ink-muted">
      {`{${keys.length} ${keys.length === 1 ? "field" : "fields"}}`} ·{" "}
      <span className="text-ink-faint">see yaml</span>
    </span>
  );
}

function omitKey<K extends string>(
  obj: Record<string, unknown>,
  key: K,
): Record<string, unknown> {
  const { [key]: _omit, ...rest } = obj;
  return rest;
}

// ---------------------------------------------------------------------
// YAML tab — minimal renderer for CR YAML
// ---------------------------------------------------------------------

function CustomResourceYamlView(props: CRDetailRefProps) {
  const { cluster, group, version, plural, namespace, name } = props;
  const yamlQuery = useQuery({
    queryKey: ["cr-yaml", cluster, group, version, plural, namespace ?? "", name],
    queryFn: ({ signal }) =>
      api.getCustomResourceYAML(cluster, group, version, plural, namespace, name, signal),
    enabled: Boolean(name),
  });
  const [copied, setCopied] = useState(false);

  if (yamlQuery.isLoading) return <DetailLoading label="loading yaml…" />;
  if (yamlQuery.isError)
    return <DetailError message={(yamlQuery.error as Error)?.message ?? "unknown"} />;
  if (!yamlQuery.data) return null;

  const data = yamlQuery.data;
  const lines = data.replace(/\n+$/, "").split("\n");

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(data);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // clipboard unavailable
    }
  };

  return (
    <div className="relative">
      <div className="pointer-events-none sticky top-0 z-10 flex justify-end">
        <button
          type="button"
          onClick={handleCopy}
          className={cn(
            "pointer-events-auto m-2 inline-flex items-center gap-1.5 rounded-md border bg-surface px-2.5 py-1 font-mono text-[11px] shadow-sm transition-colors",
            copied
              ? "border-green/40 bg-green-soft text-green"
              : "border-border text-ink-muted hover:border-border-strong hover:text-ink",
          )}
        >
          {copied ? "✓ copied" : "copy"}
        </button>
      </div>
      <pre className="grid grid-cols-[auto_1fr] gap-x-4 px-4 pb-5 pt-1 font-mono text-[11.5px] leading-[1.55]">
        {lines.map((line, i) => (
          <span key={i} className="contents">
            <span className="select-none text-right text-ink-faint tabular">
              {i + 1}
            </span>
            <code className="whitespace-pre text-ink">{line || " "}</code>
          </span>
        ))}
      </pre>
    </div>
  );
}

// ---------------------------------------------------------------------
// Events tab — fetches CR-specific events endpoint
// ---------------------------------------------------------------------

function CustomResourceEventsView(props: CRDetailRefProps) {
  const { cluster, group, version, plural, namespace, name } = props;
  const eventsQuery = useQuery({
    queryKey: ["cr-events", cluster, group, version, plural, namespace ?? "", name],
    queryFn: async ({ signal }) => {
      const ns = namespace && namespace.length > 0 ? namespace : "_";
      const url = `/api/clusters/${encodeURIComponent(cluster)}/customresources/${encodeURIComponent(group)}/${encodeURIComponent(version)}/${encodeURIComponent(plural)}/${encodeURIComponent(ns)}/${encodeURIComponent(name)}/events`;
      const res = await fetch(url, { signal });
      if (!res.ok) throw new Error(`events fetch failed: ${res.status}`);
      return res.json() as Promise<{ events: Array<{ type: string; reason: string; message: string; count: number; last: string; source: string }> }>;
    },
    enabled: Boolean(name),
  });
  if (eventsQuery.isLoading) return <DetailLoading label="loading events…" />;
  if (eventsQuery.isError)
    return <DetailError message={(eventsQuery.error as Error)?.message ?? "unknown"} />;
  if (!eventsQuery.data || eventsQuery.data.events.length === 0)
    return <DetailEmpty label="no events for this object" />;

  return (
    <ul className="divide-y divide-border">
      {eventsQuery.data.events.map((ev, i) => {
        const isWarning = ev.type === "Warning";
        return (
          <li key={i} className="px-5 py-3">
            <div className="flex gap-3">
              <span
                className={cn(
                  "mt-1 block size-1.5 shrink-0 rounded-full",
                  isWarning ? "bg-red" : "bg-green",
                )}
              />
              <div className="min-w-0 flex-1">
                <div className="flex flex-wrap items-baseline gap-x-2 gap-y-1">
                  <span
                    className={cn(
                      "font-mono text-[12px] font-medium",
                      isWarning ? "text-red" : "text-ink",
                    )}
                  >
                    {ev.reason}
                  </span>
                  <span className="text-[11px] text-ink-faint">·</span>
                  <span className="text-[11.5px] text-ink-muted">
                    {ageFrom(ev.last)} ago
                  </span>
                  {ev.count > 1 && (
                    <>
                      <span className="text-[11px] text-ink-faint">·</span>
                      <span className="font-mono text-[11.5px] text-ink-muted">
                        ×{ev.count}
                      </span>
                    </>
                  )}
                </div>
                <div className="mt-1 text-[12px] leading-relaxed text-ink">
                  {ev.message}
                </div>
              </div>
            </div>
          </li>
        );
      })}
    </ul>
  );
}
