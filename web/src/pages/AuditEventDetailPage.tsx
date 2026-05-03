import { useMemo, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { useAudit } from "../hooks/useAudit";
import { PageHeader } from "../components/page/PageHeader";
import { LoadingState, ErrorState } from "../components/table/states";
import type { AuditOutcome } from "../lib/types";

const OUTCOME_BADGE: Record<
  AuditOutcome,
  { glyph: string; label: string; color: string }
> = {
  success: { glyph: "●", label: "success", color: "text-green" },
  failure: { glyph: "▲", label: "failure", color: "text-yellow" },
  denied: { glyph: "✕", label: "denied", color: "text-red" },
};

/**
 * AuditEventDetailPage — /clusters/:cluster/audit/:eventId
 *
 * Full-page detail for a single audit row. Renders sections for
 * actor, action, reason, request, and verb-specific extra fields
 * (pretty-printed JSON). Bottom toolbar offers filter pivots and a
 * permalink that encodes the event's request_id.
 *
 * v1 fetches by request_id filter rather than a dedicated /api/audit/:id
 * endpoint — keeps the backend surface small. Falls back to a
 * not-found state if the row has no request_id (older events) or the
 * id is missing from the result set.
 */
export function AuditEventDetailPage() {
  const { cluster, eventId } = useParams<{ cluster: string; eventId: string }>();
  const navigate = useNavigate();

  // Fetch a wide window so we can resolve the event by id. In practice
  // the page is always entered via a click on AuditRow so the event is
  // recent — last 7 days covers the common case. If the event is older,
  // the user gets a clean "not found" state.
  const [sevenDaysAgo] = useState(() =>
    new Date(Date.now() - 7 * 24 * 60 * 60 * 1000).toISOString(),
  );
  const query = useAudit(
    { cluster, from: sevenDaysAgo, limit: 500 },
    Boolean(cluster && eventId),
  );

  const row = useMemo(() => {
    if (!query.data || !eventId) return undefined;
    const id = Number(eventId);
    return query.data.items.find((r) => r.id === id);
  }, [query.data, eventId]);

  if (query.isPending) {
    return (
      <div className="flex flex-1 flex-col">
        <PageHeader title="Event" />
        <LoadingState resource="event" />
      </div>
    );
  }

  if (query.isError) {
    return (
      <div className="flex flex-1 flex-col">
        <PageHeader title="Event" />
        <ErrorState
          title="couldn't load event"
          message={(query.error as Error)?.message ?? "unknown error"}
        />
      </div>
    );
  }

  if (!row) {
    return (
      <div className="flex flex-1 flex-col">
        <PageHeader title="Event" />
        <NotFound cluster={cluster} />
      </div>
    );
  }

  const badge = OUTCOME_BADGE[row.outcome] ?? OUTCOME_BADGE.failure;
  const auditBase = `/clusters/${encodeURIComponent(cluster ?? "")}/audit`;

  const filterPivot = (params: Record<string, string>) => {
    const sp = new URLSearchParams(params);
    navigate(`${auditBase}?${sp.toString()}`);
  };

  return (
    <div className="flex flex-1 flex-col">
      <PageHeader title="Event" />

      <div className="flex flex-1 flex-col gap-6 px-6 py-5">
        {/* breadcrumb back to list */}
        <Link
          to={auditBase}
          className="self-start font-mono text-[11.5px] text-ink-muted hover:text-ink"
        >
          ← back to audit log
        </Link>

        {/* header card */}
        <header className="flex flex-col gap-3 rounded-md border border-border bg-surface px-5 py-4">
          <div className="flex items-baseline gap-3">
            <span className={`text-[26px] leading-none ${badge.color}`} aria-hidden>
              {badge.glyph}
            </span>
            <h2
              className="font-display text-[28px] leading-none tracking-[-0.01em] text-ink"
              style={{ fontWeight: 400 }}
            >
              {row.verb}
            </h2>
            <span className={`font-mono text-[12px] uppercase tracking-wide ${badge.color}`}>
              {badge.label}
            </span>
          </div>
          <div className="font-mono text-[12px] text-ink-muted">
            {formatFullTime(row.timestamp)} · by{" "}
            <span className="text-ink">{row.actor.email || row.actor.sub}</span>
          </div>
        </header>

        {/* sections grid */}
        <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
          <Section title="Actor">
            <Field label="sub" value={row.actor.sub} mono />
            {row.actor.email && <Field label="email" value={row.actor.email} />}
            {row.actor.groups && row.actor.groups.length > 0 && (
              <Field label="groups" value={row.actor.groups.join(", ")} mono />
            )}
          </Section>

          <Section title="Target">
            {row.cluster && <Field label="cluster" value={row.cluster} mono />}
            {row.resource.namespace && (
              <Field label="namespace" value={row.resource.namespace} mono />
            )}
            {(row.resource.group || row.resource.version || row.resource.resource) && (
              <Field
                label="kind"
                mono
                value={[
                  row.resource.group,
                  row.resource.version,
                  row.resource.resource,
                ]
                  .filter(Boolean)
                  .join("/") || "—"}
              />
            )}
            {row.resource.name && (
              <Field label="name" value={row.resource.name} mono />
            )}
          </Section>

          {row.reason && (
            <Section title="Reason" className="lg:col-span-2">
              <p className="text-[12.5px] italic text-ink-muted">
                {row.reason}
              </p>
            </Section>
          )}

          <Section title="Request">
            {row.requestId && (
              <Field label="id" value={row.requestId} mono />
            )}
            {row.route && <Field label="route" value={row.route} mono />}
          </Section>

          {row.extra && Object.keys(row.extra).length > 0 && (
            <Section title="Extra (verb-specific)" className="lg:col-span-2">
              <pre className="overflow-x-auto rounded border border-border bg-bg/50 p-3 font-mono text-[11.5px] text-ink-muted">
                {JSON.stringify(row.extra, null, 2)}
              </pre>
            </Section>
          )}
        </div>

        {/* actions toolbar */}
        <div className="flex flex-wrap gap-2 border-t border-border pt-4">
          <ActionLink
            onClick={() =>
              filterPivot({ actor: row.actor.email || row.actor.sub })
            }
          >
            → filter: this actor
          </ActionLink>
          <ActionLink onClick={() => filterPivot({ verb: row.verb })}>
            → filter: this verb
          </ActionLink>
          <ActionLink
            onClick={() => filterPivot({ outcome: row.outcome })}
          >
            → filter: this outcome
          </ActionLink>
          {row.requestId && (
            <ActionLink onClick={() => filterPivot({ requestId: row.requestId! })}>
              ⌘ permalink
            </ActionLink>
          )}
          <CopyButton value={JSON.stringify(row, null, 2)} />
        </div>
      </div>
    </div>
  );
}

function Section({
  title,
  children,
  className,
}: {
  title: string;
  children: React.ReactNode;
  className?: string;
}) {
  return (
    <section
      className={`flex flex-col gap-2 rounded-md border border-border bg-surface px-4 py-3 ${className ?? ""}`}
    >
      <h3 className="font-mono text-[10.5px] uppercase tracking-[0.18em] text-ink-faint">
        {title}
      </h3>
      <div className="flex flex-col gap-1">{children}</div>
    </section>
  );
}

function Field({
  label,
  value,
  mono,
}: {
  label: string;
  value: string;
  mono?: boolean;
}) {
  return (
    <div className="grid grid-cols-[80px_1fr] items-baseline gap-3 text-[12px]">
      <span className="font-mono text-[10.5px] uppercase tracking-wide text-ink-faint">
        {label}
      </span>
      <span
        className={
          mono
            ? "font-mono text-[12px] text-ink"
            : "text-[12.5px] text-ink"
        }
      >
        {value}
      </span>
    </div>
  );
}

function ActionLink({
  onClick,
  children,
}: {
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="rounded-md border border-border bg-surface px-3 py-1.5 font-mono text-[11.5px] text-ink-muted hover:border-border-strong hover:text-ink"
    >
      {children}
    </button>
  );
}

function CopyButton({ value }: { value: string }) {
  return (
    <button
      type="button"
      onClick={() => {
        void navigator.clipboard?.writeText(value);
      }}
      className="rounded-md border border-border bg-surface px-3 py-1.5 font-mono text-[11.5px] text-ink-muted hover:border-border-strong hover:text-ink"
    >
      ⎘ copy as JSON
    </button>
  );
}

function NotFound({ cluster }: { cluster?: string }) {
  return (
    <div className="flex flex-1 flex-col items-center justify-center gap-3 px-6 py-16 text-center">
      <h3
        className="font-display text-[28px] leading-none tracking-[-0.01em] text-ink-muted"
        style={{ fontWeight: 400, fontStyle: "italic" }}
      >
        event not found
      </h3>
      <p className="max-w-md text-[12.5px] text-ink-muted">
        Either the event is older than 7 days (outside the lookup window),
        or it was pruned by the retention policy.
      </p>
      {cluster && (
        <Link
          to={`/clusters/${encodeURIComponent(cluster)}/audit`}
          className="text-[12px] text-accent hover:underline"
        >
          ← back to audit log
        </Link>
      )}
    </div>
  );
}

function formatFullTime(ts: string): string {
  const t = new Date(ts);
  if (Number.isNaN(t.getTime())) return ts;
  const sec = Math.max(0, Math.round((Date.now() - t.getTime()) / 1000));
  const rel =
    sec < 60
      ? `${sec}s ago`
      : sec < 3600
        ? `${Math.round(sec / 60)}m ago`
        : sec < 86400
          ? `${Math.round(sec / 3600)}h ago`
          : `${Math.round(sec / 86400)}d ago`;
  return `${t.toISOString().replace("T", " ").replace(/\.\d+Z$/, "")} (${rel})`;
}
