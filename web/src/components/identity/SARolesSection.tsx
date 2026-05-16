// Section 2 of the Identity page: unified ServiceAccount → IAM Role
// index. Groups by namespace, expandable to per-SA row. Per-SA row
// renders one badge per binding (IRSA / PodIdentity), and a single
// "Both" warning chip when both bindings exist — Pod Identity wins
// at runtime so the IRSA annotation is shadowed dead config worth
// surfacing.

import { useMemo, useState } from "react";

import { cn } from "../../lib/cn";
import { isAWSForbidden, isAWSThrottled } from "../../lib/api";
import type { SARoleBinding, SARoleIndexEntry } from "../../lib/identity";

export function SARolesSection({
  data,
  isLoading,
  isError,
  error,
}: {
  data: SARoleIndexEntry[] | undefined;
  isLoading: boolean;
  isError: boolean;
  error: unknown;
}) {
  // Group entries by namespace once per data change.
  const byNamespace = useMemo(() => {
    const out: Map<string, SARoleIndexEntry[]> = new Map();
    for (const e of data ?? []) {
      const list = out.get(e.namespace);
      if (list) list.push(e);
      else out.set(e.namespace, [e]);
    }
    return [...out.entries()].sort(([a], [b]) => a.localeCompare(b));
  }, [data]);

  const [expanded, setExpanded] = useState<Set<string>>(() => new Set());

  if (isError && isAWSForbidden(error)) {
    return (
      <Frame title="ServiceAccount → IAM Role">
        <p className="text-[13px] text-ink-faint">
          Periscope&apos;s AWS role does not have permission to read
          IAM roles or Pod Identity associations. Required IAM:{" "}
          <code className="font-mono text-[12px]">
            eks:ListPodIdentityAssociations
          </code>
          ,{" "}
          <code className="font-mono text-[12px]">
            eks:DescribePodIdentityAssociation
          </code>
          ,{" "}
          <code className="font-mono text-[12px]">iam:GetRole</code>.
        </p>
      </Frame>
    );
  }

  if (isError && isAWSThrottled(error)) {
    return (
      <Frame title="ServiceAccount → IAM Role">
        <p className="text-[13px] text-ink-faint">
          AWS rate-limited this request. Refresh in a moment.
        </p>
      </Frame>
    );
  }

  // 503 — informer still syncing on cold start. The hook retries
  // automatically; we surface a soft pending state so users get a
  // hint instead of "loading" indefinitely.
  if (
    isError &&
    error &&
    typeof error === "object" &&
    "status" in error &&
    (error as { status: number }).status === 503
  ) {
    return (
      <Frame title="ServiceAccount → IAM Role">
        <p className="text-[13px] text-ink-faint">
          The ServiceAccount informer is still syncing. Auto-retrying…
        </p>
      </Frame>
    );
  }

  if (isError) {
    return (
      <Frame title="ServiceAccount → IAM Role">
        <p className="text-[13px] text-red">
          Failed to load SA → Role index:{" "}
          {(error as Error)?.message ?? "unknown error"}
        </p>
      </Frame>
    );
  }

  if (isLoading || !data) {
    return (
      <Frame title="ServiceAccount → IAM Role">
        <Spinner />
      </Frame>
    );
  }

  if (data.length === 0) {
    return (
      <Frame title="ServiceAccount → IAM Role">
        <p className="text-[13px] text-ink-faint">
          No ServiceAccounts in this cluster have an IAM role bound
          via IRSA or Pod Identity.
        </p>
      </Frame>
    );
  }

  return (
    <Frame title="ServiceAccount → IAM Role">
      <ul className="divide-y divide-border rounded-md border border-border bg-surface">
        {byNamespace.map(([ns, entries]) => {
          const isOpen = expanded.has(ns);
          return (
            <li key={ns}>
              <button
                type="button"
                onClick={() =>
                  setExpanded((prev) => {
                    const next = new Set(prev);
                    if (next.has(ns)) next.delete(ns);
                    else next.add(ns);
                    return next;
                  })
                }
                className="flex w-full items-center gap-2 px-3 py-2 text-left transition-colors hover:bg-surface-2/50"
              >
                <svg
                  width="11"
                  height="11"
                  viewBox="0 0 11 11"
                  className={cn(
                    "shrink-0 text-ink-faint transition-transform duration-200",
                    isOpen ? "rotate-90" : "rotate-0",
                  )}
                  aria-hidden
                >
                  <path
                    d="M3.5 2l4 3.5-4 3.5"
                    stroke="currentColor"
                    strokeWidth="1.4"
                    strokeLinecap="round"
                    strokeLinejoin="round"
                    fill="none"
                  />
                </svg>
                <span className="font-mono text-[12.5px] text-ink">
                  {ns}
                </span>
                <span className="font-mono text-[11px] text-ink-faint">
                  {entries.length} SA{entries.length === 1 ? "" : "s"}
                </span>
                {entries.some((e) => e.dualSource) && (
                  <span className="ml-auto rounded-sm border border-yellow/40 bg-yellow/10 px-1.5 py-0.5 text-[10.5px] uppercase tracking-wide text-yellow">
                    dual source present
                  </span>
                )}
              </button>

              <div
                className={cn(
                  "grid transition-all duration-200 ease-in-out",
                  isOpen ? "grid-rows-[1fr]" : "grid-rows-[0fr]",
                )}
              >
                <div className="overflow-hidden">
                  <ul className="divide-y divide-border">
                    {entries.map((e) => (
                      <SARow key={e.namespace + "/" + e.saName} entry={e} />
                    ))}
                  </ul>
                </div>
              </div>
            </li>
          );
        })}
      </ul>
    </Frame>
  );
}

