// Tone classifiers for describe panes — pure functions, no React.
// Lives separately from shared.tsx so React Refresh keeps that file
// component-only (eslint react-refresh/only-export-components).

import type { StatTone } from "./shared";

export function phaseStatTone(phase: string): StatTone {
  switch (phase) {
    case "Running":
    case "Active":
      return "green";
    case "Pending":
    case "Terminating":
      return "yellow";
    case "Failed":
    case "CrashLoopBackOff":
      return "red";
    default:
      return "muted";
  }
}

export function restartStatTone(n: number): StatTone {
  if (n > 5) return "red";
  if (n > 0) return "yellow";
  return "muted";
}

export function readyStatTone(ready: string): StatTone {
  const [r, t] = ready.split("/").map((n) => parseInt(n, 10));
  if (Number.isNaN(r) || Number.isNaN(t)) return "neutral";
  return r < t ? "yellow" : "neutral";
}
