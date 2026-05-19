// ActionBar — sticky strip at the bottom of the YAML editor body.
// Status segments on the left (mode · ops · errors · schema), action
// buttons on the right (cancel · patch · dry-run · diff · apply).
//
// Buttons use a soft-disabled pattern: when their precondition isn't
// met (no edits made, validation errors, etc.) they LOOK disabled but
// still receive clicks — the click surfaces a toast explaining why
// nothing happened. This is more discoverable than HTML `disabled`
// (which silently swallows clicks) and matches what users expect from
// an editor: try the action, see why it can't run, fix and retry.
//
// State machine: the ActionBar is a dumb view over the unified
// ApplyState from useApplyState. The mapping from state to render
// shape lives in actionBarViewModel.ts.

import { showToast } from "../../../lib/toastBus";
import { cn } from "../../../lib/cn";
import type { ApplyState } from "../../../hooks/useApplyState";
import { actionBarViewModel, type StatusChip } from "./actionBarViewModel";

interface ActionBarProps {
  mode: "edit" | "diff";
  opsCount: number;
  errorCount: number;
  dirty: boolean;
  /** Unified state from useApplyLifecycle. */
  applyState: ApplyState;
  schemaLabel?: string;
  schemaState?: "loading" | "loaded" | "missing" | "failed";
  /**
   * Hide the diff toggle button. Form mode passes this because it has
   * no Monaco surface to overlay a diff on — the structured patch view
   * (which IS visible in form mode) carries the same semantic info.
   */
  hideDiff?: boolean;
  /**
   * Hide the "errors N" indicator + the "fix N schema errors first"
   * apply gate. Form mode doesn't surface monaco-style markers; its
   * own validation lives inside the form component and gates the
   * apply button differently.
   */
  hideErrors?: boolean;
  onCancel: () => void;
  onTogglePatch: () => void;
  onDryRun: () => void;
  /**
   * Optional in form mode (no diff button → never invoked). Required
   * in YAML mode.
   */
  onToggleDiff?: () => void;
  onApply: () => void;
  /**
   * Optional in form mode (errors indicator hidden → never invoked).
   * Required in YAML mode.
   */
  onJumpToError?: () => void;
}

export function ActionBar({
  mode,
  opsCount,
  errorCount,
  dirty,
  applyState,
  schemaLabel,
  schemaState,
  hideDiff,
  hideErrors,
  onCancel,
  onTogglePatch,
  onDryRun,
  onToggleDiff,
  onApply,
  onJumpToError,
}: ActionBarProps) {
  const view = actionBarViewModel(applyState);

  // Each button gates on a precondition. When the precondition fails,
  // we surface a toast instead of silently doing nothing. The cancel
  // button is always live — it's the user's escape hatch.
  const guarded =
    (action: () => void, reason: string | null): (() => void) =>
    () => {
      if (reason) {
        showToast(reason, "info");
        return;
      }
      action();
    };

  const noEdits = "make a change first";
  const busyMsg =
    view.statusChip.kind === "in-flight" && view.statusChip.label === "validating…"
      ? "dry-run in progress…"
      : "apply in progress…";

  const patchReason = view.busy ? busyMsg : !dirty ? noEdits : null;
  const dryRunReason = view.busy ? busyMsg : !dirty ? noEdits : null;
  const diffReason = view.busy ? busyMsg : !dirty ? noEdits : null;
  const applyReason = view.busy
    ? busyMsg
    : !dirty
      ? noEdits
      : !hideErrors && errorCount > 0
        ? `fix ${errorCount} schema error${errorCount === 1 ? "" : "s"} first`
        : null;

  return (
    <div className="flex h-9 shrink-0 items-center gap-4 border-t border-border bg-surface px-3 font-mono text-[11px]">
      {/* Status segments */}
      <div className="flex min-w-0 items-center gap-3 text-ink-muted">
        <Segment label="mode" value={mode} />
        <Segment
          label="ops"
          value={String(opsCount)}
          className={dirty ? "text-accent" : "text-ink-faint"}
        />
        {!hideErrors && (
          <SegmentButton
            label="errors"
            value={String(errorCount)}
            className={errorCount > 0 ? "text-red" : "text-green"}
            onClick={errorCount > 0 ? onJumpToError : undefined}
            tabular
          />
        )}
        {schemaLabel && (
          <SchemaPill label={schemaLabel} state={schemaState ?? "loading"} />
        )}
        {!view.bannerOwnsState && <ApplyStatusChip chip={view.statusChip} />}
      </div>

      {/* Buttons */}
      <div className="ml-auto flex items-center gap-1.5">
        <button
          type="button"
          onClick={onCancel}
          disabled={view.busy}
          className="rounded-sm px-2.5 py-1 text-[11px] text-ink-muted transition-colors hover:bg-surface-2 hover:text-ink disabled:opacity-50"
        >
          cancel
        </button>
        <BarButton
          onClick={guarded(onTogglePatch, patchReason)}
          softDisabled={patchReason !== null}
          title={patchReason ?? undefined}
        >
          patch
        </BarButton>
        <BarButton
          onClick={guarded(onDryRun, dryRunReason)}
          softDisabled={dryRunReason !== null}
          title={dryRunReason ?? undefined}
        >
          {view.dryRunLabel}
        </BarButton>
        {!hideDiff && (
          <BarButton
            onClick={guarded(onToggleDiff ?? (() => undefined), diffReason)}
            softDisabled={diffReason !== null}
            active={mode === "diff"}
            title={diffReason ?? undefined}
          >
            diff
          </BarButton>
        )}
        <button
          type="button"
          onClick={guarded(onApply, applyReason)}
          aria-disabled={applyReason !== null}
          title={applyReason ?? undefined}
          className={cn(
            "rounded-sm px-3 py-1 font-medium transition-colors",
            applyReason === null
              ? "border border-accent bg-accent text-bg hover:brightness-110"
              : "cursor-not-allowed border border-border-strong text-ink-faint opacity-60",
          )}
        >
          {view.applyLabel}
        </button>
      </div>
    </div>
  );
}

