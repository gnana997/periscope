import { useState, type ReactNode } from "react";
import { Link } from "react-router-dom";
import { cn } from "../../lib/cn";
import type { LogStreamStatus } from "../../hooks/useLogStream";

export interface LogToolbarProps {
  containers: string[];
  initContainers: string[];
  container: string;
  onContainerChange: (value: string) => void;

  tailLines: number;
  onTailLinesChange: (value: number) => void;

  sinceSeconds: number | null;
  onSinceSecondsChange: (value: number | null) => void;

  previous: boolean;
  onPreviousChange: (value: boolean) => void;

  follow: boolean;
  onFollowChange: (value: boolean) => void;

  timestamps: boolean;
  onTimestampsChange: (value: boolean) => void;

  wrap: boolean;
  onWrapChange: (value: boolean) => void;

  search: string;
  onSearchChange: (value: string) => void;

  status: LogStreamStatus;
  totalReceived: number;
  overflowed: boolean;
  onReload: () => void;

  // Always-present share button; copies a URL to the equivalent full-page view.
  shareUrl: string;
  // When provided, render an "↗ expand" Link to the full page (in-tab variant).
  expandTo?: string;
}

const TAIL_OPTIONS = [
  { value: 100, label: "100" },
  { value: 500, label: "500" },
  { value: 1000, label: "1k" },
  { value: 5000, label: "5k" },
  { value: 10000, label: "10k" },
];

const SINCE_OPTIONS: Array<{ value: number | null; label: string }> = [
  { value: null, label: "all" },
  { value: 15 * 60, label: "15m" },
  { value: 60 * 60, label: "1h" },
  { value: 6 * 60 * 60, label: "6h" },
  { value: 24 * 60 * 60, label: "24h" },
];

export function LogToolbar(props: LogToolbarProps) {
  const containerOpts = [
    ...props.containers.map((c) => ({ value: c, label: c, group: "containers" })),
    ...props.initContainers.map((c) => ({
      value: c,
      label: `init: ${c}`,
      group: "init",
    })),
  ];

  return (
    <div className="flex flex-col gap-2 border-b border-border bg-bg/95 px-5 py-3 backdrop-blur-sm">
      {/* Top row: source controls */}
      <div className="flex flex-wrap items-center gap-2">
        <Field label="container">
          <Select
            value={props.container}
            onChange={props.onContainerChange}
            options={containerOpts}
            disabled={containerOpts.length === 0}
          />
        </Field>

        <Field label="tail">
          <Select
            value={String(props.tailLines)}
            onChange={(v) => props.onTailLinesChange(parseInt(v, 10))}
            options={TAIL_OPTIONS.map((o) => ({
              value: String(o.value),
              label: o.label,
            }))}
          />
        </Field>

        <Field label="since">
          <Select
            value={props.sinceSeconds === null ? "" : String(props.sinceSeconds)}
            onChange={(v) =>
              props.onSinceSecondsChange(v === "" ? null : parseInt(v, 10))
            }
            options={SINCE_OPTIONS.map((o) => ({
              value: o.value === null ? "" : String(o.value),
              label: o.label,
            }))}
          />
        </Field>

        <ToggleChip
          label="previous"
          tooltip="Show logs from the previous container instance (e.g. before crash)"
          active={props.previous}
          onChange={props.onPreviousChange}
        />
        <ToggleChip
          label="follow"
          tooltip="Stream new lines as they arrive"
          active={props.follow}
          onChange={props.onFollowChange}
        />
        <ToggleChip
          label="timestamps"
          tooltip="Show timestamp gutter"
          active={props.timestamps}
          onChange={props.onTimestampsChange}
        />
        <ToggleChip
          label="wrap"
          tooltip="Wrap long lines"
          active={props.wrap}
          onChange={props.onWrapChange}
        />

        <div className="ml-auto flex items-center gap-2">
          <StatusPill
            status={props.status}
            totalReceived={props.totalReceived}
            overflowed={props.overflowed}
          />
          <ShareButton url={props.shareUrl} />
          {props.expandTo && (
            <Link
              to={props.expandTo}
              title="Open this view full-screen (preserves state)"
              className="rounded-md border border-border bg-surface-2/40 px-2 py-1 font-mono text-[11px] text-ink-muted transition-colors hover:border-border-strong hover:bg-surface-2 hover:text-ink"
            >
              ↗ expand
            </Link>
          )}
          <button
            type="button"
            onClick={props.onReload}
            className="rounded-md border border-border bg-surface-2/40 px-2 py-1 font-mono text-[11px] text-ink-muted transition-colors hover:border-border-strong hover:bg-surface-2 hover:text-ink"
          >
            reload
          </button>
        </div>
      </div>

      {/* Bottom row: search */}
      <div className="flex items-center gap-2">
        <input
          type="text"
          placeholder="search   ·   matches show ±5 lines of context"
          value={props.search}
          onChange={(e) => props.onSearchChange(e.target.value)}
          className="min-w-0 flex-1 rounded-md border border-border bg-surface-2/30 px-3 py-1.5 font-mono text-[12px] text-ink placeholder:text-ink-faint focus:border-accent focus:bg-bg focus:outline-none"
        />
        {props.search && (
          <button
            type="button"
            onClick={() => props.onSearchChange("")}
            className="rounded-md border border-border bg-surface-2/40 px-2 py-1.5 font-mono text-[11px] text-ink-muted hover:border-border-strong hover:text-ink"
          >
            clear
          </button>
        )}
      </div>
    </div>
  );
}

