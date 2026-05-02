// DriftBanner — surfaces a "the cluster changed under you" warning
// while the user is mid-edit. Shown only when the buffer is dirty;
// for clean buffers the editor silently swaps to the new server YAML
// (PR9). Sits above ApplyErrorBanner in the YamlEditor banner stack.
//
// Color comes from the manager category:
//   - HUMAN  → red (race with another operator; you might overwrite)
//   - GITOPS → yellow (Flux/ArgoCD; will revert your write on next reconcile)
//   - CONTROLLER / HELM → yellow (in-cluster controller; will reset)
//   - UNKNOWN → muted yellow
//
// Three actions: [show diff] (PR11 wires the overlay; PR10 stub),
// [reload] (confirm-if-dirty, discard edits, fetch fresh server YAML
// as new pristine), [×] dismiss (suppress until the next NEW
// resourceVersion brings new drift).

import { useMemo } from "react";
import { cn } from "../../../lib/cn";
import type { DriftInfo } from "../../../lib/drift";
import { managerColorClass } from "../../../lib/managers";

interface DriftBannerProps {
  drift: DriftInfo;
  /** Disables actions during in-flight apply/reload operations. */
  busy: boolean;
  onShowDiff: () => void;
  onReload: () => void;
  onDismiss: () => void;
  /** True when [show diff] is wired to a real overlay (PR11). */
  showDiffEnabled: boolean;
}

export function DriftBanner({
  drift,
  busy,
  onShowDiff,
  onReload,
  onDismiss,
  showDiffEnabled,
}: DriftBannerProps) {
  const palette = managerColorClass(drift.category);
  const isUrgent = drift.category === "HUMAN";

  const ago = useMemo(() => formatAgo(drift.at), [drift.at]);
  const previewPaths = drift.paths.slice(0, 2);
  const hiddenCount = Math.max(0, drift.paths.length - previewPaths.length);

  return (
    <div
      className={cn(
        "flex shrink-0 items-start gap-3 border-y px-4 py-2.5",
        isUrgent ? "border-red/50 bg-red-soft" : "border-yellow/50 bg-yellow/10",
      )}
      role="status"
    >
      {/* Manager-category glyph — same colour family as the editor gutter */}
      <span aria-hidden className={cn("mt-1 size-2 shrink-0 rounded-sm", palette.glyph)} />

      <div className="min-w-0 flex-1">
        <div
          className={cn(
            "font-mono text-[11px] font-medium",
            isUrgent ? "text-red" : "text-yellow-700 dark:text-yellow-300",
          )}
        >
          {isUrgent ? "cluster modified by another operator" : "cluster modified by a controller"}
        </div>
        <div className="mt-0.5 font-mono text-[11.5px] text-ink">
          <span className="font-medium">{drift.manager}</span>
          <span className="text-ink-muted"> · {drift.category.toLowerCase()}</span>
          <span className="text-ink-muted">
            {" · "}
            modified {drift.paths.length} field{drift.paths.length === 1 ? "" : "s"}
            {ago && <> · {ago}</>}
          </span>
        </div>
        {previewPaths.length > 0 && (
          <div className="mt-1 truncate font-mono text-[11px] text-ink-muted">
            {previewPaths.join("  ·  ")}
            {hiddenCount > 0 && (
              <span className="text-ink-faint"> · +{hiddenCount} more</span>
            )}
          </div>
        )}
      </div>

      <div className="flex shrink-0 items-center gap-1.5">
        <button
          type="button"
          onClick={onShowDiff}
          disabled={busy || !showDiffEnabled}
          title={
            showDiffEnabled
              ? "Open inline diff of pristine vs current cluster state"
              : "Diff overlay ships in PR11"
          }
          className="rounded-sm border border-border-strong px-2.5 py-1 font-mono text-[11px] text-ink-muted transition-colors hover:border-ink-muted hover:text-ink disabled:cursor-not-allowed disabled:opacity-50"
        >
          show diff
        </button>
        <button
          type="button"
          onClick={onReload}
          disabled={busy}
          className={cn(
            "rounded-sm border px-2.5 py-1 font-mono text-[11px] font-medium transition-colors disabled:cursor-not-allowed disabled:opacity-50",
            isUrgent
              ? "border-red bg-red text-bg hover:brightness-110"
              : "border-yellow-700 bg-yellow text-bg hover:brightness-110",
          )}
          title="Discard your edits and load the latest cluster state"
        >
          reload
        </button>
        <button
          type="button"
          onClick={onDismiss}
          disabled={busy}
          className="flex size-6 items-center justify-center rounded-md text-ink-muted hover:bg-surface-2 hover:text-ink disabled:cursor-not-allowed disabled:opacity-50"
          aria-label="Dismiss drift warning"
          title="Dismiss until the next change"
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
  );
}

/**
 * formatAgo turns an ISO timestamp into "Ns ago" / "Nm ago" / "Nh ago".
 * Re-renders on every meta poll (15s), so a banner that's been open
 * for a while will progress from "12s ago" → "27s ago" → "1m ago".
 */
function formatAgo(iso: string): string {
  const t = Date.parse(iso);
  if (!Number.isFinite(t)) return "";
  const sec = Math.max(0, Math.floor((Date.now() - t) / 1000));
  if (sec < 60) return `${sec}s ago`;
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min}m ago`;
  const hr = Math.floor(min / 60);
  return `${hr}h ago`;
}
