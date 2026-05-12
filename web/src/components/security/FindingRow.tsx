// FindingRow — collapsible per-finding row inside SecurityTab.
//
// Collapsed: one line summary (CVE, severity, CVSS, EPSS, exploit,
// package → fix). Expanded: description + remediation text +
// vendor link + observed timestamps + Inspector deep-link.
//
// Kept presentation-only: data comes in fully shaped, the row
// doesn't fetch or aggregate.

import { useState } from "react";
import type { CveFinding } from "../../lib/types";
import { cn } from "../../lib/cn";

interface Props {
  finding: CveFinding;
}

export function FindingRow({ finding }: Props) {
  const [expanded, setExpanded] = useState(false);
  const sev = (finding.severity ?? "").toUpperCase();

  return (
    <div className="border-b border-border/60 py-2 font-mono text-[12px] last:border-b-0">
      <button
        type="button"
        onClick={() => setExpanded((v) => !v)}
        className="flex w-full items-start gap-2 text-left transition-colors hover:bg-surface-2/40"
      >
        <span
          className="mt-0.5 inline-block w-3 text-ink-faint"
          aria-hidden
        >
          {expanded ? "▾" : "▸"}
        </span>
        <span className="flex flex-1 flex-wrap items-baseline gap-x-2 gap-y-0.5">
          <span className="text-ink">{finding.cve ?? "(no CVE)"}</span>
          <SeverityPill sev={sev} />
          {finding.cvssV3Score != null && finding.cvssV3Score > 0 && (
            <span className="text-ink-muted">
              cvss {finding.cvssV3Score.toFixed(1)}
            </span>
          )}
          {finding.epssScore != null && finding.epssScore > 0 && (
            <span className="text-ink-muted">
              epss {finding.epssScore.toFixed(2)}
            </span>
          )}
          {finding.exploitAvailable === "YES" && (
            <span className="rounded-sm border border-red/40 bg-red-soft px-1 text-[10px] uppercase tracking-[0.06em] text-red">
              exploit
            </span>
          )}
        </span>
      </button>

      {expanded && (
        <div className="ml-5 mt-1.5 space-y-1.5 text-ink-muted">
          {finding.description && (
            <p className="whitespace-pre-line leading-snug">
              {finding.description}
            </p>
          )}
          {finding.remediation && (
            <p className="leading-snug">
              <span className="text-ink-faint">remediation:</span>{" "}
              {finding.remediation}
              {finding.remediationUrl && (
                <>
                  {" "}
                  <a
                    href={finding.remediationUrl}
                    target="_blank"
                    rel="noreferrer"
                    className="underline decoration-dotted underline-offset-2 hover:text-ink"
                  >
                    advisory ↗
                  </a>
                </>
              )}
            </p>
          )}
          <div className="flex flex-wrap gap-x-3 text-[11px] text-ink-faint">
            {finding.firstObservedAt && (
              <span>first seen {fmtDate(finding.firstObservedAt)}</span>
            )}
            {finding.lastObservedAt && (
              <span>last seen {fmtDate(finding.lastObservedAt)}</span>
            )}
            <a
              href={finding.inspectorUrl}
              target="_blank"
              rel="noreferrer"
              className="underline decoration-dotted underline-offset-2 hover:text-ink"
            >
              open in Inspector ↗
            </a>
          </div>
        </div>
      )}
    </div>
  );
}

function SeverityPill({ sev }: { sev: string }) {
  const cls =
    sev === "CRITICAL"
      ? "border-red/40 bg-red-soft text-red"
      : sev === "HIGH"
        ? "border-yellow/40 bg-yellow/10 text-yellow"
        : sev === "MEDIUM"
          ? "border-border bg-surface-2 text-ink"
          : sev === "LOW"
            ? "border-border bg-surface-2 text-ink-muted"
            : "border-border bg-surface-2 text-ink-faint";
  return (
    <span
      className={cn(
        "rounded-sm border px-1 text-[10px] uppercase tracking-[0.06em]",
        cls,
      )}
    >
      {sev || "—"}
    </span>
  );
}

function fmtDate(s: string): string {
  try {
    const d = new Date(s);
    return d.toLocaleDateString();
  } catch {
    return s;
  }
}
