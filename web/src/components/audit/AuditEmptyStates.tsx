import type { ReactNode } from "react";

interface PanelProps {
  title: string;
  body: ReactNode;
}

function Panel({ title, body }: PanelProps) {
  return (
    <div className="flex flex-1 flex-col items-center justify-center gap-3 px-6 py-16 text-center">
      <h3
        className="font-display text-[28px] leading-none tracking-[-0.01em] text-ink-muted"
        style={{ fontWeight: 400, fontStyle: "italic" }}
      >
        {title}
      </h3>
      <div className="max-w-md text-[12.5px] text-ink-muted">{body}</div>
    </div>
  );
}

/** Periscope binary started with audit.enabled=false (or sink failed to open). */
export function AuditNotEnabledState() {
  return (
    <Panel
      title="audit history is not enabled"
      body={
        <>
          Periscope persists privileged actions only when{" "}
          <code className="rounded bg-surface-2 px-1 py-0.5 font-mono text-[11px]">
            audit.enabled=true
          </code>{" "}
          in the chart values. Audit events are still emitted to stdout — ship
          them via your log aggregator, or enable persistence to query them
          here.
          <br />
          <br />
          Reference:{" "}
          <a
            href="https://github.com/gnana997/periscope/blob/main/docs/setup/audit.md"
            className="text-accent underline-offset-2 hover:underline"
            target="_blank"
            rel="noreferrer"
          >
            docs/setup/audit.md
          </a>
        </>
      }
    />
  );
}

/** Audit IS enabled but no events have been recorded yet. */
export function AuditNoEventsYetState() {
  return (
    <Panel
      title="no actions recorded yet"
      body={
        <>
          The audit pipeline is wired but the table is empty. Trigger a
          privileged action — apply a YAML, scale a deployment, open a pod
          shell, or reveal a secret — then refresh.
        </>
      }
    />
  );
}

interface AuditEmptyFilteredProps {
  onClear: () => void;
}

/** Audit IS enabled and has events, but the current filter excludes all of them. */
export function AuditEmptyFilteredState({ onClear }: AuditEmptyFilteredProps) {
  return (
    <div className="flex flex-1 flex-col items-center justify-center gap-4 px-6 py-16 text-center">
      <h3
        className="font-display text-[24px] leading-none tracking-[-0.01em] text-ink-muted"
        style={{ fontWeight: 400, fontStyle: "italic" }}
      >
        no events match this filter
      </h3>
      <button
        type="button"
        onClick={onClear}
        className="rounded-md border border-border bg-surface px-3 py-1.5 text-[12px] text-ink hover:border-border-strong hover:bg-surface-2"
      >
        clear filters
      </button>
    </div>
  );
}
