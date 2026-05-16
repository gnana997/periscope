// AWSAccessTab is the per-detail-pane composed view: identity
// chain header, server-emitted warnings, service-grouped
// permissions, raw statements, affected pods. One backend call
// returns everything — no joins, no chip catalog lookup on the
// SPA.
//
// Renders <LockedFeaturePane> when capabilities reports the tab
// unavailable (paywall pattern, not hide-on-403).

import { useNavigate } from "react-router-dom";
import { useState } from "react";

import { cn } from "../../lib/cn";
import { isAWSForbidden, isAWSThrottled, isBackendNotEKS } from "../../lib/api";
import type {
  AwsAccessWarning,
  Permission,
  PodRef,
  ServiceGroup,
  WorkloadKind,
} from "../../lib/identity";
import {
  useIdentityCapabilities,
  useWorkloadPermissions,
} from "../../hooks/useIdentity";
import { LockedFeaturePane } from "../ui/LockedFeaturePane";
import { SensitivePermChip } from "./SensitivePermChip";

export interface AWSAccessTabProps {
  cluster: string;
  kind: WorkloadKind;
  namespace: string;
  name: string;
}

export function AWSAccessTab({ cluster, kind, namespace, name }: AWSAccessTabProps) {
  const capQuery = useIdentityCapabilities(cluster);
  const [recheckPending, setRecheckPending] = useState(false);

  if (capQuery.isLoading) {
    return <Pending label="Checking AWS Access availability…" />;
  }

  const cap = capQuery.data?.features.awsAccessTab;
  if (cap && !cap.available) {
    return (
      <LockedFeaturePane
        feature={cap}
        title="AWS Access"
        probedAt={capQuery.data?.fetchedAt}
        recheckPending={recheckPending}
        onRecheck={async () => {
          setRecheckPending(true);
          try {
            await capQuery.recheck();
          } finally {
            setRecheckPending(false);
          }
        }}
      />
    );
  }

  return (
    <AWSAccessTabBody
      cluster={cluster}
      kind={kind}
      namespace={namespace}
      name={name}
      capNote={cap?.note}
    />
  );
}

function AWSAccessTabBody({
  cluster,
  kind,
  namespace,
  name,
  capNote,
}: AWSAccessTabProps & { capNote?: string }) {
  const wp = useWorkloadPermissions(cluster, kind, namespace, name);
  if (wp.isLoading) {
    return <Pending label="Resolving identity chain + IAM policies…" />;
  }
  if (wp.isError) {
    const err = wp.error;
    if (isBackendNotEKS(err)) {
      return <Empty label="AWS Access is only available on EKS-backed clusters." />;
    }
    if (isAWSForbidden(err)) {
      return (
        <Empty label="Periscope's AWS role lacks one or more IAM read permissions for this query." />
      );
    }
    if (isAWSThrottled(err)) {
      return <Empty label="AWS API is throttling Periscope's reads. Try again in a moment." />;
    }
    return <Empty label={`Couldn't load AWS Access: ${(err as Error).message}`} />;
  }
  const data = wp.data!;
  return (
    <div className="px-6 py-4">
      {capNote ? (
        <p className="mb-3 rounded-sm border border-border bg-surface-2 px-3 py-2 text-[12px] text-ink-muted">
          {capNote}
        </p>
      ) : null}
      <IdentityChainHeader chain={data.identityChain} />
      <WarningsBand warnings={data.warnings} />
      {data.policyFetchPartial ? (
        <p className="mb-3 text-[12.5px] text-amber-500">
          Partial policy fetch — some statements may be missing from the list below.
        </p>
      ) : null}
      <ServiceGroupsList groups={data.groups} />
      <RawStatementsList items={data.rawStatements} />
      <AffectedPodsFooter
        pods={data.affectedPods}
        total={data.affectedPodCount}
        cluster={cluster}
      />
      {data.truncated ? (
        <p className="mt-3 text-[12px] text-ink-faint">
          Showing {(data.groups ?? []).reduce((n, g) => n + (g.permissions?.length ?? 0), 0)} of {data.totalCount} permissions; filter on a service to narrow.
        </p>
      ) : null}
      <p className="mt-3 text-[11px] text-ink-faint">
        Catalog v{data.catalogVersion} · Fetched {new Date(data.fetchedAt).toLocaleTimeString()}
      </p>
    </div>
  );
}

