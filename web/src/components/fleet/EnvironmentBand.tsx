import type { ReactNode } from "react";

interface EnvironmentBandProps {
  label: string;
  /** Right-aligned summary text — e.g. "3 clusters · all healthy". */
  summary?: string;
  /** Suppress the band header — useful when the page has only one band. */
  hideHeader?: boolean;
  children: ReactNode;
}

/**
 * EnvironmentBand groups a row of ClusterCards under a small-caps
 * label with an optional rollup line on the right. The grid uses
 * auto-fit to keep card width in a sensible 300–360px window so a
 * single card doesn't stretch awkwardly across an empty viewport,
 * while many cards still pack densely.
 */
export function EnvironmentBand({
  label,
  summary,
  hideHeader,
  children,
}: EnvironmentBandProps) {
  return (
    <section className="flex flex-col gap-3">
      {!hideHeader && (
        <header className="flex items-baseline justify-between gap-3 border-b border-border pb-1">
          <h2 className="font-mono text-[11px] uppercase tracking-[0.18em] text-ink-muted">
            {label}
          </h2>
          {summary && (
            <span className="font-mono text-[11px] text-ink-faint tabular">
              {summary}
            </span>
          )}
        </header>
      )}
      <div className="grid gap-3 grid-cols-[repeat(auto-fit,minmax(300px,360px))]">
        {children}
      </div>
    </section>
  );
}