function Segment({
  label,
  value,
  className,
  tabular,
}: {
  label: string;
  value: string;
  className?: string;
  tabular?: boolean;
}) {
  return (
    <span className="inline-flex items-center gap-1">
      <span className="text-ink-faint">{label}</span>
      <span className={cn(tabular && "tabular", className ?? "text-ink")}>{value}</span>
    </span>
  );
}

function SegmentButton({
  label,
  value,
  className,
  onClick,
  tabular,
}: {
  label: string;
  value: string;
  className?: string;
  onClick?: () => void;
  tabular?: boolean;
}) {
  if (!onClick) {
    return <Segment label={label} value={value} className={className} tabular={tabular} />;
  }
  return (
    <button
      type="button"
      onClick={onClick}
      className="inline-flex items-center gap-1 transition-colors hover:underline"
    >
      <span className="text-ink-faint">{label}</span>
      <span className={cn(tabular && "tabular", className ?? "text-ink")}>{value}</span>
    </button>
  );
}

function BarButton({
  onClick,
  softDisabled,
  active,
  title,
  children,
}: {
  onClick: () => void;
  softDisabled?: boolean;
  active?: boolean;
  title?: string;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-disabled={softDisabled}
      title={title}
      className={cn(
        "rounded-sm border px-2.5 py-1 transition-colors",
        softDisabled
          ? "cursor-not-allowed border-border-strong text-ink-faint opacity-60"
          : active
            ? "border-accent text-accent"
            : "border-border-strong text-ink-muted hover:border-ink-muted hover:text-ink",
      )}
    >
      {children}
    </button>
  );
}

function ApplyStatusChip({ chip }: { chip: StatusChip }) {
  if (chip.kind === "none") return null;
  if (chip.kind === "applied") {
    return (
      <span className="inline-flex items-center gap-1 text-green">
        <span className="size-1.5 rounded-full bg-green" /> applied
      </span>
    );
  }
  if (chip.kind === "error") {
    return (
      <span
        className="inline-flex max-w-[24ch] items-center gap-1 truncate text-red"
        title={chip.message}
      >
        <span className="size-1.5 rounded-full bg-red" /> {chip.message}
      </span>
    );
  }
  return (
    <span className="inline-flex items-center gap-1 text-ink-muted">
      <span className="size-1.5 animate-pulse rounded-full bg-accent" />
      {chip.label}
    </span>
  );
}

function SchemaPill({
  label,
  state,
}: {
  label: string;
  state: "loading" | "loaded" | "missing" | "failed";
}) {
  const dotColor =
    state === "loaded" ? "bg-green" :
    state === "loading" ? "bg-ink-faint animate-pulse" :
    state === "missing" ? "bg-yellow" :
    "bg-red";
  const title =
    state === "loaded" ? "Schema validation active" :
    state === "loading" ? "Loading OpenAPI schema from cluster…" :
    state === "missing" ? "No bundled schema for this resource (likely a CRD). Editor still works; validation/autocomplete unavailable." :
    "Schema unavailable. Editor works; validation/autocomplete disabled.";
  return (
    <span
      className={cn(
        "hidden items-center gap-1.5 md:inline-flex",
        state === "loaded" ? "text-ink" :
        state === "missing" ? "text-yellow" :
        state === "failed" ? "text-red" : "text-ink-faint",
      )}
      title={title}
    >
      <span className="text-ink-faint">schema</span>
      <span className={cn("size-1.5 rounded-full", dotColor)} />
      <span className="tabular">{state === "loaded" ? label : state}</span>
    </span>
  );
}
