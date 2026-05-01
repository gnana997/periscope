const SIZE = 88;
const STROKE = 8;
const R = (SIZE - STROKE) / 2;
const CX = SIZE / 2;
const CY = SIZE / 2;
const CIRCUMFERENCE = 2 * Math.PI * R;

function gaugeColor(percent: number): string {
  if (percent > 85) return "var(--color-red, #ef4444)";
  if (percent > 65) return "var(--color-yellow, #eab308)";
  return "var(--color-green, #22c55e)";
}

interface CircularGaugeProps {
  /** 0–100. Pass null when percent cannot be computed (no limit set). */
  percent: number | null;
  label: string;
  usageLabel: string;
  totalLabel?: string;
}

export function CircularGauge({ percent, label, usageLabel, totalLabel }: CircularGaugeProps) {
  const hasPct = percent !== null && percent >= 0;
  const filled = hasPct ? Math.min(percent!, 100) : 0;
  const offset = CIRCUMFERENCE * (1 - filled / 100);
  const color = hasPct ? gaugeColor(percent!) : "var(--color-ink-faint, #6b7280)";
  const displayPct = hasPct ? `${Math.round(percent!)}%` : "—";

  return (
    <div className="flex flex-col items-center gap-1.5">
      <svg
        width={SIZE}
        height={SIZE}
        viewBox={`0 0 ${SIZE} ${SIZE}`}
        aria-label={`${label}: ${displayPct}`}
      >
        {/* Track */}
        <circle
          cx={CX}
          cy={CY}
          r={R}
          fill="none"
          stroke="currentColor"
          strokeWidth={STROKE}
          className="text-border"
        />
        {/* Progress arc — rotated so it starts from 12 o'clock */}
        <circle
          cx={CX}
          cy={CY}
          r={R}
          fill="none"
          stroke={color}
          strokeWidth={STROKE}
          strokeDasharray={CIRCUMFERENCE}
          strokeDashoffset={offset}
          strokeLinecap="round"
          transform={`rotate(-90 ${CX} ${CY})`}
          style={{ transition: "stroke-dashoffset 0.4s ease" }}
        />
        {/* Percentage */}
        <text
          x={CX}
          y={CY - 6}
          textAnchor="middle"
          dominantBaseline="middle"
          style={{
            fill: color,
            fontSize: "15px",
            fontWeight: 700,
            fontFamily: "var(--font-mono, monospace)",
          }}
        >
          {displayPct}
        </text>
        {/* Label */}
        <text
          x={CX}
          y={CY + 12}
          textAnchor="middle"
          style={{
            fill: "currentColor",
            fontSize: "10px",
            fontFamily: "var(--font-sans, sans-serif)",
          }}
          className="text-ink-muted"
        >
          {label}
        </text>
      </svg>

      {/* Usage / total below the gauge */}
      <div className="text-center">
        <span className="font-mono text-[11.5px] text-ink">{usageLabel}</span>
        {totalLabel && (
          <span className="font-mono text-[11px] text-ink-faint"> / {totalLabel}</span>
        )}
      </div>
    </div>
  );
}

/** Skeleton shown while metrics are loading */
export function CircularGaugeSkeleton({ label }: { label: string }) {
  return (
    <div className="flex flex-col items-center gap-1.5">
      <svg width={SIZE} height={SIZE} viewBox={`0 0 ${SIZE} ${SIZE}`}>
        <circle
          cx={CX} cy={CY} r={R}
          fill="none" stroke="currentColor" strokeWidth={STROKE}
          className="text-border animate-pulse"
        />
        <text x={CX} y={CY} textAnchor="middle" dominantBaseline="middle"
          style={{ fill: "currentColor", fontSize: "10px" }}
          className="text-ink-faint">
          {label}
        </text>
      </svg>
      <div className="h-3 w-14 animate-pulse rounded bg-border" />
    </div>
  );
}
