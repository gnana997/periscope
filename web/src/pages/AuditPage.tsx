import { useEffect, useMemo, useState } from "react";
import { useParams, useSearchParams } from "react-router-dom";
import { useAuth } from "../auth/useAuth";
import { useAudit } from "../hooks/useAudit";
import { PageHeader } from "../components/page/PageHeader";
import { AuditFilterStrip } from "../components/audit/AuditFilterStrip";
import { AuditRow } from "../components/audit/AuditRow";
import { AuditTimelineStrip } from "../components/audit/AuditTimelineStrip";
import { ScopeBanner } from "../components/audit/ScopeBanner";
import {
  AuditNotEnabledState,
  AuditNoEventsYetState,
  AuditEmptyFilteredState,
} from "../components/audit/AuditEmptyStates";
import { LoadingState, ErrorState } from "../components/table/states";
import type { AuditQueryParams } from "../lib/types";

const PAGE_SIZE = 50;
const DEFAULT_RANGE_MINUTES = 60;

/**
 * AuditPage — /clusters/:cluster/audit
 *
 * Per-cluster audit history. Cluster filter is locked to :cluster from
 * the URL; everything else is user-controlled and serializes to query
 * params for shareable URLs.
 *
 * State branches:
 *   user.auditEnabled === false → AuditNotEnabledState
 *   query.isPending → LoadingState
 *   query.isError → ErrorState
 *   data.total === 0 + no filters → AuditNoEventsYetState
 *   data.total === 0 + filters    → AuditEmptyFilteredState (with clear)
 *   data.total > 0 → results
 */
export function AuditPage() {
  const { cluster } = useParams<{ cluster: string }>();
  const { user } = useAuth();
  const [searchParams, setSearchParams] = useSearchParams();

  // Default time range: last hour. Set on mount if no `from` is present.
  useEffect(() => {
    if (searchParams.get("from")) return;
    const to = new Date();
    const from = new Date(to.getTime() - DEFAULT_RANGE_MINUTES * 60_000);
    const next = new URLSearchParams(searchParams);
    next.set("from", from.toISOString());
    next.set("to", to.toISOString());
    setSearchParams(next, { replace: true });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const filters = useMemo(
    () => paramsFromSearch(searchParams, cluster),
    [searchParams, cluster],
  );

  const auditEnabled = user?.auditEnabled !== false;
  const showActorFilter = user?.auditScope === "all";

  const query = useAudit(filters, Boolean(cluster) && auditEnabled);

  // Page is reset to 0 inside handleFiltersChange whenever filters
  // change, which means we can avoid an effect-driven sync (and the
  // cascading-render lint warning) entirely. Browser back/forward to
  // a previous URL with filters preserves the filter state but
  // resets pagination — acceptable for v1.
  const [page, setPage] = useState(0);

  // Re-derive query params with the page applied (useAudit consumes the
  // full param object).
  const pagedFilters = useMemo(
    () => ({ ...filters, limit: PAGE_SIZE, offset: page * PAGE_SIZE }),
    [filters, page],
  );
  const pagedQuery = useAudit(pagedFilters, Boolean(cluster) && auditEnabled);

  if (!auditEnabled) {
    return (
      <div className="flex flex-1 flex-col">
        <PageHeader title="Audit" />
        <AuditNotEnabledState />
      </div>
    );
  }

  if (pagedQuery.isPending) {
    return (
      <div className="flex flex-1 flex-col">
        <PageHeader title="Audit" />
        <LoadingState resource="audit" />
      </div>
    );
  }

  if (pagedQuery.isError) {
    return (
      <div className="flex flex-1 flex-col">
        <PageHeader title="Audit" />
        <ErrorState
          title="couldn't load audit log"
          message={(pagedQuery.error as Error)?.message ?? "unknown error"}
        />
      </div>
    );
  }

  const data = pagedQuery.data;
  const total = data?.total ?? 0;
  const items = data?.items ?? [];
  const hasFilters = Boolean(
    filters.outcome ||
      filters.verb ||
      filters.actor ||
      filters.namespace ||
      filters.name ||
      filters.requestId,
  );

  // Use the unscoped query (no pagination) for the timeline density
  // strip — we want the full picture across the time range, not just
  // the current page.
  const allInRange = query.data?.items ?? items;
  const totalInRange = query.data?.total ?? total;
  const deniedCount = (query.data?.items ?? []).filter(
    (r) => r.outcome === "denied",
  ).length;

  const subtitle = makeSubtitle(totalInRange, deniedCount, filters);

  const handleClearFilters = () => {
    const next = new URLSearchParams();
    if (filters.from) next.set("from", filters.from);
    if (filters.to) next.set("to", filters.to);
    setSearchParams(next, { replace: true });
  };

  const handleFiltersChange = (next: AuditQueryParams) => {
    const params = new URLSearchParams();
    for (const [k, v] of Object.entries(next)) {
      if (v === undefined || v === null || v === "" || k === "cluster") continue;
      params.set(k, String(v));
    }
    setSearchParams(params, { replace: true });
    // Reset pagination when filters change so the user doesn't land
    // on an empty page after narrowing the result set.
    setPage(0);
  };

  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));
  const showDenialCallout =
    !filters.outcome && deniedCount > 0 && allInRange.length > 0;

  return (
    <div className="flex flex-1 flex-col">
      <PageHeader title="Audit" subtitle={subtitle} />
      <AuditFilterStrip
        filters={filters}
        onChange={handleFiltersChange}
        showActorFilter={showActorFilter}
      />

      <div className="flex flex-1 flex-col gap-4 px-6 py-5">
        {user?.auditScope === "self" && <ScopeBanner />}

        {allInRange.length > 0 && (
          <AuditTimelineStrip
            rows={allInRange}
            from={filters.from}
            to={filters.to}
          />
        )}

        {showDenialCallout && (
          <button
            type="button"
            onClick={() =>
              handleFiltersChange({ ...filters, outcome: "denied" })
            }
            className="flex items-center gap-2 self-start rounded-md border border-red/40 bg-red-soft/40 px-3 py-1.5 text-[12px] text-red hover:border-red"
          >
            <span aria-hidden>⚠</span>
            <span>
              {deniedCount} denial{deniedCount === 1 ? "" : "s"} in this window —
              show denials only
            </span>
          </button>
        )}

        {total === 0 && hasFilters && (
          <AuditEmptyFilteredState onClear={handleClearFilters} />
        )}
        {total === 0 && !hasFilters && <AuditNoEventsYetState />}

        {items.length > 0 && (
          <div className="flex flex-col gap-1.5">
            {items.map((row) => (
              <AuditRow key={row.id} row={row} />
            ))}
          </div>
        )}

        {total > PAGE_SIZE && (
          <Pagination
            page={page}
            pageCount={totalPages}
            total={total}
            pageSize={PAGE_SIZE}
            onChange={setPage}
          />
        )}
      </div>
    </div>
  );
}

