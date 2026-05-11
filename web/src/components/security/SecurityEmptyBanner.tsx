// SecurityEmptyBanner — hairline strip shown once per cluster on
// every CVE-aware page when Inspector v2 is disabled (Helm-side or
// AWS-account-side). Dismissal is cluster-scoped via localStorage
// so "dismissed on Pods" carries over to Nodes.
//
// Renders null when:
//   - cluster is empty (defensive),
//   - cveStatus query is still in flight (avoid flicker),
//   - inspector is enabled,
//   - the operator has dismissed the banner for this cluster.

import { useState } from "react";
import { useCveStatus } from "../../hooks/useCve";
import { cn } from "../../lib/cn";
import { isBannerVisible } from "../../lib/cve";

interface Props {
  cluster: string;
  className?: string;
}

const STORAGE_PREFIX = "periscope.cve.banner.dismissed.";

export function SecurityEmptyBanner({ cluster, className }: Props) {
  const { data } = useCveStatus(cluster);
  // Bump on dismiss so the component re-derives `isDismissed(cluster)`
  // from localStorage. Avoids a useEffect→setState cascade (the
  // react-hooks/set-state-in-effect rule flags that pattern).
  const [, bump] = useState(0);
  const dismissed = isDismissed(cluster);

  if (!isBannerVisible({ cluster, status: data, dismissed })) return null;

  return (
    <div
      className={cn(
        "flex items-center gap-3 border-b border-yellow/40 bg-yellow/5 px-5 py-2 font-mono text-[11.5px] text-ink-muted",
        className,
      )}
    >
      <span className="font-semibold uppercase tracking-[0.14em] text-yellow">
        CVE scan
      </span>
      <span>
        Inspector v2 is not enabled on this cluster — vulnerability data is unavailable.{" "}
        <a
          href="/docs/usage/cve.md"
          target="_blank"
          rel="noreferrer"
          className="underline decoration-dotted underline-offset-2 hover:text-ink"
        >
          enable
        </a>
      </span>
      <button
        type="button"
        className="ml-auto rounded px-1.5 py-0.5 text-ink-faint transition-colors hover:bg-surface-2 hover:text-ink"
        onClick={() => {
          dismiss(cluster);
          bump((n) => n + 1);
        }}
        aria-label="Dismiss banner"
      >
        ✕
      </button>
    </div>
  );
}

function isDismissed(cluster: string): boolean {
  if (typeof window === "undefined") return false;
  try {
    return window.localStorage.getItem(STORAGE_PREFIX + cluster) === "1";
  } catch {
    return false;
  }
}

function dismiss(cluster: string): void {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(STORAGE_PREFIX + cluster, "1");
  } catch {
    // localStorage may be disabled (private mode); banner stays
    // visible until next mount.
  }
}
