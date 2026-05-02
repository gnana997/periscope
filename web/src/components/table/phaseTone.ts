// phaseTone — classify a (kubectl-style) computed pod status string —
// plus a few non-pod phases like Namespace.Active — into a UI tone.
// The string space is wide on purpose: the backend emits
// CrashLoopBackOff, ImagePullBackOff, Init:0/2, ExitCode:137, etc., so
// this needs to handle them all rather than a closed enum.
//
// Lives separately from StatusDot.tsx so that file stays component-only
// (eslint react-refresh/only-export-components).

export type Tone = "green" | "yellow" | "red" | "muted";

const GREEN_PHASES = new Set([
  "Running",
  "Active",
  "Completed",
  "Succeeded",
  "Bound",
]);

const YELLOW_PHASES = new Set([
  "Pending",
  "Terminating",
  "ContainerCreating",
  "PodInitializing",
  "NotReady",
]);

const RED_PHASES = new Set([
  "Failed",
  "Error",
  "Evicted",
  "CrashLoopBackOff",
  "ImagePullBackOff",
  "ErrImagePull",
  "OOMKilled",
  "RunContainerError",
  "ContainerCannotRun",
  "CreateContainerError",
  "CreateContainerConfigError",
  "InvalidImageName",
  "DeadlineExceeded",
  "Lost",
]);

export function phaseTone(phase: string): Tone {
  if (GREEN_PHASES.has(phase)) return "green";
  // Init:N/M (progress) is yellow; Init:<reason> (error) is red.
  if (/^Init:\d+\/\d+$/.test(phase)) return "yellow";
  if (phase.startsWith("Init:")) return "red";
  if (YELLOW_PHASES.has(phase)) return "yellow";
  if (RED_PHASES.has(phase)) return "red";
  // Signal:N or ExitCode:N (terminated without a friendly reason).
  if (phase.startsWith("Signal:") || phase.startsWith("ExitCode:")) return "red";
  return "muted";
}
