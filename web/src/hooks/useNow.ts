// useNow — returns the current epoch ms, refreshed on a fixed interval.
// Use when a render needs to reflect "wall-clock time right now"
// (idle countdowns, uptime pills, ago-from-now formatters) and a
// stable, pure render is required (react-hooks/purity bans calling
// Date.now() during render).
//
// The default 1s tick is fine for second-level UIs; pass a longer
// interval when only minute-level freshness matters to avoid extra
// re-renders.

import { useEffect, useState } from "react";

export function useNow(intervalMs: number = 1000): number {
  const [now, setNow] = useState<number>(() => Date.now());
  useEffect(() => {
    const id = window.setInterval(() => setNow(Date.now()), intervalMs);
    return () => window.clearInterval(id);
  }, [intervalMs]);
  return now;
}
