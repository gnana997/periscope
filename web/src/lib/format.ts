/**
 * Formatting helpers for the UI layer.
 */

/**
 * Convert a creation timestamp into a kubectl-style age string:
 * 47s, 12m, 4h, 2d, 14d, 32d.
 */
export function ageFrom(iso: string, now: Date = new Date()): string {
  const t = new Date(iso).getTime();
  if (Number.isNaN(t)) return "—";
  const sec = Math.max(0, Math.floor((now.getTime() - t) / 1000));
  if (sec < 60) return `${sec}s`;
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min}m`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr}h`;
  const d = Math.floor(hr / 24);
  return `${d}d`;
}

/** Lowercase substring match. */
export function nameMatches(name: string, query: string): boolean {
  if (!query) return true;
  return name.toLowerCase().includes(query.toLowerCase());
}

/** Collapses multiple ports into a compact display string. */
export function formatPorts(
  ports: { protocol: string; port: number; targetPort: string }[],
): string {
  if (ports.length === 0) return "—";
  return ports
    .map((p) => `${p.port}→${p.targetPort}/${p.protocol}`)
    .join(", ");
}
