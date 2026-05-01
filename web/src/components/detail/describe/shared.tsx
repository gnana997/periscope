import { Link } from "react-router-dom";
import type { ReactNode } from "react";
import { ageFrom } from "../../../lib/format";
import { cn } from "../../../lib/cn";
import type { JobChildPod } from "../../../lib/types";

// ---------- KV (label + value rows) ----------

/** Two-column key/value row used in the metadata sections. */
export function KV({
  label,
  children,
  mono = false,
}: {
  label: string;
  children: ReactNode;
  mono?: boolean;
}) {
  return (
    <div className="grid grid-cols-[110px_1fr] items-start gap-3 text-[12.5px]">
      <dt className="break-words leading-[1.45] text-ink-faint">{label}</dt>
      <dd
        className={cn(
          "min-w-0 break-words text-ink",
          mono && "font-mono text-[12px]",
        )}
      >
        {children}
      </dd>
    </div>
  );
}

// ---------- Section title ----------

export function SectionTitle({ children }: { children: ReactNode }) {
  return (
    <h3 className="mb-2 mt-5 text-[10px] font-medium uppercase tracking-[0.08em] text-ink-faint first:mt-0">
      {children}
    </h3>
  );
}

// ---------- Meta pills (labels / annotations) ----------

export function MetaPill({ k, v }: { k: string; v: string }) {
  return (
    <div className="inline-flex max-w-full items-center gap-1 rounded-md border border-border bg-surface-2/40 px-2 py-0.5 font-mono text-[11px]">
      <span className="text-ink-muted">{k}</span>
      <span className="text-ink-faint">=</span>
      <span className="truncate text-ink" title={v}>
        {v}
      </span>
    </div>
  );
}

export function MetaPills({ map }: { map?: Record<string, string> }) {
  if (!map || Object.keys(map).length === 0) {
    return <span className="text-[11.5px] text-ink-faint">—</span>;
  }
  return (
    <div className="flex flex-wrap gap-1.5">
      {Object.entries(map).map(([k, v]) => (
        <MetaPill key={k} k={k} v={v} />
      ))}
    </div>
  );
}

// ---------- StatStrip (editorial hero stats) ----------

export type StatTone = "neutral" | "green" | "yellow" | "red" | "muted";
export type StatFamily = "display" | "mono" | "sans";

export interface Stat {
  label: string;
  value: ReactNode;
  tone?: StatTone;
  /** Default: "display" (Instrument Serif). Use "sans" for K8s identifiers
   *  like "ClusterIP", "mono" for technical strings like IPs. */
  family?: StatFamily;
}

const toneClass = (tone?: StatTone) => {
  switch (tone) {
    case "green":
      return "text-green";
    case "yellow":
      return "text-yellow";
    case "red":
      return "text-red";
    case "muted":
      return "text-ink-muted";
    default:
      return "text-ink";
  }
};

const familyClass = (family?: StatFamily) => {
  switch (family) {
    case "mono":
      return "font-mono text-[16px] tracking-tight";
    case "sans":
      return "text-[20px] font-medium tracking-tight";
    case "display":
    default:
      return "font-display text-[30px] tracking-[-0.01em]";
  }
};

export function StatStrip({ stats }: { stats: Stat[] }) {
  return (
    <div
      className="grid gap-x-6 gap-y-4 border-b border-border px-5 py-4"
      style={{ gridTemplateColumns: "repeat(auto-fit, minmax(110px, 1fr))" }}
    >
      {stats.map((stat) => (
        <div key={stat.label} className="min-w-0">
          <div className="mb-1.5 text-[10px] font-medium uppercase tracking-[0.08em] text-ink-faint">
            {stat.label}
          </div>
          <div
            className={cn(
              "tabular leading-none truncate",
              familyClass(stat.family),
              toneClass(stat.tone),
            )}
            title={typeof stat.value === "string" ? stat.value : undefined}
          >
            {stat.value}
          </div>
        </div>
      ))}
    </div>
  );
}

// ---------- ConditionList (replaces overlap-prone KV layout for K8s conditions) ----------