function IdentityChainHeader({ chain }: { chain: import("../../lib/identity").IdentityChain }) {
  // Server emits `bindings: null` (not []) when the SA has no IAM
  // bindings — matches the NO_BINDINGS warning emitted in parallel.
  // Normalize once so every .length / .map call below is safe.
  const bindings = chain.bindings ?? [];
  return (
    <header className="mb-4">
      <h3 className="text-[14px] font-medium text-ink">Identity chain</h3>
      <p className="mt-1 text-[12.5px] text-ink-muted">
        ServiceAccount{" "}
        <code className="font-mono text-[12px] text-ink">{chain.serviceAccount}</code>
        {bindings.length > 0 ? " bound to" : " has no IAM role bindings."}
      </p>
      {bindings.length > 0 ? (
        <ul className="mt-2 divide-y divide-border rounded-md border border-border">
          {bindings.map((b, i) => (
            <li key={`${b.roleArn}-${i}`} className="flex flex-wrap items-center gap-2 px-3 py-2">
              <span
                className={cn(
                  "rounded-sm border px-1.5 py-0.5 font-mono text-[11px]",
                  b.source === "IRSA" && "border-blue-500/30 bg-blue-500/10 text-blue-500",
                  b.source === "PodIdentity" && "border-emerald-500/30 bg-emerald-500/10 text-emerald-500",
                  b.source === "Both" && "border-purple-500/30 bg-purple-500/10 text-purple-500",
                )}
              >
                {b.source}
              </span>
              <code className="flex-1 truncate font-mono text-[12px] text-ink">{b.roleArn}</code>
              {!b.roleExists ? (
                <span className="rounded-sm border border-red/30 bg-red/10 px-1.5 py-0.5 font-mono text-[11px] text-red">
                  role not found
                </span>
              ) : null}
            </li>
          ))}
        </ul>
      ) : null}
    </header>
  );
}

function WarningsBand({ warnings }: { warnings: AwsAccessWarning[] }) {
  if (!warnings || warnings.length === 0) return null;
  return (
    <div className="mb-4 space-y-1">
      {warnings.map((w, i) => (
        <p
          key={`${w.code}-${i}`}
          className="rounded-sm border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-[12.5px] text-amber-500"
        >
          <span className="font-mono uppercase">{w.code}</span> · {w.message}
        </p>
      ))}
    </div>
  );
}

function ServiceGroupsList({ groups }: { groups: ServiceGroup[] | null }) {
  const items = groups ?? [];
  if (items.length === 0) {
    return (
      <p className="my-3 text-[13px] text-ink-faint">
        No expanded permissions to render. (Empty role policies, or all statements are complex — see Raw statements below.)
      </p>
    );
  }
  return (
    <div className="mb-4 divide-y divide-border rounded-md border border-border">
      {items.map((g) => (
        <ServiceGroupRow key={g.service} group={g} />
      ))}
    </div>
  );
}

function ServiceGroupRow({ group }: { group: ServiceGroup }) {
  const [open, setOpen] = useState(group.sensitive);
  return (
    <div>
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        className="flex w-full items-center gap-2 px-3 py-2 text-left text-[13px] hover:bg-surface-2"
      >
        <span className="text-ink-faint">{open ? "▾" : "▸"}</span>
        <code className="font-mono text-ink">{group.service}</code>
        <span className="text-ink-muted">·</span>
        <span className="text-ink-muted">{group.count} statements</span>
        {group.sensitive ? (
          <span className="ml-auto rounded-sm border border-red/30 bg-red/10 px-1.5 py-0.5 font-mono text-[11px] text-red">
            sensitive
          </span>
        ) : null}
      </button>
      {open ? <PermissionsTable perms={group.permissions} /> : null}
    </div>
  );
}

