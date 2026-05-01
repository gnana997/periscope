import { useState } from "react";
import type { Pod } from "../../lib/types";
import { ageFrom } from "../../lib/format";
import { phaseTone, StatusDot } from "../table/StatusDot";
import { cn } from "../../lib/cn";

const TABS = [
  { id: "describe", label: "describe", ready: true },
  { id: "yaml", label: "yaml", ready: false },
  { id: "logs", label: "logs", ready: false },
  { id: "exec", label: "exec", ready: false },
  { id: "events", label: "events", ready: false },
] as const;

type TabId = (typeof TABS)[number]["id"];

export function DetailPane({
  pod,
  onClose,
}: {
  pod: Pod;
  onClose: () => void;
}) {
  const [tab, setTab] = useState<TabId>("describe");

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex h-9 shrink-0 items-center gap-1 border-b border-border bg-surface px-3">
        {TABS.map((t) => (
          <button
            key={t.id}
            type="button"
            onClick={() => t.ready && setTab(t.id)}
            disabled={!t.ready}
            className={cn(
              "relative inline-flex items-center gap-1.5 px-2 py-1 text-[12px] transition-colors",
              !t.ready && "cursor-not-allowed text-ink-faint",
              t.ready && tab === t.id && "text-ink",
              t.ready && tab !== t.id && "text-ink-muted hover:text-ink",
            )}
          >
            {t.label}
            {!t.ready && (
              <span className="rounded-sm border border-border px-1 py-px text-[8.5px] uppercase tracking-[0.06em]">
                Soon
              </span>
            )}
            {t.ready && tab === t.id && (
              <span className="absolute inset-x-2 -bottom-px h-px bg-accent" />
            )}
          </button>
        ))}
        <div className="ml-auto">
          <button
            type="button"
            onClick={onClose}
            className="flex size-7 items-center justify-center rounded-md text-ink-muted transition-colors hover:bg-surface-2 hover:text-ink"
            aria-label="Close detail"
          >
            <svg width="11" height="11" viewBox="0 0 11 11" aria-hidden>
              <path
                d="M2 2l7 7M9 2l-7 7"
                stroke="currentColor"
                strokeWidth="1.4"
                strokeLinecap="round"
              />
            </svg>
          </button>
        </div>
      </div>
      <div className="flex-1 overflow-auto">
        {tab === "describe" && <DescribeTab pod={pod} />}
      </div>
    </div>
  );
}

function DescribeTab({ pod }: { pod: Pod }) {
  const tone = phaseTone(pod.phase);
  const phaseColorCls =
    tone === "green"
      ? "text-green"
      : tone === "yellow"
        ? "text-yellow"
        : tone === "red"
          ? "text-red"
          : "text-ink-muted";

  return (
    <div className="px-5 py-4">
      <div className="mb-4 flex items-baseline gap-3">
        <h2
          className="font-mono text-[15px] font-medium text-ink"
          title={pod.name}
        >
          {pod.name}
        </h2>
        <span className="text-[11px] text-ink-faint">·</span>
        <span className="font-mono text-[12px] text-ink-muted">
          {pod.namespace}
        </span>
      </div>

      <dl className="space-y-2">
        <Row label="Phase">
          <span className={cn("inline-flex items-center gap-1.5", phaseColorCls)}>
            <StatusDot tone={tone} />
            {pod.phase}
          </span>
        </Row>
        <Row label="Ready">
          <span className="font-mono">{pod.ready}</span>
        </Row>
        <Row label="Restarts">
          <span
            className={cn(
              "font-mono",
              pod.restarts > 5
                ? "text-red"
                : pod.restarts > 0
                  ? "text-yellow"
                  : "text-ink-muted",
            )}
          >
            {pod.restarts}
          </span>
        </Row>
        {pod.nodeName && (
          <Row label="Node">
            <span className="font-mono text-ink-muted">{pod.nodeName}</span>
          </Row>
        )}
        {pod.podIP && (
          <Row label="Pod IP">
            <span className="font-mono text-ink-muted">{pod.podIP}</span>
          </Row>
        )}
        <Row label="Age">
          <span className="font-mono text-ink-muted">
            {ageFrom(pod.createdAt)}
          </span>
        </Row>
        <Row label="Created">
          <span className="font-mono text-ink-faint">{pod.createdAt}</span>
        </Row>
      </dl>

      <div className="mt-6 rounded-md border border-border bg-surface-2/40 px-3 py-2.5 text-[11.5px] leading-relaxed text-ink-muted">
        <span className="font-mono text-ink-faint">soon: </span>
        full <span className="font-mono">describe</span>, live{" "}
        <span className="font-mono">logs</span>, in-browser{" "}
        <span className="font-mono">exec</span>, scoped{" "}
        <span className="font-mono">events</span>.
      </div>
    </div>
  );
}

function Row({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <div className="grid grid-cols-[100px_1fr] items-baseline gap-3 text-[12.5px]">
      <dt className="text-ink-faint">{label}</dt>
      <dd className="min-w-0 truncate text-ink">{children}</dd>
    </div>
  );
}