function SARow({ entry }: { entry: SARoleIndexEntry }) {
  return (
    <li className="px-6 py-2">
      <div className="flex items-center gap-2">
        <span className="font-mono text-[12.5px] text-ink">
          {entry.saName}
        </span>
        {entry.dualSource && (
          <span
            className="rounded-sm border border-yellow/40 bg-yellow/10 px-1.5 py-0.5 text-[10.5px] uppercase tracking-wide text-yellow"
            title="IRSA annotation and Pod Identity association both present — Pod Identity takes precedence at runtime; the IRSA annotation is shadowed dead config."
          >
            both — Pod Identity wins
          </span>
        )}
      </div>
      <ul className="mt-1 space-y-0.5 pl-2">
        {entry.bindings.map((b, i) => (
          <BindingRow key={i} binding={b} />
        ))}
      </ul>
    </li>
  );
}

function BindingRow({ binding }: { binding: SARoleBinding }) {
  return (
    <li className="flex items-center gap-2">
      <SourceBadge source={binding.source} />
      <span
        className={cn(
          "flex-1 truncate font-mono text-[11.5px]",
          binding.roleExists ? "text-ink-muted" : "text-red",
        )}
      >
        {binding.roleArn}
      </span>
      {!binding.roleExists && (
        <span
          className="shrink-0 rounded-sm border border-red/30 bg-red/10 px-1.5 py-0.5 text-[10.5px] uppercase tracking-wide text-red"
          title="iam:GetRole returned NoSuchEntity for this role ARN, or Periscope's AWS role lacked permission to verify."
        >
          role not found
        </span>
      )}
    </li>
  );
}

function SourceBadge({ source }: { source: SARoleBinding["source"] }) {
  // Per-binding source. "Both" never appears on an individual binding
  // (it's a per-SA derived flag), but we render it defensively in
  // case the backend evolves to emit aggregated bindings.
  if (source === "PodIdentity") {
    return (
      <span className="shrink-0 rounded-sm border border-green/30 bg-green/10 px-1.5 py-0.5 font-mono text-[10.5px] uppercase tracking-wide text-green">
        pod identity
      </span>
    );
  }
  if (source === "IRSA") {
    return (
      <span className="shrink-0 rounded-sm border border-border bg-surface-2 px-1.5 py-0.5 font-mono text-[10.5px] uppercase tracking-wide text-ink-muted">
        irsa
      </span>
    );
  }
  return (
    <span className="shrink-0 rounded-sm border border-yellow/40 bg-yellow/10 px-1.5 py-0.5 font-mono text-[10.5px] uppercase tracking-wide text-yellow">
      both
    </span>
  );
}

function Frame({
  title,
  children,
}: {
  title: string;
  children: React.ReactNode;
}) {
  return (
    <section className="mb-6">
      <header className="mb-2">
        <h2 className="text-[14px] font-medium">{title}</h2>
      </header>
      {children}
    </section>
  );
}

function Spinner() {
  return (
    <span className="block size-4 animate-spin rounded-full border-[1.5px] border-border-strong border-t-accent" />
  );
}
