import { cn } from "../../lib/cn";
import { tlsExpiryState } from "../../lib/tlsExpiry";

/**
 * Single-row chip rendering for a TLS cert expiry. Calm grey when
 * comfortable (>30d), yellow at ≤30d, red at ≤7d, red with
 * "expired" label after the boundary. nil/undefined → em-dash.
 *
 * Used on the SecretsPage list (per-Secret expiry) and the
 * IngressesPage list (soonest expiry across spec.tls[]).
 */
export function TLSExpiryChip({ expiresAt }: { expiresAt?: string }) {
  const state = tlsExpiryState(expiresAt);
  if (!state) {
    return <span className="text-ink-faint">—</span>;
  }
  const tone =
    state.tone === "comfortable"
      ? "text-ink-muted"
      : state.tone === "warning"
        ? "text-yellow"
        : "text-red";
  const label =
    state.tone === "expired"
      ? `expired ${Math.abs(state.daysRemaining)}d ago`
      : `${state.daysRemaining}d`;
  return (
    <span
      className={cn("font-mono text-[11.5px]", tone)}
      title={`Expires ${state.isoDate}`}
    >
      {label}
    </span>
  );
}
