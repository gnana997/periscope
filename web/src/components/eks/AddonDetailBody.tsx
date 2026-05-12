// AddonDetailBody — body of the installed-add-on detail surface.
// Rendered inside AddonDetailPane (right-edge DetailOverlay). Held in
// its own file so the next EKS surface that wants the same
// "Field / Section" atoms can import them from one place rather
// than re-deriving the layout.
//
// The version-history block deserves its own note:
//
//   - Rows are CLICKABLE — clicking one opens the upgrade dialog
//     with that version pre-selected, so the operator can pick a
//     specific target (including a downgrade or pinning to an
//     older "eksbuild.N" of the same minor) without a second
//     pass through the dialog's radio list.
//   - The "compatible with k8s [X ▼]" dropdown above the table
//     filters rows by which k8s version they support. Default is
//     the cluster's current k8s. Switching to the next minor lets
//     the operator answer the question raised by the row's
//     "blocks next k8s minor" warning chip — IS there a newer
//     addon version that would unblock the cluster upgrade?
//   - The currently-installed row never gets the click affordance
//     (upgrading "to current" is a noop and AWS rejects it).

import { useMemo, useState } from "react";
import { useAddon } from "../../hooks/useAddons";
import { compareAddonVersions } from "../../lib/addonInstall";
import { cn } from "../../lib/cn";
import type { AddonVersionEntry } from "../../lib/types";

const ANY_K8S = "__any__";

interface AddonDetailBodyProps {
  cluster: string;
  name: string;
  /** Cluster's current K8s version. Sets the initial dropdown
   *  selection so "compatible with k8s [today's version]" is the
   *  default view. */
  clusterK8sVersion?: string;
  /** Click handler for version-history rows. Pane wires this to
   *  open the upgrade dialog with the chosen version. When omitted
   *  rows render as plain (read-only) — useful for surfaces that
   *  surface this body without an upgrade affordance. */
  onUpgradeToVersion?: (version: string) => void;
}