function paramsFromSearch(
  sp: URLSearchParams,
  cluster: string | undefined,
): AuditQueryParams {
  return {
    cluster,
    from: sp.get("from") || undefined,
    to: sp.get("to") || undefined,
    actor: sp.get("actor") || undefined,
    verb: sp.get("verb") || undefined,
    outcome: (sp.get("outcome") as AuditQueryParams["outcome"]) || undefined,
    namespace: sp.get("namespace") || undefined,
    name: sp.get("name") || undefined,
    requestId: sp.get("requestId") || undefined,
  };
}

function makeSubtitle(
  total: number,
  denied: number,
  filters: AuditQueryParams,
): string {
  const fromLabel = filters.from
    ? new Date(filters.from).toLocaleTimeString("en-GB", {
        hour12: false,
        hour: "2-digit",
        minute: "2-digit",
      })
    : null;

  const head =
    total === 0
      ? "No entries"
      : total === 1
        ? "One entry"
        : numberWord(total) + " entries";

  const tail = denied
    ? ` — ${denied === 1 ? "one of them denied" : numberWord(denied) + " of them denied"}`
    : "";

  if (fromLabel) {
    return `${head} since ${fromLabel}${tail}.`;
  }
  return `${head}${tail}.`;
}

function numberWord(n: number): string {
  // Editorial formatting for small numbers, mono digits for the rest.
  // Matches Fleet's "Six clusters under command." voice when feasible.
  const small: Record<number, string> = {
    2: "Two", 3: "Three", 4: "Four", 5: "Five", 6: "Six",
    7: "Seven", 8: "Eight", 9: "Nine", 10: "Ten",
    11: "Eleven", 12: "Twelve",
  };
  if (n in small) return small[n];
  return String(n);
}

function Pagination({
  page,
  pageCount,
  total,
  pageSize,
  onChange,
}: {
  page: number;
  pageCount: number;
  total: number;
  pageSize: number;
  onChange: (n: number) => void;
}) {
  const start = page * pageSize + 1;
  const end = Math.min(total, (page + 1) * pageSize);
  const pages = pageRange(page, pageCount);

  return (
    <nav
      aria-label="Audit pagination"
      className="flex items-center justify-center gap-3 border-t border-border pt-4 font-mono text-[11.5px]"
    >
      <span className="text-ink-faint tabular">
        Showing {start}–{end} of {total}
      </span>
      <span className="text-ink-faint">·</span>
      <button
        type="button"
        onClick={() => onChange(Math.max(0, page - 1))}
        disabled={page === 0}
        className="text-ink-muted hover:text-ink disabled:cursor-not-allowed disabled:text-ink-faint"
      >
        ‹ prev
      </button>
      <span className="text-ink-faint">·</span>
      {pages.map((p, i) =>
        p === "…" ? (
          <span key={`gap-${i}`} className="text-ink-faint">
            …
          </span>
        ) : (
          <button
            key={p}
            type="button"
            onClick={() => onChange(p)}
            className={
              p === page
                ? "rounded bg-accent-soft px-1.5 text-accent"
                : "px-1.5 text-ink-muted hover:text-ink"
            }
            aria-current={p === page ? "page" : undefined}
          >
            {p + 1}
          </button>
        ),
      )}
      <span className="text-ink-faint">·</span>
      <button
        type="button"
        onClick={() => onChange(Math.min(pageCount - 1, page + 1))}
        disabled={page >= pageCount - 1}
        className="text-ink-muted hover:text-ink disabled:cursor-not-allowed disabled:text-ink-faint"
      >
        next ›
      </button>
    </nav>
  );
}

/** ABRIDGED page list: 1 2 … 5 6 7 … 99 100 */
function pageRange(current: number, total: number): Array<number | "…"> {
  if (total <= 7) {
    return Array.from({ length: total }, (_, i) => i);
  }
  const out: Array<number | "…"> = [0, 1];
  if (current > 3) out.push("…");
  const start = Math.max(2, current - 1);
  const end = Math.min(total - 3, current + 1);
  for (let i = start; i <= end; i++) out.push(i);
  if (current < total - 4) out.push("…");
  out.push(total - 2, total - 1);
  return out;
}
