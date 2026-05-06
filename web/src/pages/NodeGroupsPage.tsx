import { useState } from "react";
import { useNodegroup, useNodegroups } from "../hooks/useNodegroups";
import { isAWSForbidden, isAWSThrottled, isBackendNotEKS } from "../lib/api";
import { cn } from "../lib/cn";
import { classifyDrift } from "../lib/nodegroups";
import type { NodegroupSummary } from "../lib/types";

// NodeGroupsPage — dedicated view for managed node groups (issue
// #103, sequenced PR-2 / PR-3).
//
// Layout: a table of nodegroups; click a row to expand the full
// detail panel. CUSTOM AMI rows get a "drift not tracked" badge
// (the issue's prescribed UX); for AWS-managed nodegroups, the
// drift column is empty in PR-2 and populated in PR-3.

export function NodeGroupsPage({ cluster }: { cluster: string }) {
  const { data, isLoading, isError, error } = useNodegroups(cluster);

  if (isError && isBackendNotEKS(error)) {
    return (
      <div className="px-6 py-8">
        <h1 className="mb-2 text-[16px] font-medium">Node groups</h1>
        <p className="text-[13px] text-ink-faint">
          Managed node group introspection is an EKS feature; this
          cluster is not backed by EKS, so no node group data is
          available.
        </p>
      </div>
    );
  }

  if (isError && isAWSForbidden(error)) {
    return (
      <div className="px-6 py-8">
        <h1 className="mb-2 text-[16px] font-medium">Node groups</h1>
        <p className="text-[13px] text-ink-faint">
          Periscope's AWS role does not have permission to read managed
          node groups for this cluster. Required IAM actions:{" "}
          <code className="font-mono text-[12px]">eks:ListNodegroups</code>,{" "}
          <code className="font-mono text-[12px]">eks:DescribeNodegroup</code>,
          plus{" "}
          <code className="font-mono text-[12px]">ssm:GetParameter</code> and{" "}
          <code className="font-mono text-[12px]">ec2:DescribeImages</code>{" "}
          for the AMI drift lookups. See{" "}
          <code className="font-mono text-[12px]">docs/setup/deploy.md</code>.
        </p>
      </div>
    );
  }

  if (isError && isAWSThrottled(error)) {
    return (
      <div className="px-6 py-8">
        <h1 className="mb-2 text-[16px] font-medium">Node groups</h1>
        <p className="text-[13px] text-ink-faint">
          AWS rate-limited this request. Refresh the page in a moment;
          the cache will absorb subsequent calls.
        </p>
      </div>
    );
  }

  if (isLoading) {
    return (
      <div className="flex h-full items-center justify-center">
        <span className="block size-4 animate-spin rounded-full border-[1.5px] border-border-strong border-t-accent" />
      </div>
    );
  }

  if (isError) {
    return (
      <div className="px-6 py-8">
        <h1 className="mb-2 text-[16px] font-medium">Node groups</h1>
        <p className="text-[13px] text-red">
          Failed to load node groups:{" "}
          {(error as Error)?.message ?? "unknown error"}.
        </p>
      </div>
    );
  }

  if (!data) return null;

  const rows = data.nodegroups;

  return (
    <div className="flex h-full min-h-0 flex-col overflow-y-auto px-6 py-5">
      <header className="mb-4">
        <h1 className="text-[16px] font-medium">Node groups</h1>
        <p className="mt-0.5 text-[12px] text-ink-faint">
          {data.counts.total} total · {data.counts.healthy} healthy
          {data.counts.custom > 0 && ` · ${data.counts.custom} custom`}
          {data.nodegroups.some((n) => n.driftComputed) &&
            ` · ${data.counts.behind} behind`}
        </p>
      </header>

      {rows.length === 0 ? (
        <p className="text-[13px] text-ink-faint">
          This cluster has no managed node groups. Self-managed nodes (raw
          EC2 / Karpenter / Fargate profiles) are not surfaced here.
        </p>
      ) : (
        <div className="overflow-hidden rounded-md border border-border bg-surface">
          <table className="w-full text-[12.5px]">
            <thead className="border-b border-border bg-surface-2/40 text-[10px] uppercase tracking-[0.08em] text-ink-faint">
              <tr>
                <th className="px-3 py-2 text-left">Name</th>
                <th className="px-3 py-2 text-left">AMI</th>
                <th className="px-3 py-2 text-left">Release</th>
                <th className="px-3 py-2 text-left">Status</th>
                <th className="px-3 py-2 text-left">Scale</th>
                <th className="px-3 py-2 text-left">Drift</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((row) => (
                <NodegroupRow
                  key={row.name}
                  cluster={cluster}
                  ng={row}
                />
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

function NodegroupRow({
  cluster,
  ng,
}: {
  cluster: string;
  ng: NodegroupSummary;
}) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <tr
        className={cn(
          "cursor-pointer border-b border-border last:border-0 transition-colors hover:bg-surface-2",
          open && "bg-surface-2",
        )}
        onClick={() => setOpen((v) => !v)}
      >
        <td className="px-3 py-2 font-mono">{ng.name}</td>
        <td className="px-3 py-2">
          {ng.customAmi ? (
            <span className="rounded-sm border border-border-strong px-1.5 py-px font-mono text-[10px] uppercase tracking-[0.06em] text-ink-muted">
              custom
            </span>
          ) : (
            <span className="font-mono text-ink-muted">{ng.amiType || "—"}</span>
          )}
        </td>
        <td className="px-3 py-2 font-mono text-ink-muted">
          {ng.releaseVersion || (ng.customAmi ? "—" : "")}
        </td>
        <td className="px-3 py-2">
          <StatusBadge status={ng.status} />
        </td>
        <td className="px-3 py-2 font-mono tabular-nums text-ink-muted">
          {ng.desiredSize}
          <span className="text-ink-faint">
            {" "}({ng.minSize}–{ng.maxSize})
          </span>
        </td>
        <td className="px-3 py-2">
          <DriftCell ng={ng} />
        </td>
      </tr>
      {open && (
        <tr>
          <td colSpan={6} className="border-b border-border bg-surface-2/40 px-6 py-3">
            <NodegroupDetailBody cluster={cluster} name={ng.name} />
          </td>
        </tr>
      )}
    </>
  );
}

function StatusBadge({ status }: { status: string }) {
  const tone =
    status === "ACTIVE"
      ? "text-green"
      : status === "CREATE_FAILED" || status === "DELETE_FAILED" || status === "DEGRADED" || status === "DEGRADED_DESCRIBE"
      ? "text-red"
      : "text-ink-muted";
  return (
    <span className={cn("font-mono text-[11px] uppercase tracking-[0.06em]", tone)}>
      {status || "unknown"}
    </span>
  );
}

function DriftCell({ ng }: { ng: NodegroupSummary }) {
  const label = classifyDrift(ng);
  switch (label.kind) {
    case "custom":
      return (
        <span
          className="text-[11px] italic text-ink-faint"
          title="AWS does not publish a 'latest' for custom AMIs; freshness is the operator's responsibility."
        >
          not tracked
        </span>
      );
    case "uncomputed":
      return <span className="text-[11px] text-ink-faint">—</span>;
    case "current":
      return <span className="text-[11px] font-mono text-green">current</span>;
    case "behind":
      return (
        <span
          className="text-[11px] font-mono text-yellow"
          title={`Latest release: ${label.latest ?? "unknown"}`}
        >
          {label.days}d behind
        </span>
      );
  }
}

function NodegroupDetailBody({
  cluster,
  name,
}: {
  cluster: string;
  name: string;
}) {
  const { data, isLoading, isError, error } = useNodegroup(cluster, name);
  if (isLoading) {
    return <p className="text-[12px] italic text-ink-faint">Loading…</p>;
  }
  if (isError) {
    return (
      <p className="text-[12px] text-red">
        Failed to load detail: {(error as Error)?.message ?? "unknown error"}.
      </p>
    );
  }
  if (!data) return null;

  return (
    <div className="space-y-3">
      <FieldGrid>
        <Field label="Kubernetes version">
          {data.kubernetesVersion ?? "—"}
        </Field>
        <Field label="Capacity type">{data.capacityType ?? "—"}</Field>
        <Field label="Disk size">
          {data.diskSize ? `${data.diskSize} GiB` : "—"}
        </Field>
        <Field label="Created">
          {data.createdAt ? new Date(data.createdAt).toUTCString() : "—"}
        </Field>
      </FieldGrid>

      {data.instanceTypes && data.instanceTypes.length > 0 && (
        <Section label="Instance types">
          <div className="flex flex-wrap gap-1 font-mono text-[11px]">
            {data.instanceTypes.map((t) => (
              <span
                key={t}
                className="rounded-sm border border-border bg-surface px-1.5 py-px text-ink-muted"
              >
                {t}
              </span>
            ))}
          </div>
        </Section>
      )}

      {data.launchTemplate && (
        <Section label="Launch template">
          <div className="font-mono text-[11.5px]">
            {data.launchTemplate.id ?? data.launchTemplate.name ?? "—"}
            {data.launchTemplate.version &&
              ` · version ${data.launchTemplate.version}`}
          </div>
          {data.customAmi && (
            <p className="mt-1 text-[11.5px] italic text-ink-faint">
              Custom AMI: launch template controls the image. AWS-side
              drift detection does not apply.
            </p>
          )}
        </Section>
      )}

      {data.healthIssues && data.healthIssues.length > 0 && (
        <Section label="Health issues">
          <ul className="space-y-1.5 text-[12px]">
            {data.healthIssues.map((issue, i) => (
              <li key={i} className="text-red">
                <span className="font-mono">{issue.code}</span>
                {issue.message && <> — {issue.message}</>}
                {issue.resourceIds && issue.resourceIds.length > 0 && (
                  <div className="ml-3 mt-1 font-mono text-[11px] text-ink-faint">
                    {issue.resourceIds.join(", ")}
                  </div>
                )}
              </li>
            ))}
          </ul>
        </Section>
      )}

      {data.subnets && data.subnets.length > 0 && (
        <Section label="Subnets">
          <div className="font-mono text-[11.5px] text-ink-muted">
            {data.subnets.join(", ")}
          </div>
        </Section>
      )}

      {data.autoScalingGroups && data.autoScalingGroups.length > 0 && (
        <Section label="Auto Scaling groups">
          <div className="font-mono text-[11.5px] text-ink-muted">
            {data.autoScalingGroups.join(", ")}
          </div>
        </Section>
      )}

      {data.nodeRole && (
        <Section label="Node IAM role">
          <div className="break-all font-mono text-[11px] text-ink-muted">
            {data.nodeRole}
          </div>
        </Section>
      )}
    </div>
  );
}

function Section({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <div>
      <div className="mb-1 font-mono text-[10px] uppercase tracking-[0.08em] text-ink-faint">
        {label}
      </div>
      {children}
    </div>
  );
}

function FieldGrid({ children }: { children: React.ReactNode }) {
  return (
    <div className="grid grid-cols-2 gap-x-6 gap-y-2 lg:grid-cols-4">
      {children}
    </div>
  );
}

function Field({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <div>
      <div className="font-mono text-[10px] uppercase tracking-[0.08em] text-ink-faint">
        {label}
      </div>
      <div className="mt-0.5 text-[12.5px]">{children}</div>
    </div>
  );
}