function ShareButton({ url }: { url: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <button
      type="button"
      title="Copy a shareable URL to this view"
      onClick={async () => {
        try {
          await navigator.clipboard.writeText(url);
          setCopied(true);
          window.setTimeout(() => setCopied(false), 1500);
        } catch {
          // Clipboard API can fail (insecure context, blocked perms);
          // fail silently rather than disrupt the user.
        }
      }}
      className={cn(
        "rounded-md border px-2 py-1 font-mono text-[11px] transition-colors",
        copied
          ? "border-green text-green"
          : "border-border bg-surface-2/40 text-ink-muted hover:border-border-strong hover:bg-surface-2 hover:text-ink",
      )}
    >
      {copied ? "✓ copied" : "share"}
    </button>
  );
}

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <label className="flex items-center gap-1.5">
      <span className="font-mono text-[10px] uppercase tracking-[0.08em] text-ink-faint">
        {label}
      </span>
      {children}
    </label>
  );
}

function Select({
  value,
  onChange,
  options,
  disabled,
}: {
  value: string;
  onChange: (value: string) => void;
  options: Array<{ value: string; label: string; group?: string }>;
  disabled?: boolean;
}) {
  return (
    <select
      value={value}
      disabled={disabled}
      onChange={(e) => onChange(e.target.value)}
      className="rounded-md border border-border bg-surface-2/40 px-2 py-1 font-mono text-[11.5px] text-ink hover:border-border-strong focus:border-accent focus:outline-none disabled:opacity-50"
    >
      {options.map((o) => (
        <option key={o.value} value={o.value}>
          {o.label}
        </option>
      ))}
    </select>
  );
}

function ToggleChip({
  label,
  tooltip,
  active,
  onChange,
}: {
  label: string;
  tooltip?: string;
  active: boolean;
  onChange: (value: boolean) => void;
}) {
  return (
    <button
      type="button"
      title={tooltip}
      onClick={() => onChange(!active)}
      className={cn(
        "rounded-md border px-2 py-1 font-mono text-[11px] transition-colors",
        active
          ? "border-accent bg-accent-soft text-accent"
          : "border-border bg-surface-2/40 text-ink-muted hover:border-border-strong hover:text-ink",
      )}
    >
      {label}
    </button>
  );
}

function StatusPill({
  status,
  totalReceived,
  overflowed,
}: {
  status: LogStreamStatus;
  totalReceived: number;
  overflowed: boolean;
}) {
  const tone =
    status === "streaming"
      ? "text-accent"
      : status === "connecting"
        ? "text-yellow"
        : status === "error"
          ? "text-red"
          : "text-ink-faint";
  const dotClass =
    status === "streaming"
      ? "bg-accent"
      : status === "connecting"
        ? "bg-yellow"
        : status === "error"
          ? "bg-red"
          : "bg-ink-faint/50";
  return (
    <div className="flex items-center gap-2 font-mono text-[10.5px]">
      <span className="relative flex size-2">
        {status === "streaming" && (
          <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-accent opacity-60" />
        )}
        <span className={cn("relative inline-flex size-2 rounded-full", dotClass)} />
      </span>
      <span className={cn("uppercase tracking-[0.06em]", tone)}>{status}</span>
      <span className="text-ink-faint">
        {totalReceived.toLocaleString()} lines
        {overflowed ? " (older dropped)" : ""}
      </span>
    </div>
  );
}
