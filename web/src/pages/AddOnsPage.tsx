import { useState } from "react";
import { useAddon, useAddons } from "../hooks/useAddons";
import { isAWSForbidden, isAWSThrottled, isBackendNotEKS } from "../lib/api";
import { cn } from "../lib/cn";
import type { AddonHealthGlyph, AddonSummary } from "../lib/types";

// AddOnsPage — dedicated /clusters/{c}/addons view (issue #117).
//
// Layout: a table of installed add-ons; click a row to expand the
// detail panel (version history, compat matrix, IAM service-account
// ARN, health issues). Three glyphs (●/▲/✕) match the issue mockup.
// Pairs with Upgrade Insights — the operator can see "what's
// installed" alongside the AWS-side "what should change before next
// minor".

export function AddOnsPage({ cluster }: { cluster: string }) {
  const { data, isLoading, isError, error } = useAddons(cluster);

  if (isError && isBackendNotEKS(error)) {
    return (
      <div className="px-6 py-8">
        <h1 className="mb-2 text-[16px] font-medium">EKS add-ons</h1>
        <p className="text-[13px] text-ink-faint">
          Add-on introspection is an EKS feature; this cluster is not
          backed by EKS, so no add-on data is available.
        </p>
      </div>
    );
  }

  if (isError && isAWSForbidden(error)) {
    return (
      <div className="px-6 py-8">
        <h1 className="mb-2 text-[16px] font-medium">EKS add-ons</h1>
        <p className="text-[13px] text-ink-faint">
          Periscope's AWS role does not have permission to read add-ons
          for this cluster. Required IAM actions:{" "}
          <code className="font-mono text-[12px]">eks:ListAddons</code>,{" "}
          <code className="font-mono text-[12px]">eks:DescribeAddon</code>{" "}
          (scoped to the addon resource), and{" "}
          <code className="font-mono text-[12px]">
            eks:DescribeAddonVersions
          </code>
          . See{" "}
          <code className="font-mono text-[12px]">docs/setup/deploy.md</code>.
        </p>
      </div>
    );
  }

  if (isError && isAWSThrottled(error)) {
    return (
      <div className="px-6 py-8">
        <h1 className="mb-2 text-[16px] font-medium">EKS add-ons</h1>
        <p className="text-[13px] text-ink-faint">
          AWS rate-limited this request. Refresh in a moment; the cache
          will absorb subsequent calls.
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
        <h1 className="mb-2 text-[16px] font-medium">EKS add-ons</h1>
        <p className="text-[13px] text-red">
          Failed to load add-ons:{" "}
          {(error as Error)?.message ?? "unknown error"}.
        </p>
      </div>
    );
  }

  if (!data) return null;
  const rows = data.addons;

  return (
    <div className="flex h-full min-h-0 flex-col overflow-y-auto px-6 py-5">
      <header className="mb-4">
        <h1 className="text-[16px] font-medium">
          EKS add-ons{" "}
          {data.clusterKubernetesVersion && (
            <span className="ml-2 font-mono text-[12px] text-ink-faint">
              · k8s {data.clusterKubernetesVersion}
            </span>
          )}
        </h1>
        <p className="mt-0.5 text-[12px] text-ink-faint">
          {data.counts.total} total · {data.counts.healthy} healthy
          {data.counts.updateAvailable > 0 &&
            ` · ${data.counts.updateAvailable} update available`}
          {data.counts.unhealthy > 0 &&
            ` · ${data.counts.unhealthy} failing`}
          {data.counts.blocksNextMinor > 0 &&
            ` · ${data.counts.blocksNextMinor} blocks next k8s`}
        </p>
      </header>

      {rows.length === 0 ? (
        <p className="text-[13px] text-ink-faint">
          This cluster has no managed add-ons. Self-managed add-ons
          (operator-deployed coredns/kube-proxy via Helm) are not
          surfaced here — EKS does not track them.
        </p>
      ) : (
        <div className="overflow-hidden rounded-md border border-border bg-surface">
          <table className="w-full text-[12.5px]">
            <thead className="border-b border-border bg-surface-2/40 text-[10px] uppercase tracking-[0.08em] text-ink-faint">
              <tr>
                <th className="px-3 py-2 text-left">Health</th>
                <th className="px-3 py-2 text-left">Name</th>
                <th className="px-3 py-2 text-left">Installed</th>
                <th className="px-3 py-2 text-left">Latest</th>
                <th className="px-3 py-2 text-left">Compat (k8s)</th>
                <th className="px-3 py-2 text-left">Status</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((row) => (
                <AddonRow key={row.name} cluster={cluster} addon={row} />
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

function AddonRow({
  cluster,
  addon,
}: {
  cluster: string;
  addon: AddonSummary;
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
        <td className="px-3 py-2">
          <Glyph glyph={addon.healthGlyph} />
        </td>
        <td className="px-3 py-2 font-mono">
          {addon.name}
          {addon.blocksNextMinor && (
            <div className="text-[10.5px] text-yellow">
              blocks next k8s minor
            </div>
          )}
        </td>
        <td className="px-3 py-2 font-mono text-ink-muted">
          {addon.version || "—"}
        </td>
        <td className="px-3 py-2 font-mono text-ink-muted">
          {addon.updateAvailable ? (
            <span className="text-yellow">{addon.latestVersion ?? "—"}</span>
          ) : addon.latestVersion ? (
            <span>{addon.latestVersion}</span>
          ) : (
            "—"
          )}
        </td>
        <td className="px-3 py-2 font-mono text-[11.5px] text-ink-muted">
          {addon.compatMinK8s && addon.compatMaxK8s
            ? `${addon.compatMinK8s} – ${addon.compatMaxK8s}`
            : "—"}
        </td>
        <td className="px-3 py-2">
          <StatusBadge status={addon.status} />
        </td>
      </tr>
      {open && (
        <tr>
          <td
            colSpan={6}
            className="border-b border-border bg-surface-2/40 px-6 py-3"
          >
            <AddonDetailBody cluster={cluster} name={addon.name} />
          </td>
        </tr>
      )}
    </>
  );
}

function Glyph({ glyph }: { glyph: AddonHealthGlyph }) {
  switch (glyph) {
    case "ok":
      return (
        <span className="font-mono text-[14px] text-green" aria-label="healthy">
          ●
        </span>
      );
    case "update":
      return (
        <span
          className="font-mono text-[14px] text-yellow"
          aria-label="update available"
        >
          ▲
        </span>
      );
    case "fail":
      return (
        <span className="font-mono text-[14px] text-red" aria-label="failing">
          ✕
        </span>
      );
  }
}

function StatusBadge({ status }: { status: string }) {
  const tone =
    status === "ACTIVE"
      ? "text-green"
      : status === "CREATE_FAILED" ||
        status === "DELETE_FAILED" ||
        status === "DEGRADED" ||
        status === "UPDATE_FAILED" ||
        status === "DEGRADED_DESCRIBE"
      ? "text-red"
      : "text-ink-muted";
  return (
    <span
      className={cn(
        "font-mono text-[11px] uppercase tracking-[0.06em]",
        tone,
      )}
    >
      {status || "unknown"}
    </span>
  );
}

function AddonDetailBody({
  cluster,
  name,
}: {
  cluster: string;
  name: string;
}) {
  const { data, isLoading, isError, error } = useAddon(cluster, name);
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
        <Field label="Installed version">{data.version ?? "—"}</Field>
        <Field label="Latest version">{data.latestVersion ?? "—"}</Field>
        <Field label="Status">{data.status ?? "—"}</Field>
        <Field label="Modified">
          {data.modifiedAt ? new Date(data.modifiedAt).toUTCString() : "—"}
        </Field>
      </FieldGrid>

      {data.serviceAccountRoleArn && (
        <Section label="IAM service account role">
          <div className="break-all font-mono text-[11px] text-ink-muted">
            {data.serviceAccountRoleArn}
          </div>
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

      {data.availableVersions && data.availableVersions.length > 0 && (
        <Section label="Version history">
          <div className="overflow-hidden rounded-sm border border-border">
            <table className="w-full text-[11.5px]">
              <thead className="border-b border-border bg-surface text-[10px] uppercase tracking-[0.06em] text-ink-faint">
                <tr>
                  <th className="px-2 py-1 text-left">Version</th>
                  <th className="px-2 py-1 text-left">Compatible k8s</th>
                  <th className="px-2 py-1 text-left">Default</th>
                </tr>
              </thead>
              <tbody>
                {data.availableVersions.map((v) => {
                  const isInstalled = v.version === data.version;
                  return (
                    <tr
                      key={v.version}
                      className={cn(
                        "border-b border-border last:border-0",
                        isInstalled && "bg-accent-soft/30",
                      )}
                    >
                      <td className="px-2 py-1 font-mono">
                        {v.version}
                        {isInstalled && (
                          <span className="ml-2 text-[10px] uppercase tracking-[0.06em] text-accent">
                            installed
                          </span>
                        )}
                      </td>
                      <td className="px-2 py-1 font-mono text-ink-muted">
                        {v.compatibleK8sVersions.join(", ") || "—"}
                      </td>
                      <td className="px-2 py-1 text-ink-muted">
                        {v.defaultVersion ? "yes" : ""}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        </Section>
      )}

      {(data.owner || data.publisher) && (
        <Section label="Source">
          <div className="text-[11.5px] text-ink-muted">
            {data.publisher ? `Published by ${data.publisher}` : null}
            {data.owner ? ` · owner: ${data.owner}` : null}
          </div>
        </Section>
      )}

      {data.arn && (
        <Section label="ARN">
          <div className="break-all font-mono text-[11px] text-ink-muted">
            {data.arn}
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
