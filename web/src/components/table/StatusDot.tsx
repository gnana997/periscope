import { cn } from "../../lib/cn";

type Tone = "green" | "yellow" | "red" | "muted";

export function StatusDot({
  tone,
  className,
}: {
  tone: Tone;
  className?: string;
}) {
  const cls =
    tone === "green"
      ? "bg-green"
      : tone === "yellow"
        ? "bg-yellow"
        : tone === "red"
          ? "bg-red"
          : "bg-ink-faint";
  return (
    <span
      className={cn("block size-1.5 shrink-0 rounded-full", cls, className)}
    />
  );
}

export function PhaseTag({ phase }: { phase: string }) {
  const tone = phaseTone(phase);
  const colorCls =
    tone === "green"
      ? "text-green"
      : tone === "yellow"
        ? "text-yellow"
        : tone === "red"
          ? "text-red"
          : "text-ink-muted";
  return (
    <span className={cn("inline-flex items-center gap-1.5", colorCls)}>
      <StatusDot tone={tone} />
      <span>{phase}</span>
    </span>
  );
}

// phaseTone maps the (kubectl-style) computed pod status — plus a few
// non-pod phases like Namespace.Active — to a UI tone. The string space
// is wide on purpose: the backend emits CrashLoopBackOff, ImagePullBackOff,
// Init:0/2, ExitCode:137, etc., so this needs to handle them all rather
// than a closed enum.
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
