// Section 1 of the Identity page: aws-auth ↔ Access Entries
// reconciliation with a migration-health chip pinned at the top.
//
// The chip is the headline artifact for operators mid-migration:
// "X aws-auth-only · Y dual · Z entries-only" tells you in one
// glance whether the migration is done. Clicking a segment scopes
// the table below to that source.

import { useMemo, useState } from "react";

import { cn } from "../../lib/cn";
import type { DiffSide } from "../../lib/identity";
import { isAWSForbidden, isAWSThrottled } from "../../lib/api";
import type { AwsAuthDiffResponse } from "../../lib/identity";

type FilterMode = DiffSide | "all" | "diff-only";

export function AccessEntriesSection({
  data,
  isLoading,
  isError,
  error,
}: {
  data: AwsAuthDiffResponse | undefined;
  isLoading: boolean;
  isError: boolean;
  error: unknown;
}) {
  const [filter, setFilter] = useState<FilterMode>("all");

  // Memoize the filtered table to avoid re-running the predicate on
  // every keystroke / sidebar toggle.
  const rows = useMemo(() => {
    if (!data) return [];
    if (filter === "all") return data.entries;
    if (filter === "diff-only")
      return data.entries.filter((r) => r.in !== "both");
    return data.entries.filter((r) => r.in === filter);
  }, [data, filter]);

  if (isError && isAWSForbidden(error)) {
    return (
      <Frame title="Cluster access">
        <p className="text-[13px] text-ink-faint">
          Periscope&apos;s AWS role does not have permission to read
          access entries. Required IAM:{" "}
          <code className="font-mono text-[12px]">
            eks:ListAccessEntries
          </code>
          ,{" "}
          <code className="font-mono text-[12px]">
            eks:DescribeAccessEntry
          </code>
          ,{" "}
          <code className="font-mono text-[12px]">
            eks:ListAssociatedAccessPolicies
          </code>
          .
        </p>
      </Frame>
    );
  }

  if (isError && isAWSThrottled(error)) {
    return (
      <Frame title="Cluster access">
        <p className="text-[13px] text-ink-faint">
          AWS rate-limited this request. Refresh in a moment.
        </p>
      </Frame>
    );
  }

  if (isError) {
    return (
      <Frame title="Cluster access">
        <p className="text-[13px] text-red">
          Failed to load cluster access:{" "}
          {(error as Error)?.message ?? "unknown error"}
        </p>
      </Frame>
    );
  }

  if (isLoading || !data) {
    return (
      <Frame title="Cluster access">
        <Spinner />
      </Frame>
    );
  }

  const { health } = data;
  const total =
    health.awsAuthOnly + health.dual + health.accessEntriesOnly;

  return (
    <Frame
      title="Cluster access"
      subtitle={
        total === 0
          ? "No principals mapped via Access Entries or aws-auth."
          : undefined
      }
    >
      <div className="mb-3 flex flex-wrap items-center gap-2">
        <HealthChip
          tone="red"
          glyph="▲"
          count={health.awsAuthOnly}
          label="aws-auth only"
          active={filter === "aws-auth"}
          onClick={() =>
            setFilter((f) => (f === "aws-auth" ? "all" : "aws-auth"))
          }
        />
        <HealthChip
          tone="yellow"
          glyph="●"
          count={health.dual}
          label="dual"
          active={filter === "both"}
          onClick={() =>
            setFilter((f) => (f === "both" ? "all" : "both"))
          }
        />
        <HealthChip
          tone="green"
          glyph="●"
          count={health.accessEntriesOnly}
          label="entries only"
          active={filter === "access-entries"}
          onClick={() =>
            setFilter((f) =>
              f === "access-entries" ? "all" : "access-entries",
            )
          }
        />

        <button
          type="button"
          onClick={() =>
            setFilter((f) => (f === "diff-only" ? "all" : "diff-only"))
          }
          className={cn(
            "ml-auto rounded-sm border border-border px-2 py-1 text-[11.5px]",
            filter === "diff-only"
              ? "bg-accent-soft text-accent"
              : "text-ink-muted hover:bg-surface-2",
          )}
        >
          {filter === "diff-only" ? "Show all" : "Diff only"}
        </button>
      </div>

      {rows.length === 0 ? (
        <p className="text-[13px] text-ink-faint">
          {filter === "all"
            ? "No principals mapped."
            : "No matching principals."}
        </p>
      ) : (
        <ul className="divide-y divide-border rounded-md border border-border bg-surface">
          {rows.map((row) => (
            <li
              key={row.in + row.principalArn}
              className="flex items-center gap-3 px-3 py-2"
            >
              <DiffBadge side={row.in} />
              <span className="flex-1 truncate font-mono text-[12.5px] text-ink">
                {row.principalArn}
              </span>
              {row.kubernetesGroups && row.kubernetesGroups.length > 0 && (
                <div className="flex flex-wrap items-center gap-1">
                  {row.kubernetesGroups.map((g) => (
                    <span
                      key={g}
                      className="rounded-sm border border-border bg-surface-2 px-1.5 py-0.5 font-mono text-[11px] text-ink-muted"
                    >
                      {g}
                    </span>
                  ))}
                </div>
              )}
            </li>
          ))}
        </ul>
      )}
    </Frame>
  );
}

// ── Local helpers ─────────────────────────────────────────────────

function Frame({
  title,
  subtitle,
  children,
}: {
  title: string;
  subtitle?: string;
  children: React.ReactNode;
}) {
  return (
    <section className="mb-6">
      <header className="mb-2">
        <h2 className="text-[14px] font-medium">{title}</h2>
        {subtitle && (
          <p className="mt-0.5 text-[12px] text-ink-faint">{subtitle}</p>
        )}
      </header>
      {children}
    </section>
  );
}

function HealthChip({
  tone,
  glyph,
  count,
  label,
  active,
  onClick,
}: {
  tone: "red" | "yellow" | "green";
  glyph: string;
  count: number;
  label: string;
  active: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "flex items-center gap-1.5 rounded-sm border px-2 py-0.5 text-[12.5px] transition-colors",
        active
          ? "border-accent bg-accent-soft text-accent"
          : "border-border hover:bg-surface-2",
      )}
      aria-pressed={active}
    >
      <span
        className={cn(
          "font-mono leading-none",
          tone === "red" && "text-red",
          tone === "yellow" && "text-yellow",
          tone === "green" && "text-green",
        )}
        aria-hidden
      >
        {glyph}
      </span>
      <span className="font-mono tabular-nums">{count}</span>
      <span className="text-[11px] text-ink-faint">{label}</span>
    </button>
  );
}

function DiffBadge({ side }: { side: DiffSide }) {
  if (side === "both") {
    return (
      <span className="shrink-0 rounded-sm border border-border bg-surface-2 px-1.5 py-0.5 font-mono text-[10.5px] uppercase tracking-wide text-ink-faint">
        both
      </span>
    );
  }
  if (side === "aws-auth") {
    return (
      <span className="shrink-0 rounded-sm border border-red/30 bg-red/10 px-1.5 py-0.5 font-mono text-[10.5px] uppercase tracking-wide text-red">
        aws-auth
      </span>
    );
  }
  return (
    <span className="shrink-0 rounded-sm border border-green/30 bg-green/10 px-1.5 py-0.5 font-mono text-[10.5px] uppercase tracking-wide text-green">
      entries
    </span>
  );
}

function Spinner() {
  return (
    <span className="block size-4 animate-spin rounded-full border-[1.5px] border-border-strong border-t-accent" />
  );
}