interface ConditionRow {
  type: string;
  status: string; // "True" | "False" | "Unknown"
  reason?: string;
  message?: string;
}

export function ConditionList({ items }: { items: ConditionRow[] }) {
  return (
    <ul className="space-y-1.5">
      {items.map((c, i) => {
        const ok = c.status === "True";
        const tone = ok ? "text-green" : c.status === "False" ? "text-yellow" : "text-ink-muted";
        return (
          <li key={i}>
            <div className="flex items-baseline gap-2 text-[12.5px]">
              <span
                className={cn(
                  "mt-[3px] block size-1.5 shrink-0 self-center rounded-full",
                  ok ? "bg-green" : c.status === "False" ? "bg-yellow" : "bg-ink-faint",
                )}
              />
              <span className="text-ink">{c.type}</span>
              {c.reason && (
                <span className="text-ink-muted">· {c.reason}</span>
              )}
              <span className={cn("ml-auto font-mono text-[12px]", tone)}>
                {c.status}
              </span>
            </div>
            {c.message && (
              <div className="ml-3.5 mt-0.5 break-words text-[11.5px] leading-relaxed text-ink-muted">
                {c.message}
              </div>
            )}
          </li>
        );
      })}
    </ul>
  );
}

// ---------- Tone helpers ----------

export function phaseStatTone(phase: string): StatTone {
  switch (phase) {
    case "Running":
    case "Active":
      return "green";
    case "Pending":
    case "Terminating":
      return "yellow";
    case "Failed":
    case "CrashLoopBackOff":
      return "red";
    default:
      return "muted";
  }
}

export function restartStatTone(n: number): StatTone {
  if (n > 5) return "red";
  if (n > 0) return "yellow";
  return "muted";
}

export function readyStatTone(ready: string): StatTone {
  const [r, t] = ready.split("/").map((n) => parseInt(n, 10));
  if (Number.isNaN(r) || Number.isNaN(t)) return "neutral";
  return r < t ? "yellow" : "neutral";
}

// ---------- PodRow (shared across workload + service describe panes) ----------

function podPhaseTone(phase: string): string {
  switch (phase) {
    case "Running":
      return "text-accent";
    case "Succeeded":
      return "text-green";
    case "Failed":
      return "text-red";
    case "Pending":
      return "text-yellow";
    default:
      return "text-ink-muted";
  }
}

export function PodRow({
  cluster,
  ns,
  pod,
}: {
  cluster: string;
  ns: string;
  pod: JobChildPod;
}) {
  const target =
    `/clusters/${encodeURIComponent(cluster)}/pods` +
    `?sel=${encodeURIComponent(pod.name)}` +
    `&selNs=${encodeURIComponent(ns)}` +
    `&tab=describe`;

  return (
    <li>
      <Link
        to={target}
        className="group flex items-center gap-3 rounded-md border border-border bg-surface-2/40 px-3 py-1.5 transition-colors hover:border-border-strong hover:bg-surface-2"
      >
        <span
          className={cn("block size-1.5 rounded-full", {
            "bg-accent": pod.phase === "Running",
            "bg-green": pod.phase === "Succeeded",
            "bg-red": pod.phase === "Failed",
            "bg-yellow": pod.phase === "Pending",
            "bg-ink-faint":
              pod.phase !== "Running" &&
              pod.phase !== "Succeeded" &&
              pod.phase !== "Failed" &&
              pod.phase !== "Pending",
          })}
        />
        <span className="truncate font-mono text-[12px] text-ink group-hover:text-ink">
          {pod.name}
        </span>
        <span
          className={cn(
            "font-mono text-[11.5px]",
            podPhaseTone(pod.phase),
          )}
        >
          {pod.phase}
        </span>
        <span className="ml-auto flex items-center gap-3 font-mono text-[11px] text-ink-muted tabular">
          <span>{pod.ready}</span>
          <span>↻ {pod.restarts}</span>
          <span>{ageFrom(pod.createdAt)}</span>
        </span>
      </Link>
    </li>
  );
}
