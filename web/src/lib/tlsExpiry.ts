/**
 * TLS cert expiry chip math, shared between SecretsPage and
 * IngressesPage. Mirrors the supportWindowState shape from eosChip.ts
 * but with TLS-cert-relevant thresholds: red at ≤7d (rotate now),
 * yellow at ≤30d (rotate soon), muted at > 30d (calm), red+expired
 * label when past.
 */

export type TLSExpiryTone = "comfortable" | "warning" | "critical" | "expired";

export interface TLSExpiryState {
  tone: TLSExpiryTone;
  daysRemaining: number;
  isoDate: string;
}

export function tlsExpiryState(expiresAt?: string): TLSExpiryState | null {
  if (!expiresAt) return null;
  const d = new Date(expiresAt);
  if (Number.isNaN(d.getTime())) return null;

  const now = Date.now();
  const ms = d.getTime();
  const dayMs = 24 * 60 * 60 * 1000;
  const days = Math.ceil((ms - now) / dayMs);
  const iso = expiresAt.slice(0, 10);

  if (now >= ms) {
    return { tone: "expired", daysRemaining: days, isoDate: iso };
  }
  if (days <= 7) {
    return { tone: "critical", daysRemaining: days, isoDate: iso };
  }
  if (days <= 30) {
    return { tone: "warning", daysRemaining: days, isoDate: iso };
  }
  return { tone: "comfortable", daysRemaining: days, isoDate: iso };
}
