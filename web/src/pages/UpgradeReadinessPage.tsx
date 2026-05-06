import { useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { useUpgradeInsight, useUpgradeInsights } from "../hooks/useUpgradeInsights";
import { isAWSForbidden, isAWSThrottled, isBackendNotEKS } from "../lib/api";
import { cn } from "../lib/cn";
import type {
  UpgradeInsightStatus,
  UpgradeInsightSummary,
} from "../lib/types";
import { sortInsightsByStatus } from "../lib/upgradeInsights";

// UpgradeReadinessPage — the dedicated tab/page for EKS Upgrade
// Insights.
//
// Layout:
//   - Header: target K8s version + three-bucket count.
//   - Body: insight rows sorted ERROR → WARNING → UNKNOWN → PASSING
//           (worst-first, since the operator opens this page when
//           planning an upgrade and wants the blockers up top).
//   - Each row is expandable. Expanded body shows description,
//     recommendation, and the affected-resources list with deep
//     links to Periscope's editor.
//
// Empty states:
//   - Non-EKS cluster (HTTP 422 with E_BACKEND_NOT_EKS) → renders
//     the issue's prescribed one-line note rather than a generic
//     error banner.
//   - No insights returned → "no upgrade readiness checks have run
//     yet" with a hint that AWS refreshes daily.

export function UpgradeReadinessPage({ cluster }: { cluster: string }) {
  const { data, isLoading, isError, error } = useUpgradeInsights(cluster);
  const sorted = useMemo(
    () => sortInsightsByStatus(data?.insights ?? []),
    [data?.insights],
  );

  if (isError && isBackendNotEKS(error)) {
    return (
      <div className="px-6 py-8">
        <h1 className="mb-2 text-[16px] font-medium">Upgrade readiness</h1>
        <p className="text-[13px] text-ink-faint">
          Upgrade insights are an EKS feature; this cluster is not
          backed by EKS, so no upgrade-readiness data is available.
        </p>
      </div>
    );
  }

  if (isError && isAWSForbidden(error)) {
    return (
      <div className="px-6 py-8">
        <h1 className="mb-2 text-[16px] font-medium">Upgrade readiness</h1>
        <p className="text-[13px] text-ink-faint">
          Periscope's AWS role does not have permission to read upgrade
          insights for this cluster. Required IAM actions:{" "}
          <code className="font-mono text-[12px]">eks:ListInsights</code>{" "}
          and{" "}
          <code className="font-mono text-[12px]">eks:DescribeInsight</code>.
          See{" "}
          <code className="font-mono text-[12px]">docs/setup/deploy.md</code>{" "}
          for the full permission matrix.
        </p>
      </div>
    );
  }

  if (isError && isAWSThrottled(error)) {
    return (
      <div className="px-6 py-8">
        <h1 className="mb-2 text-[16px] font-medium">Upgrade readiness</h1>
        <p className="text-[13px] text-ink-faint">
          AWS rate-limited this request. Refresh the page in a moment;
          the cache will absorb subsequent calls.
        </p>
      </div>
    );
  }

  if (isLoading) {
    return (
      <div className="flex h-full items-center justify-center">
        <span className="block size-4 animate-spin rounded-full border-[1.5px] border-border-strong border-t-accent" />
      </div>
    );
  }

  if (isError) {
    return (
      <div className="px-6 py-8">
        <h1 className="mb-2 text-[16px] font-medium">Upgrade readiness</h1>
        <p className="text-[13px] text-red">
          Failed to load upgrade insights:{" "}
          {(error as Error)?.message ?? "unknown error"}.
        </p>
      </div>
    );
  }

  if (!data) return null;

  return (
    <div className="flex h-full min-h-0 flex-col overflow-y-auto px-6 py-5">
      <header className="mb-4 flex items-center justify-between">
        <div>
          <h1 className="text-[16px] font-medium">Upgrade readiness</h1>
          {data.targetKubernetesVersion && (
            <p className="mt-0.5 text-[12px] text-ink-faint">
              Target Kubernetes version: {data.targetKubernetesVersion}
            </p>
          )}
        </div>
        <CountsHeader
          passing={data.counts.passing}
          warning={data.counts.warning}
          error={data.counts.error}
          unknown={data.counts.unknown}
        />
      </header>

      {sorted.length === 0 ? (
        <p className="text-[13px] text-ink-faint">
          No upgrade-readiness checks have produced findings on this cluster.
          AWS refreshes upgrade insights once per day; the absence of rows
          here means EKS has not flagged any issues.
        </p>
      ) : (
        <ul className="divide-y divide-border rounded-md border border-border bg-surface">
          {sorted.map((row) => (
            <li key={row.id}>
              <InsightRow cluster={cluster} insight={row} />
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

// ── Header ───────────────────────────────────────────────────────────

function CountsHeader({
  passing,
  warning,
  error,
  unknown,
}: {
  passing: number;
  warning: number;
  error: number;
  unknown: number;
}) {
  return (
    <div className="flex items-center gap-3 text-[13px]">
      <CountChip glyph="●" tone="green" count={passing} label="passing" />
      <CountChip glyph="▲" tone="yellow" count={warning} label="warning" />
      <CountChip glyph="✕" tone="red" count={error} label="error" />
      {unknown > 0 && (
        <CountChip glyph="?" tone="faint" count={unknown} label="unknown" />
      )}
    </div>
  );
}

function CountChip({
  glyph,
  tone,
  count,
  label,
}: {
  glyph: string;
  tone: "green" | "yellow" | "red" | "faint";
  count: number;
  label: string;
}) {
  return (
    <div className="flex items-center gap-1.5 rounded-sm border border-border px-2 py-0.5">
      <span
        className={cn(
          "font-mono leading-none",
          tone === "green" && "text-green",
          tone === "yellow" && "text-yellow",
          tone === "red" && "text-red",
          tone === "faint" && "text-ink-faint",
        )}
        aria-hidden
      >
        {glyph}
      </span>
      <span className="font-mono tabular-nums">{count}</span>
      <span className="text-[11px] text-ink-faint">{label}</span>
    </div>
  );
}

// ── Rows ─────────────────────────────────────────────────────────────

function InsightRow({
  cluster,
  insight,
}: {
  cluster: string;
  insight: UpgradeInsightSummary;
}) {
  const [open, setOpen] = useState(false);
  return (
    <div>
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center gap-3 px-3 py-2 text-left transition-colors hover:bg-surface-2"
      >
        <StatusGlyph status={insight.status} />
        <div className="flex-1 truncate">
          <div className="text-[13px]">{insight.name || insight.id}</div>
          {insight.kubernetesVersion && (
            <div className="text-[11px] text-ink-faint">
              for Kubernetes {insight.kubernetesVersion}
            </div>
          )}
        </div>
        <span className="font-mono text-[10px] uppercase tracking-[0.08em] text-ink-faint">
          {open ? "hide" : "details"}
        </span>
      </button>
      {open && (
        <div className="border-t border-border bg-surface-2/40 px-6 py-3">
          <InsightDetailBody cluster={cluster} insightId={insight.id} />
        </div>
      )}
    </div>
  );
}

function StatusGlyph({ status }: { status: UpgradeInsightStatus }) {
  const map = {
    PASSING: { g: "●", c: "text-green" },
    WARNING: { g: "▲", c: "text-yellow" },
    ERROR: { g: "✕", c: "text-red" },
    UNKNOWN: { g: "?", c: "text-ink-faint" },
  } as const;
  const v = map[status] ?? map.UNKNOWN;
  return (
    <span
      className={cn("font-mono text-[14px] leading-none shrink-0", v.c)}
      aria-label={status.toLowerCase()}
    >
      {v.g}
    </span>
  );
}

function InsightDetailBody({
  cluster,
  insightId,
}: {
  cluster: string;
  insightId: string;
}) {
  const { data, isLoading, isError, error } = useUpgradeInsight(cluster, insightId);
  if (isLoading) {
    return <p className="text-[12px] italic text-ink-faint">Loading details…</p>;
  }
  if (isError) {
    return (
      <p className="text-[12px] text-red">
        Failed to load insight: {(error as Error)?.message ?? "unknown error"}.
      </p>
    );
  }
  if (!data) return null;
  return (
    <div className="space-y-3">
      {data.description && (
        <Section label="Description">
          <p className="whitespace-pre-wrap text-[12.5px] leading-relaxed">
            {data.description}
          </p>
        </Section>
      )}
      {data.recommendation && (
        <Section label="Recommendation">
          <p className="whitespace-pre-wrap text-[12.5px] leading-relaxed">
            {data.recommendation}
          </p>
        </Section>
      )}
      {data.deprecationDetails && data.deprecationDetails.length > 0 && (
        <Section label="Deprecated APIs">
          <ul className="space-y-1.5 text-[12px]">
            {data.deprecationDetails.map((d, i) => (
              <li key={i} className="font-mono text-ink-muted">
                <span>{d.usage ?? "(unknown usage)"}</span>
                {d.replacedWith && (
                  <>
                    {" → "}
                    <span className="text-green">{d.replacedWith}</span>
                  </>
                )}
                {d.stopServingVersion && (
                  <span className="ml-2 text-red">
                    stops in {d.stopServingVersion}
                  </span>
                )}
              </li>
            ))}
          </ul>
        </Section>
      )}
      <Section label={`Affected resources (${data.resources.length})`}>
        {data.resources.length === 0 ? (
          <p className="text-[12px] italic text-ink-faint">
            No specific resources flagged. The insight applies cluster-wide
            (e.g. add-on compatibility, IAM, networking).
          </p>
        ) : (
          <ul className="space-y-1 font-mono text-[11.5px]">
            {data.resources.map((r, i) => (
              <li key={i} className="break-all">
                {r.editorPath ? (
                  <Link
                    to={r.editorPath}
                    className="text-accent underline-offset-2 hover:underline"
                  >
                    {r.kubernetesResourceUri}
                  </Link>
                ) : (
                  <span
                    className="text-ink-faint"
                    title="could not map this URI to an editor route"
                  >
                    {r.kubernetesResourceUri}
                  </span>
                )}
              </li>
            ))}
          </ul>
        )}
      </Section>
      {data.additionalInfo && Object.keys(data.additionalInfo).length > 0 && (
        <Section label="Further reading">
          <ul className="space-y-1 text-[11.5px]">
            {Object.entries(data.additionalInfo).map(([k, v]) => (
              <li key={k}>
                <a
                  href={v}
                  target="_blank"
                  rel="noreferrer"
                  className="text-accent underline-offset-2 hover:underline"
                >
                  {k}
                </a>
              </li>
            ))}
          </ul>
        </Section>
      )}
    </div>
  );
}

function Section({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <div>
      <div className="mb-1 font-mono text-[10px] uppercase tracking-[0.08em] text-ink-faint">
        {label}
      </div>
      {children}
    </div>
  );
}

