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

export function phaseTone(phase: string): Tone {
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