export function AddonDetailBody({
  cluster,
  name,
  clusterK8sVersion,
  onUpgradeToVersion,
}: AddonDetailBodyProps) {
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

      {data.podIdentityAssociations && data.podIdentityAssociations.length > 0 && (
        <Section label="Pod Identity associations">
          <ul className="space-y-1">
            {data.podIdentityAssociations.map((arn) => (
              <li
                key={arn}
                className="break-all font-mono text-[11px] text-ink-muted"
              >
                {arn}
              </li>
            ))}
          </ul>
        </Section>
      )}

      {!data.serviceAccountRoleArn &&
        (!data.podIdentityAssociations ||
          data.podIdentityAssociations.length === 0) && (
          <Section label="IAM identity">
            <p className="text-[11.5px] italic text-ink-faint">
              No IAM identity attached — addon runs with the cluster's
              node-instance role.
            </p>
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
        <VersionHistory
          versions={data.availableVersions}
          installedVersion={data.version ?? null}
          clusterK8sVersion={clusterK8sVersion}
          onUpgradeToVersion={onUpgradeToVersion}
        />
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

// ── Version-history block ─────────────────────────────────────────

function VersionHistory({
  versions,
  installedVersion,
  clusterK8sVersion,
  onUpgradeToVersion,
}: {
  versions: AddonVersionEntry[];
  installedVersion: string | null;
  clusterK8sVersion: string | undefined;
  onUpgradeToVersion: ((version: string) => void) | undefined;
}) {
  // Build the dropdown options: every k8s version that appears in
  // any row's compatibleK8sVersions. Sorted descending so the
  // newest minor is at the top.
  const k8sOptions = useMemo(() => {
    const s = new Set<string>();
    for (const v of versions) {
      for (const k of v.compatibleK8sVersions) s.add(k);
    }
    return Array.from(s).sort(compareK8sDesc);
  }, [versions]);

  // Default selection: cluster k8s if it's in the option list,
  // otherwise the newest k8s the addon supports. ANY_K8S is the
  // explicit "show all" escape hatch.
  const [selectedK8s, setSelectedK8s] = useState<string>(() => {
    if (clusterK8sVersion && k8sOptions.includes(clusterK8sVersion)) {
      return clusterK8sVersion;
    }
    return k8sOptions[0] ?? ANY_K8S;
  });

  const filtered = useMemo(() => {
    if (selectedK8s === ANY_K8S) return versions;
    return versions.filter((v) =>
      v.compatibleK8sVersions.includes(selectedK8s),
    );
  }, [versions, selectedK8s]);

  const isClusterK8s = selectedK8s === clusterK8sVersion;

  return (
    <Section label="Version history">
      <div className="mb-2 flex flex-wrap items-center gap-2 text-[11.5px]">
        <label className="font-mono text-[10px] uppercase tracking-[0.08em] text-ink-faint">
          compatible with k8s
        </label>
        <select
          value={selectedK8s}
          onChange={(e) => setSelectedK8s(e.target.value)}
          className="rounded-sm border border-border bg-bg px-1.5 py-0.5 font-mono text-[11.5px]"
        >
          {k8sOptions.map((k) => (
            <option key={k} value={k}>
              {k}
              {k === clusterK8sVersion ? "  (current)" : ""}
            </option>
          ))}
          <option value={ANY_K8S}>any</option>
        </select>
        {!isClusterK8s && selectedK8s !== ANY_K8S && clusterK8sVersion && (
          <button
            type="button"
            onClick={() => setSelectedK8s(clusterK8sVersion)}
            className="font-mono text-[10.5px] uppercase tracking-[0.06em] text-accent hover:underline"
          >
            reset to {clusterK8sVersion}
          </button>
        )}
      </div>

      {filtered.length === 0 ? (
        <div className="rounded-sm border border-border bg-surface px-3 py-2.5 text-[11.5px] text-ink-muted">
          No add-on versions are compatible with k8s {selectedK8s}.
          {clusterK8sVersion && selectedK8s !== clusterK8sVersion && (
            <>
              {" "}
              This is what the &quot;blocks next k8s minor&quot; warning
              flags — track AWS release notes for a future eksbuild
              that adds {selectedK8s} compatibility.
            </>
          )}
        </div>
      ) : (
        <div className="overflow-hidden rounded-sm border border-border">
          <table className="w-full text-[11.5px]">
            <thead className="border-b border-border bg-surface text-[10px] uppercase tracking-[0.06em] text-ink-faint">
              <tr>
                <th className="px-2 py-1 text-left">Version</th>
                <th className="px-2 py-1 text-left">Compatible k8s</th>
                <th className="px-2 py-1 text-left">Default</th>
                <th className="px-2 py-1 text-right" />
              </tr>
            </thead>
            <tbody>
              {filtered.map((v) => (
                <VersionRow
                  key={v.version}
                  version={v}
                  installedVersion={installedVersion}
                  onUpgradeToVersion={onUpgradeToVersion}
                />
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Section>
  );
}

function VersionRow({
  version,
  installedVersion,
  onUpgradeToVersion,
}: {
  version: AddonVersionEntry;
  installedVersion: string | null;
  onUpgradeToVersion: ((version: string) => void) | undefined;
}) {
  const isInstalled = version.version === installedVersion;
  // Clickable when (a) the operator has an upgrade affordance,
  // (b) this row isn't the current install (AWS rejects upgrade
  // to current), AND (c) this row's version is strictly newer
  // than installed. We deliberately don't surface a one-click
  // affordance for downgrades — that path stays available via the
  // kebab → Upgrade modal, which forces the operator through a
  // dialog where the deliberate "older eksbuild" choice is visible.
  // Downgrades carry CRD/IAM/schema risk that one-click obscures.
  const isNewer =
    !!installedVersion &&
    compareAddonVersions(version.version, installedVersion) > 0;
  const clickable = Boolean(onUpgradeToVersion) && !isInstalled && isNewer;
  const cta = clickable ? (
    <button
      type="button"
      onClick={() => onUpgradeToVersion?.(version.version)}
      className="rounded-sm border border-accent bg-accent-soft px-1.5 py-0.5 font-mono text-[10px] uppercase tracking-[0.06em] text-accent hover:bg-accent/10"
    >
      upgrade to
    </button>
  ) : null;

  const onRowClick = clickable
    ? () => onUpgradeToVersion?.(version.version)
    : undefined;

  return (
    <tr
      className={cn(
        "border-b border-border last:border-0",
        isInstalled && "bg-accent-soft/30",
        clickable && "cursor-pointer transition-colors hover:bg-surface-2",
      )}
      onClick={onRowClick}
    >
      <td className="px-2 py-1 font-mono">
        {version.version}
        {isInstalled && (
          <span className="ml-2 text-[10px] uppercase tracking-[0.06em] text-accent">
            installed
          </span>
        )}
      </td>
      <td className="px-2 py-1 font-mono text-ink-muted">
        {version.compatibleK8sVersions.join(", ") || "—"}
      </td>
      <td className="px-2 py-1 text-ink-muted">
        {version.defaultVersion ? "yes" : ""}
      </td>
      <td className="px-2 py-1 text-right">{cta}</td>
    </tr>
  );
}

// Sort k8s minor versions in descending order. Strings like "1.31"
// vs "1.7" need numeric-aware compare.
function compareK8sDesc(a: string, b: string): number {
  const pa = a.split(".").map(Number);
  const pb = b.split(".").map(Number);
  for (let i = 0; i < Math.max(pa.length, pb.length); i++) {
    const da = pa[i] ?? 0;
    const db = pb[i] ?? 0;
    if (da !== db) return db - da;
  }
  return 0;
}

// ── Layout primitives ──────────────────────────────────────────────
//
// Exported so siblings (future EKS detail surfaces) reuse the same
// label-above-value rhythm without re-deriving it.

export function Section({
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

export function FieldGrid({ children }: { children: React.ReactNode }) {
  return (
    <div className="grid grid-cols-2 gap-x-6 gap-y-2 lg:grid-cols-4">
      {children}
    </div>
  );
}

export function Field({
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
