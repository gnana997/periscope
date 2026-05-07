// AddonDetailPane — right-edge detail pane for the EKS add-ons
// surface. Lives inside SplitPane on both AddOnsPage and the catalog
// page; replaces the old inline-row-expand which couldn't scroll
// independently of the page (#117 follow-up).
//
// Two modes:
//   - "installed"  — fetched detail (version history, IAM SA role,
//                    health issues, ARN). Footer: Upgrade / Delete.
//   - "available"  — catalog metadata only (publisher, type, compat
//                    range, version list, marketplace warning). No
//                    /addons/{name} call (would 404). Footer: Install.

import { compatRangeOf, pickLatestForK8s } from "../../lib/addonCatalog";
import { cn } from "../../lib/cn";
import type { CatalogAddon } from "../../lib/types";
import { AddonDetailBody } from "./AddonDetailBody";

export type AddonPaneSelection =
  | { kind: "installed"; name: string; catalog?: CatalogAddon }
  | { kind: "available"; catalog: CatalogAddon };

interface AddonDetailPaneProps {
  cluster: string;
  selection: AddonPaneSelection;
  /** Cluster's K8s version — drives compat range display in
   *  available mode. */
  kubernetesVersion?: string;
  onClose: () => void;
  /** Action handlers: each is wired by the page. The pane just
   *  triggers; modal management lives in the page. */
  onInstall?: () => void;
  onUpgrade?: () => void;
  onDelete?: () => void;
  /** Variant of onUpgrade triggered from a clickable version-history
   *  row — opens the upgrade dialog with the chosen version
   *  pre-selected. Same modal, just bypasses the AWS-default
   *  selection. Wired only when onUpgrade is also wired. */
  onUpgradeToVersion?: (version: string) => void;
  /** True while AWS is mid-transition (CREATING/UPDATING/DELETING).
   *  Disables Upgrade/Delete; matches the kebab-disabled rule. */
  actionsDisabled?: boolean;
}

export function AddonDetailPane({
  cluster,
  selection,
  kubernetesVersion,
  onClose,
  onInstall,
  onUpgrade,
  onDelete,
  onUpgradeToVersion,
  actionsDisabled,
}: AddonDetailPaneProps) {
  const name =
    selection.kind === "installed" ? selection.name : selection.catalog.name;
  const catalog =
    selection.kind === "installed" ? selection.catalog : selection.catalog;
  const subtitle = subtitleFor(catalog);

  return (
    // min-w-0 mirrors DetailPane — the SplitPane right pane is
    // fixed-width and min-w-0; nested flex containers must opt-in
    // for `truncate` and overflow-hidden to actually clip.
    <div className="flex h-full min-h-0 min-w-0 flex-col bg-surface">
      <header className="flex shrink-0 items-start justify-between gap-3 border-b border-border px-4 py-3">
        <div className="min-w-0">
          <div className="truncate font-mono text-[13.5px] text-ink">
            {name}
          </div>
          {subtitle && (
            <div className="mt-0.5 truncate text-[11.5px] text-ink-faint">
              {subtitle}
            </div>
          )}
          {catalog?.marketplaceProduct && (
            <div className="mt-1 text-[11px] text-yellow">
              marketplace · accept EULA outside Periscope
            </div>
          )}
        </div>
        <button
          type="button"
          onClick={onClose}
          aria-label="Close detail pane"
          className="shrink-0 rounded-sm border border-border bg-bg px-2 py-0.5 font-mono text-[11px] text-ink-muted hover:bg-surface-2"
        >
          ✕
        </button>
      </header>

      <div className="flex-1 overflow-y-auto px-4 py-3">
        {selection.kind === "installed" ? (
          <AddonDetailBody
            cluster={cluster}
            name={selection.name}
            clusterK8sVersion={kubernetesVersion}
            onUpgradeToVersion={
              actionsDisabled ? undefined : onUpgradeToVersion
            }
          />
        ) : (
          <AvailableBody
            catalog={selection.catalog}
            kubernetesVersion={kubernetesVersion}
          />
        )}
      </div>

      <footer className="flex shrink-0 items-center justify-end gap-2 border-t border-border bg-surface-2/30 px-4 py-2.5">
        {selection.kind === "installed" ? (
          <>
            {onDelete && (
              <ActionButton
                tone="danger"
                disabled={actionsDisabled}
                onClick={onDelete}
              >
                Delete…
              </ActionButton>
            )}
            {onUpgrade && (
              <ActionButton
                tone="primary"
                disabled={actionsDisabled}
                onClick={onUpgrade}
              >
                Upgrade…
              </ActionButton>
            )}
          </>
        ) : (
          onInstall && (
            <ActionButton tone="primary" onClick={onInstall}>
              + Install
            </ActionButton>
          )
        )}
      </footer>
    </div>
  );
}

function subtitleFor(catalog: CatalogAddon | undefined): string {
  if (!catalog) return "";
  const parts: string[] = [];
  if (catalog.publisher) parts.push(`Published by ${catalog.publisher}`);
  if (catalog.owner) parts.push(`owner: ${catalog.owner}`);
  if (catalog.type) parts.push(catalog.type);
  return parts.join(" · ");
}

function ActionButton({
  tone,
  disabled,
  onClick,
  children,
}: {
  tone: "primary" | "danger";
  disabled?: boolean;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      className={cn(
        "rounded-sm border px-3 py-1 font-mono text-[11.5px] transition-colors disabled:opacity-50",
        tone === "primary" &&
          "border-accent bg-accent-soft text-accent hover:bg-accent/10",
        tone === "danger" &&
          "border-red/40 bg-red/5 text-red hover:bg-red/10",
      )}
    >
      {children}
    </button>
  );
}

// ── Available-mode body ──────────────────────────────────────────
//
// Renders catalog metadata + a versions table. We deliberately don't
// call useAddon() here — the addon isn't installed, the endpoint
// would 404, and the SAR cost is wasted.

function AvailableBody({
  catalog,
  kubernetesVersion,
}: {
  catalog: CatalogAddon;
  kubernetesVersion?: string;
}) {
  const latest = pickLatestForK8s(catalog.compatibleVersions, kubernetesVersion);
  const compatRange = compatRangeOf(latest?.kubernetesVersions ?? []);

  return (
    <div className="space-y-3">
      <PaneFieldGrid>
        <PaneField label="Latest version">{latest?.version ?? "—"}</PaneField>
        <PaneField label="Compatible k8s">{compatRange ?? "—"}</PaneField>
        <PaneField label="Owner">{catalog.owner ?? "—"}</PaneField>
        <PaneField label="Type">{catalog.type ?? "—"}</PaneField>
      </PaneFieldGrid>

      {catalog.compatibleVersions.length > 0 && (
        <PaneSection label="Available versions">
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
                {catalog.compatibleVersions.map((v) => (
                  <tr
                    key={v.version}
                    className="border-b border-border last:border-0"
                  >
                    <td className="px-2 py-1 font-mono">{v.version}</td>
                    <td className="px-2 py-1 font-mono text-ink-muted">
                      {v.kubernetesVersions.join(", ") || "—"}
                    </td>
                    <td className="px-2 py-1 text-ink-muted">
                      {v.default ? "yes" : ""}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </PaneSection>
      )}
    </div>
  );
}

function PaneSection({
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

function PaneFieldGrid({ children }: { children: React.ReactNode }) {
  return <div className="grid grid-cols-2 gap-x-6 gap-y-2">{children}</div>;
}

function PaneField({
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
      <div className="mt-0.5 font-mono text-[12px]">{children}</div>
    </div>
  );
}
