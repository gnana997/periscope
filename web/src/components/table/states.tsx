import type { ReactNode } from "react";

export function LoadingState({ resource }: { resource: string }) {
  return (
    <div className="flex flex-1 items-center justify-center px-6 py-16">
      <div className="flex items-center gap-3 text-[13px] text-ink-muted">
        <Spinner />
        <span>
          loading <span className="font-mono">{resource}</span>…
        </span>
      </div>
    </div>
  );
}

export function EmptyState({
  resource,
  namespace,
}: {
  resource: string;
  namespace: string | null;
}) {
  return (
    <div className="flex flex-1 flex-col items-center justify-center gap-2 px-6 py-16 text-center">
      <h3
        className="font-display text-[28px] leading-none tracking-[-0.01em] text-ink-muted"
        style={{ fontWeight: 400, fontStyle: "italic" }}
      >
        nothing here
      </h3>
      <p className="max-w-sm text-[12.5px] text-ink-muted">
        No <span className="font-mono">{resource}</span> visible
        {namespace ? (
          <>
            {" "}in <span className="font-mono">{namespace}</span>
          </>
        ) : (
          " in the namespaces you can access"
        )}
        .
      </p>
    </div>
  );
}

export function ErrorState({
  title,
  message,
  hint,
}: {
  title: string;
  message: string;
  hint?: ReactNode;
}) {
  return (
    <div className="flex flex-1 flex-col items-center justify-center gap-3 px-6 py-16 text-center">
      <div className="flex size-9 items-center justify-center rounded-full bg-red-soft text-red">
        <svg width="16" height="16" viewBox="0 0 16 16" aria-hidden>
          <path
            d="M8 1.5L1 14.5h14L8 1.5z"
            fill="none"
            stroke="currentColor"
            strokeWidth="1.4"
            strokeLinejoin="round"
          />
          <path
            d="M8 6.5v4M8 12.5v.5"
            stroke="currentColor"
            strokeWidth="1.4"
            strokeLinecap="round"
          />
        </svg>
      </div>
      <h3 className="text-[14px] font-medium text-ink">{title}</h3>
      <p className="max-w-md font-mono text-[11.5px] text-ink-muted">{message}</p>
      {hint && <div className="text-[12px] text-ink-muted">{hint}</div>}
    </div>
  );
}

export function NoClustersState() {
  return (
    <div className="flex flex-1 flex-col items-center justify-center gap-4 px-6 py-16 text-center">
      <h2
        className="font-display text-[40px] leading-none tracking-[-0.02em] text-ink"
        style={{ fontWeight: 400 }}
      >
        no clusters
      </h2>
      <p className="max-w-md text-[13px] text-ink-muted">
        Periscope didn't find any clusters in its registry. Set
        {" "}
        <code className="rounded bg-surface-2 px-1.5 py-0.5 font-mono text-[11.5px] text-ink">
          PERISCOPE_CLUSTERS_FILE
        </code>{" "}
        to a YAML file and restart.
      </p>
      <pre className="overflow-x-auto rounded-md border border-border bg-surface px-4 py-3 text-left font-mono text-[11px] leading-relaxed text-ink">
{`clusters:
  - name: kind-local
    backend: kubeconfig
    kubeconfigPath: ~/.kube/config
    kubeconfigContext: kind-kind`}
      </pre>
    </div>
  );
}

function Spinner() {
  return (
    <span
      aria-hidden
      className="block size-3.5 animate-spin rounded-full border-[1.5px] border-border-strong border-t-accent"
    />
  );
}
