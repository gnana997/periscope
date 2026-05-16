// LockedFeaturePane is the paywall surface shared by every AWS
// Access entry point: the per-workload tab body and the reverse-
// lookup page header. The tab is always present in the IA so the
// feature is discoverable — when capabilities reports it
// unavailable, this pane explains exactly why with the exact
// permissions / RBAC verbs the operator needs to add.
//
// Per backend-as-source-of-truth: the message / missing perms /
// docs link all come from the server's FeatureCapability shape.
// Reason→fallback copy is a defensive default in case the server
// omits Message; an MCP tool reading the same FeatureCapability
// uses the structured Reason + Missing fields directly.

import type { CapabilityReason, FeatureCapability } from "../../lib/identity";

export interface LockedFeaturePaneProps {
  feature: FeatureCapability;
  title: string;
  probedAt?: string;
  onRecheck?: () => void;
  recheckPending?: boolean;
}

export function LockedFeaturePane({
  feature,
  title,
  probedAt,
  onRecheck,
  recheckPending,
}: LockedFeaturePaneProps) {
  const reason = feature.reason ?? "RBAC_DENIED";
  const message =
    feature.message ?? reasonFallbackMessage(reason as CapabilityReason);
  return (
    <section className="mx-auto my-8 max-w-2xl rounded-md border border-border bg-surface px-6 py-6">
      <header className="mb-4 flex items-start gap-3">
        <LockGlyph />
        <div className="flex-1">
          <h3 className="text-[15px] font-medium text-ink">{title}</h3>
          <p className="mt-1 text-[12.5px] text-ink-faint">
            <span className="font-mono uppercase tracking-wide">{reason}</span>
          </p>
        </div>
      </header>

      <p className="mb-4 text-[13.5px] leading-relaxed text-ink">{message}</p>

      {feature.note ? (
        <p className="mb-4 rounded-sm border border-border bg-surface-2 px-3 py-2 text-[12.5px] text-ink-muted">
          {feature.note}
        </p>
      ) : null}

      {feature.missing && feature.missing.length > 0 ? (
        <div className="mb-4">
          <p className="mb-1 text-[12px] font-medium uppercase tracking-wide text-ink-faint">
            Missing permissions
          </p>
          <ul className="divide-y divide-border rounded-md border border-border bg-surface-2">
            {feature.missing.map((m) => (
              <li
                key={m}
                className="px-3 py-2 font-mono text-[12.5px] text-ink"
              >
                {m}
              </li>
            ))}
          </ul>
        </div>
      ) : null}

      <div className="flex flex-wrap items-center gap-3">
        {feature.docsUrl ? (
          <a
            href={feature.docsUrl}
            className="text-[13px] text-accent hover:underline"
            target="_blank"
            rel="noreferrer"
          >
            View setup documentation →
          </a>
        ) : null}
        {feature.consoleUrl ? (
          <a
            href={feature.consoleUrl}
            className="text-[13px] text-accent hover:underline"
            target="_blank"
            rel="noreferrer"
          >
            Open in AWS Console →
          </a>
        ) : null}
        {onRecheck ? (
          <button
            type="button"
            disabled={recheckPending}
            onClick={onRecheck}
            className="ml-auto rounded-sm border border-border bg-surface-2 px-3 py-1 text-[12.5px] text-ink hover:bg-surface disabled:cursor-not-allowed disabled:opacity-50"
          >
            {recheckPending ? "Re-checking…" : "Re-check"}
          </button>
        ) : null}
      </div>

      {probedAt ? (
        <p className="mt-3 text-[11.5px] text-ink-faint">
          Probed {humanizeTimestamp(probedAt)}.
        </p>
      ) : null}
    </section>
  );
}

function LockGlyph() {
  // Inline svg to avoid pulling another icon dep — matches the
  // existing pattern in TLSExpiryChip / SeverityChip.
  return (
    <svg
      width="20"
      height="20"
      viewBox="0 0 20 20"
      aria-hidden="true"
      className="mt-0.5 text-ink-muted"
    >
      <path
        d="M5 9V6a5 5 0 0 1 10 0v3h1a1 1 0 0 1 1 1v7a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1v-7a1 1 0 0 1 1-1h1zm2 0h6V6a3 3 0 1 0-6 0v3z"
        fill="currentColor"
      />
    </svg>
  );
}

function reasonFallbackMessage(reason: CapabilityReason): string {
  switch (reason) {
    case "NOT_EKS":
      return "This cluster is not backed by EKS; AWS Access features are EKS-only.";
    case "RBAC_DENIED":
      return "Your Kubernetes role lacks the reads required to power this view.";
    case "MISSING_IAM_PERMS":
      return "Periscope's AWS role lacks one or more IAM permissions needed to compute this view.";
    case "NO_IDENTITY_CONFIGURED":
      return "No ServiceAccount → IAM role bindings (IRSA or Pod Identity) exist in this cluster yet.";
    case "INFORMER_WARMING":
      return "Periscope's ServiceAccount informer is still syncing; try again in a few seconds.";
    case "IAM_PROBE_DISABLED":
      return "IAM permission probe is disabled by configuration. First call will surface any missing perms.";
    default:
      return "This feature is not available in the current cluster context.";
  }
}

function humanizeTimestamp(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  const diff = Math.max(0, Date.now() - d.getTime());
  const mins = Math.floor(diff / 60000);
  if (mins < 1) return "just now";
  if (mins === 1) return "1 minute ago";
  if (mins < 60) return `${mins} minutes ago`;
  const hours = Math.floor(mins / 60);
  return hours === 1 ? "1 hour ago" : `${hours} hours ago`;
}
