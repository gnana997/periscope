// CoManagementBanner — sticky note shown at editor open when the
// resource has managedFields entries from controllers other than
// periscope-spa. Operators see who else writes this resource so
// they aren't surprised when their edits are reconciled away later.
//
// Sits between DriftBanner and SchemaMissingBanner in the YamlEditor
// banner stack. Dismissable per-session per-resource via useDismissed
// — quiet ack, no re-prompt until a fresh tab.
//
// Density / palette mirrors SchemaMissingBanner (yellow strip with
// a left glyph). Distinct from DriftBanner (which fires mid-edit
// when the cluster mutates under the user) and ApplyErrorBanner
// (red, post-apply failure). Read-only — no actions beyond dismiss.

import { cn } from "../../../lib/cn";

export interface OtherOwnerSummary {
  manager: string;
  /** Number of paths this manager owns. Used to rank for display. */
  pathCount: number;
}

interface CoManagementBannerProps {
  /** Paths the body will re-assert this session (count only). */
  selfOwnedCount: number;
  /** Other managers grouped by name. Caller pre-sorts by salience. */
  otherOwners: OtherOwnerSummary[];
  onDismiss: () => void;
}

const TOP_N = 3;

export function CoManagementBanner({
  selfOwnedCount,
  otherOwners,
  onDismiss,
}: CoManagementBannerProps) {
  if (otherOwners.length === 0) return null;

  const top = otherOwners.slice(0, TOP_N);
  const hidden = Math.max(0, otherOwners.length - top.length);
  const managerList = top.map((o) => o.manager).join(", ");

  return (
    <div
      className={cn(
        "flex shrink-0 items-start gap-3 border-y px-4 py-2",
        "border-yellow/40 bg-yellow/5",
      )}
      role="status"
    >
      <span aria-hidden className="mt-1 size-2 shrink-0 rounded-sm bg-yellow/70" />
      <div className="min-w-0 flex-1">
        <div className="font-mono text-[11px] font-medium text-yellow">
          co-managed resource
        </div>
        <div className="mt-0.5 font-mono text-[11.5px] text-ink-muted">
          <span className="text-ink">{managerList}</span>
          {hidden > 0 && (
            <span> +{hidden} more</span>
          )}
          {" — "}
          edits will be co-owned; these managers may overwrite them on reconcile.
          {selfOwnedCount > 0 && (
            <>
              {" "}
              <span className="text-ink">
                {selfOwnedCount} field{selfOwnedCount === 1 ? "" : "s"} you already
                own will be retained.
              </span>
            </>
          )}
        </div>
      </div>
      <button
        type="button"
        onClick={onDismiss}
        aria-label="Dismiss co-management notice"
        className="ml-2 shrink-0 rounded-sm px-2 py-0.5 font-mono text-[10.5px] text-ink-faint transition-colors hover:bg-surface-2 hover:text-ink"
      >
        ×
      </button>
    </div>
  );
}
