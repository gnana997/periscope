/**
 * Shared EoSS (End of Standard Support) chip math used by the fleet
 * card and the cluster identity banner. Both consumers compute the
 * tone the same way; only the rendering policy differs (fleet card
 * hides "comfortable" tone by default; overview always shows it).
 */

export type SupportWindowTone = "comfortable" | "warning" | "past" | "eol";

export interface SupportWindowState {
  tone: SupportWindowTone;
  /** Days remaining until standard support ends. Negative when already past. */
  daysRemaining: number;
  /** ISO date (yyyy-mm-dd) of the EoSS boundary, useful for tooltips. */
  eosDate: string;
  /** ISO date of the extended-support boundary, when known. */
  eoExtendedDate?: string;
}

/**
 * Compute the support-window state for an EKS cluster.
 *
 * Returns null when no EoSS data is available (kubeconfig backend,
 * non-EKS-capable cluster, or missing eks:DescribeClusterVersions
 * permission). Otherwise returns a tone the renderer maps to a
 * color treatment:
 *
 *   comfortable  — > 180 days remaining
 *   warning      — ≤ 180 days remaining (still in standard support)
 *   past         — past EoSS, in extended support ($0.60/hr surcharge)
 *   eol          — past extended support, no AWS support at all
 */
export function supportWindowState(
  eos?: string,
  eoExtended?: string,
): SupportWindowState | null {
  if (!eos) return null;
  const eosDate = new Date(eos);
  if (Number.isNaN(eosDate.getTime())) return null;

  const now = Date.now();
  const eosMs = eosDate.getTime();
  const dayMs = 24 * 60 * 60 * 1000;
  const daysRemaining = Math.ceil((eosMs - now) / dayMs);

  const eosISO = eos.slice(0, 10);
  const eoExtendedISO = eoExtended ? eoExtended.slice(0, 10) : undefined;

  // Already past standard EoSS.
  if (now >= eosMs) {
    if (eoExtended) {
      const extDate = new Date(eoExtended);
      if (!Number.isNaN(extDate.getTime()) && now >= extDate.getTime()) {
        return {
          tone: "eol",
          daysRemaining,
          eosDate: eosISO,
          eoExtendedDate: eoExtendedISO,
        };
      }
    }
    return {
      tone: "past",
      daysRemaining,
      eosDate: eosISO,
      eoExtendedDate: eoExtendedISO,
    };
  }

  return {
    tone: daysRemaining <= 180 ? "warning" : "comfortable",
    daysRemaining,
    eosDate: eosISO,
    eoExtendedDate: eoExtendedISO,
  };
}

/**
 * Strip a K8s GitVersion ("v1.34.7-eks-abc1234") down to the
 * MAJOR.MINOR portion EKS uses as a version selector. Falls back
 * to the raw input when parsing fails so callers never show "".
 */
export function stripGitVersion(gitVersion: string): string {
  const m = gitVersion.match(/^v?(\d+\.\d+)/);
  return m ? m[1] : gitVersion;
}
