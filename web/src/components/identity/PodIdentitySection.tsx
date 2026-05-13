// Section 3 of the Identity page: role-centric Pod Identity view.
// Same data as section 2 but inverted — grouped by role ARN with
// the (namespace, ServiceAccount) pairs that bind to it. Useful
// when an operator is investigating a specific IAM role and wants
// to know which workloads can assume it.

import { useState } from "react";

import { cn } from "../../lib/cn";
import { isAWSForbidden, isAWSThrottled } from "../../lib/api";
import type { PodIdentityResponse } from "../../lib/identity";

export function PodIdentitySection({
  data,
  isLoading,
  isError,
  error,
}: {
  data: PodIdentityResponse | undefined;
  isLoading: boolean;
  isError: boolean;
  error: unknown;
}) {
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set());

  if (isError && isAWSForbidden(error)) {
    return (
      <Frame title="Pod Identity associations">
        <p className="text-[13px] text-ink-faint">
          Periscope&apos;s AWS role does not have permission to read
          Pod Identity associations. Required IAM:{" "}
          <code className="font-mono text-[12px]">
            eks:ListPodIdentityAssociations
          </code>
          ,{" "}
          <code className="font-mono text-[12px]">
            eks:DescribePodIdentityAssociation
          </code>
          .
        </p>
      </Frame>
    );
  }

  if (isError && isAWSThrottled(error)) {
    return (
      <Frame title="Pod Identity associations">
        <p className="text-[13px] text-ink-faint">
          AWS rate-limited this request. Refresh in a moment.
        </p>
      </Frame>
    );
  }

  if (isError) {
    return (
      <Frame title="Pod Identity associations">
        <p className="text-[13px] text-red">
          Failed to load Pod Identity associations:{" "}
          {(error as Error)?.message ?? "unknown error"}
        </p>
      </Frame>
    );
  }

  if (isLoading || !data) {
    return (
      <Frame title="Pod Identity associations">
        <Spinner />
      </Frame>
    );
  }

  const roleArns = Object.keys(data.groups).sort();

  if (roleArns.length === 0) {
    return (
      <Frame title="Pod Identity associations">
        <p className="text-[13px] text-ink-faint">
          No Pod Identity associations configured on this cluster.
        </p>
      </Frame>
    );
  }

  return (
    <Frame title="Pod Identity associations">
      <ul className="divide-y divide-border rounded-md border border-border bg-surface">
        {roleArns.map((roleArn) => {
          const pairs = data.groups[roleArn];
          const isOpen = expanded.has(roleArn);
          return (
            <li key={roleArn}>
              <button
                type="button"
                onClick={() =>
                  setExpanded((prev) => {
                    const next = new Set(prev);
                    if (next.has(roleArn)) next.delete(roleArn);
                    else next.add(roleArn);
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
                <span className="flex-1 truncate font-mono text-[12.5px] text-ink">
                  {roleArn}
                </span>
                <span className="font-mono text-[11px] text-ink-faint">
                  {pairs.length}{" "}
                  {pairs.length === 1 ? "binding" : "bindings"}
                </span>
              </button>

              <div
                className={cn(
                  "grid transition-all duration-200 ease-in-out",
                  isOpen ? "grid-rows-[1fr]" : "grid-rows-[0fr]",
                )}
              >
                <div className="overflow-hidden">
                  <ul className="divide-y divide-border">
                    {pairs.map((p) => (
                      <li
                        key={p.associationId}
                        className="flex items-center gap-3 px-6 py-1.5"
                      >
                        <span className="font-mono text-[11.5px] text-ink-muted">
                          {p.namespace}
                        </span>
                        <span className="text-ink-faint">/</span>
                        <span className="font-mono text-[12px] text-ink">
                          {p.serviceAccount}
                        </span>
                        <span className="ml-auto font-mono text-[10.5px] text-ink-faint">
                          {p.associationId}
                        </span>
                      </li>
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