function PermissionsTable({ perms }: { perms: Permission[] }) {
  return (
    <table className="w-full border-t border-border text-left text-[12.5px]">
      <thead>
        <tr className="bg-surface-2 text-ink-faint">
          <th className="px-3 py-1.5 font-medium">Effect</th>
          <th className="px-3 py-1.5 font-medium">Action</th>
          <th className="px-3 py-1.5 font-medium">Resource</th>
          <th className="px-3 py-1.5 font-medium">Flags</th>
        </tr>
      </thead>
      <tbody>
        {perms.map((p, i) => (
          <tr key={`${p.action}-${p.resource}-${i}`} className="border-t border-border">
            <td className="px-3 py-1.5">
              <span
                className={cn(
                  "rounded-sm px-1.5 py-0.5 font-mono text-[11px]",
                  p.effect === "Allow"
                    ? "border border-emerald-500/30 bg-emerald-500/10 text-emerald-500"
                    : "border border-red/30 bg-red/10 text-red",
                )}
              >
                {p.effect}
              </span>
            </td>
            <td className="px-3 py-1.5 font-mono text-ink">{p.action}</td>
            <td className="px-3 py-1.5 font-mono text-ink-muted">{p.resource}</td>
            <td className="px-3 py-1.5">
              <div className="flex flex-wrap gap-1">
                {p.sensitive && p.sensitiveReason ? (
                  <SensitivePermChip category={p.sensitiveReason} />
                ) : null}
                {p.hasCondition ? (
                  <span className="rounded-sm border border-border bg-surface-2 px-1.5 py-0.5 font-mono text-[11px] text-ink-muted">
                    condition (not evaluated)
                  </span>
                ) : null}
                {p.wildcard ? (
                  <span className="rounded-sm border border-amber-500/30 bg-amber-500/10 px-1.5 py-0.5 font-mono text-[11px] text-amber-500">
                    wildcard
                  </span>
                ) : null}
              </div>
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function RawStatementsList({
  items,
}: {
  items: import("../../lib/identity").RawStatement[];
}) {
  if (!items || items.length === 0) return null;
  return (
    <div className="mb-4">
      <h4 className="mb-1 text-[12.5px] font-medium text-ink">Complex statements</h4>
      <p className="mb-2 text-[12px] text-ink-faint">
        These statements use NotAction / NotResource / NotPrincipal patterns the v1.1 matcher doesn't project. Open them in the IAM console for the full picture.
      </p>
      <ul className="divide-y divide-border rounded-md border border-border">
        {items.map((s, i) => (
          <li key={`${s.policyName}-${s.statementIdx}-${i}`} className="px-3 py-2 text-[12.5px]">
            <span className="font-mono text-ink">{s.policyName}</span> · {s.summary}
            {s.consoleUrl ? (
              <>
                {" "}
                <a
                  href={s.consoleUrl}
                  className="text-accent hover:underline"
                  target="_blank"
                  rel="noreferrer"
                >
                  open in console →
                </a>
              </>
            ) : null}
          </li>
        ))}
      </ul>
    </div>
  );
}

function AffectedPodsFooter({
  pods,
  total,
  cluster,
}: {
  pods: PodRef[];
  total: number;
  cluster: string;
}) {
  const navigate = useNavigate();
  if (!pods || pods.length === 0) {
    return (
      <p className="mt-4 text-[12.5px] text-ink-faint">No matching pods are currently running.</p>
    );
  }
  return (
    <div className="mt-4">
      <p className="mb-1 text-[12.5px] font-medium text-ink">
        Affected pods{" "}
        <span className="font-normal text-ink-faint">
          ({pods.length} of {total})
        </span>
      </p>
      <ul className="divide-y divide-border rounded-md border border-border">
        {pods.map((pod) => (
          <li key={`${pod.namespace}/${pod.name}`} className="px-3 py-2 text-[12.5px]">
            <button
              type="button"
              onClick={() =>
                navigate(
                  `/clusters/${encodeURIComponent(cluster)}/pods?ns=${encodeURIComponent(
                    pod.namespace,
                  )}&sel=${encodeURIComponent(pod.name)}&selNs=${encodeURIComponent(
                    pod.namespace,
                  )}&tab=aws-access`,
                )
              }
              className="font-mono text-ink hover:underline"
            >
              {pod.namespace}/{pod.name}
            </button>
            {pod.nodeName ? (
              <span className="ml-2 text-ink-faint">on {pod.nodeName}</span>
            ) : null}
          </li>
        ))}
      </ul>
    </div>
  );
}

function Pending({ label }: { label: string }) {
  return <p className="px-6 py-6 text-[13px] text-ink-faint">{label}</p>;
}

function Empty({ label }: { label: string }) {
  return <p className="px-6 py-6 text-[13px] text-ink-muted">{label}</p>;
}
