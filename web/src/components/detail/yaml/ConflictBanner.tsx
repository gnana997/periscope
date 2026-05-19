// ConflictBanner — the single-banner conflict-resolution UI for SSA
// apply attempts. Replaces the v1.1.x multi-button per-field
// disposition flow (ConflictResolutionView + TakeoverDialog), which
// modelled a per-field choice the apiserver can't actually honor.
//
// The component is dumb: it consumes a BannerView (built from the
// apply state machine via `bannerViewModel`) and renders it. All
// logic — variant selection, manager formatting, button states —
// lives in conflictBannerViewModel.ts so it can be tested without
// a DOM.

import { cn } from "../../../lib/cn";
import {
  type BannerAction,
  type BannerView,
} from "./conflictBannerViewModel";

interface ConflictBannerProps {
  view: BannerView;
  onForce: () => void;
  onCancel: () => void;
  onRetry: () => void;
  onDismiss: () => void;
}

export function ConflictBanner({
  view,
  onForce,
  onCancel,
  onRetry,
  onDismiss,
}: ConflictBannerProps) {
  if (!view.visible) return null;

  const handlers: Record<BannerAction, () => void> = {
    force: onForce,
    cancel: onCancel,
    retry: onRetry,
    dismiss: onDismiss,
  };
  const onPrimary = handlers[view.primary.action];
  const onSecondary = handlers[view.secondary.action];

  const tone = view.variant === "error" ? "danger" : "warning";

  return (
    <div
      className={cn(
        "flex items-start gap-3 border-t px-3 py-2 font-mono text-[11px]",
        tone === "warning"
          ? "border-yellow/30 bg-yellow/5 text-ink"
          : "border-red/30 bg-red/5 text-ink",
      )}
      role="alert"
    >
      <span
        className={cn(
          "mt-0.5 size-1.5 shrink-0 rounded-full",
          tone === "warning" ? "bg-yellow" : "bg-red",
        )}
      />
      <div className="flex min-w-0 flex-1 flex-col gap-0.5">
        {view.variant === "error" ? (
          <ErrorBody status={view.error?.status} message={view.error?.message} />
        ) : (
          <ConflictBody
            forcing={view.variant === "forcing"}
            managers={view.managers ?? []}
            fields={view.fields ?? []}
          />
        )}
      </div>
      <div className="ml-2 flex shrink-0 items-center gap-1.5">
        <button
          type="button"
          onClick={onSecondary}
          disabled={view.secondary.disabled}
          className="rounded-sm px-2.5 py-1 text-ink-muted transition-colors hover:bg-surface-2 hover:text-ink disabled:opacity-50"
        >
          {view.secondary.label}
        </button>
        <button
          type="button"
          onClick={onPrimary}
          disabled={view.primary.disabled}
          className={cn(
            "rounded-sm border px-3 py-1 font-medium transition-colors",
            view.primary.disabled
              ? "cursor-not-allowed border-border-strong text-ink-faint opacity-60"
              : tone === "warning"
                ? "border-yellow bg-yellow/20 text-yellow hover:bg-yellow/30"
                : "border-red bg-red/20 text-red hover:bg-red/30",
          )}
        >
          {view.primary.loading ? `${view.primary.label}…` : view.primary.label}
        </button>
      </div>
    </div>
  );
}

function ConflictBody({
  forcing,
  managers,
  fields,
}: {
  forcing: boolean;
  managers: string[];
  fields: string[];
}) {
  return (
    <>
      <span>
        {forcing
          ? "Forcing apply — overriding field ownership."
          : managers.length === 1
            ? `Field ownership conflict with ${managers[0]}.`
            : `Field ownership conflict with ${managers.length} managers.`}
      </span>
      {managers.length > 1 && (
        <span className="text-ink-muted">{managers.join(", ")}</span>
      )}
      {fields.length > 0 && (
        <span className="text-ink-muted">
          fields: <span className="text-ink">{fields.join(", ")}</span>
        </span>
      )}
      {!forcing && (
        <span className="text-ink-faint">
          Force apply will overwrite the conflicting fields. The current
          owner may re-claim on its next reconcile.
        </span>
      )}
    </>
  );
}

function ErrorBody({
  status,
  message,
}: {
  status?: number;
  message?: string;
}) {
  return (
    <>
      <span>
        Apply failed{status && status > 0 ? ` (${status})` : ""}.
      </span>
      {message && <span className="text-ink-muted">{message}</span>}
    </>
  );
}
