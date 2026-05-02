import { useMemo } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { useCRDs } from "../hooks/useResource";
import { ageFrom, nameMatches } from "../lib/format";
import { cn } from "../lib/cn";
import { PageHeader } from "../components/page/PageHeader";
import { ErrorState, ForbiddenState, LoadingState, isForbidden } from "../components/table/states";
import type { CRD } from "../lib/types";

/**
 * CRDsPage — the catalog of every CustomResourceDefinition installed
 * on the cluster. Operators land here when they click "Custom
 * Resources" in the sidebar; each row drills into a CR list view for
 * that kind.
 *
 * CRDs are grouped by API group (cert-manager.io, argoproj.io, …) so
 * users can navigate by ecosystem rather than scrolling a flat list.
 * Filter input is fuzzy across kind, plural, and group — operators
 * search by what they remember.
 */
export function CRDsPage({ cluster }: { cluster: string }) {
  const [params, setParams] = useSearchParams();
  const search = params.get("q") ?? "";
  const navigate = useNavigate();

  const { data, isLoading, isError, error } = useCRDs(cluster);
  const crds = data?.crds ?? [];

  const filtered = useMemo(() => {
    if (!search) return crds;
    return crds.filter(
      (c) =>
        nameMatches(c.kind, search) ||
        nameMatches(c.plural, search) ||
        nameMatches(c.group, search),
    );
  }, [crds, search]);

  const grouped = useMemo(() => {
    const map = new Map<string, CRD[]>();
    for (const c of filtered) {
      const list = map.get(c.group) ?? [];
      list.push(c);
      map.set(c.group, list);
    }
    return [...map.entries()].sort((a, b) => a[0].localeCompare(b[0]));
  }, [filtered]);

  const setParam = (key: string, value: string | null) => {
    const next = new URLSearchParams(params);
    if (value === null || value === "") next.delete(key);
    else next.set(key, value);
    setParams(next, { replace: true });
  };

  const openCRD = (c: CRD) => {
    navigate(
      `/clusters/${encodeURIComponent(cluster)}/customresources/${encodeURIComponent(c.group)}/${encodeURIComponent(c.servedVersion)}/${encodeURIComponent(c.plural)}`,
    );
  };

  return (
    <div className="flex h-full min-h-0 flex-col">
      <PageHeader
        title="Custom Resources"
        subtitle={
          data ? `${crds.length} ${crds.length === 1 ? "CRD" : "CRDs"} installed` : undefined
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
            placeholder="filter by kind, plural, or API group"
            className="min-w-0 flex-1 bg-transparent font-mono text-[12.5px] text-ink outline-none placeholder:text-ink-faint"
            spellCheck={false}
            autoComplete="off"
          />
        </div>
        <span className="font-mono text-[11px] tabular-nums text-ink-muted">
          {filtered.length}
          <span className="text-ink-faint"> / </span>
          {crds.length}
        </span>
      </div>

      <div className="flex-1 overflow-auto [scrollbar-gutter:stable]">
        {isLoading ? (
          <LoadingState resource="custom resource definitions" />
        ) : isError ? (
          isForbidden(error) ? (
            <ForbiddenState resource="custom resource definitions" />
          ) : (
          <ErrorState
            title="couldn't load CRDs"
            message={(error as Error)?.message ?? "unknown"}
          />
          )
        ) : grouped.length === 0 ? (
          <div className="flex h-full items-center justify-center px-6 text-center">
            <div>
              <p className="font-mono text-[13px] text-ink-muted">
                {crds.length === 0
                  ? "no CRDs installed on this cluster"
                  : `no matches for "${search}"`}
              </p>
              <p className="mt-2 font-mono text-[11px] text-ink-faint">
                Custom resources appear here when an operator (cert-manager,
                ArgoCD, Istio, etc.) installs CRDs.
              </p>
            </div>
          </div>
        ) : (
          <div className="space-y-6 px-6 py-5">
            {grouped.map(([group, items]) => (
              <section key={group}>
                <div className="mb-2 flex items-baseline gap-2">
                  <h2 className="font-mono text-[12px] text-ink">{group}</h2>
                  <span className="font-mono text-[10.5px] text-ink-faint">
                    {items.length}
                  </span>
                </div>
                <div className="grid grid-cols-1 gap-2 md:grid-cols-2 xl:grid-cols-3">
                  {items.map((c) => (
                    <CRDCard key={c.name} crd={c} onOpen={() => openCRD(c)} />
                  ))}
                </div>
              </section>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

function CRDCard({ crd, onOpen }: { crd: CRD; onOpen: () => void }) {
  const versions = crd.versions
    .map((v) => v.name + (v.deprecated ? "*" : ""))
    .join(", ");
  return (
    <button
      type="button"
      onClick={onOpen}
      className="group flex flex-col items-start rounded-md border border-border bg-surface px-3 py-2.5 text-left transition-colors hover:border-border-strong hover:bg-surface-2/40"
    >
      <div className="flex w-full items-baseline gap-2">
        <span className="font-mono text-[13px] font-medium text-ink">
          {crd.kind}
        </span>
        <span
          className={cn(
            "rounded-sm border px-1 py-px font-mono text-[9.5px] uppercase tracking-[0.04em]",
            crd.scope === "Namespaced"
              ? "border-border bg-surface-2/60 text-ink-faint"
              : "border-accent/30 bg-accent-soft text-accent",
          )}
        >
          {crd.scope === "Namespaced" ? "ns" : "cluster"}
        </span>
        <span className="ml-auto font-mono text-[10.5px] text-ink-faint">
          {ageFrom(crd.createdAt)}
        </span>
      </div>
      <span className="mt-0.5 truncate font-mono text-[10.5px] text-ink-faint">
        {crd.plural}
        {crd.shortNames && crd.shortNames.length > 0 && (
          <span className="ml-1 text-ink-faint/70">
            ({crd.shortNames.join(", ")})
          </span>
        )}
      </span>
      <span className="mt-1.5 font-mono text-[10.5px] text-ink-muted">
        v: {versions}
      </span>
    </button>
  );
}
