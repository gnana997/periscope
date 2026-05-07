// EKSAddOnsCatalogPage — /clusters/{c}/eks/addons/catalog (issue
// #119, PR-1 + PR-2).
//
// Browse of every AWS-published add-on available on this cluster's
// K8s version. Pairs with AddOnsPage (#117): catalog is "what could
// I install", AddOnsPage is "what's installed and is it stale".
// Filter chips narrow by ownership (AWS vs third-party) and by
// type (networking / storage / observability / security).
//
// Layout: SplitPane with the catalog table on the left and
// AddonDetailPane on the right. Both installed and available rows
// open the pane on click; the row's primary action (Install / kebab)
// stops propagation so the operator can still skip to the modal
// without going through the pane first.

import { useEffect, useMemo, useState } from "react";
import { AddonDetailPane } from "../components/eks/AddonDetailPane";
import { DeleteAddOnModal } from "../components/eks/DeleteAddOnModal";
import { InstallAddOnDialog } from "../components/eks/InstallAddOnDialog";
import { UpgradeAddOnDialog } from "../components/eks/UpgradeAddOnDialog";
import { KebabMenu } from "../components/ui/KebabMenu";
import { SplitPane } from "../components/page/SplitPane";
import { useAddonCatalog } from "../hooks/useAddonCatalog";
import { useAddon, useAddons } from "../hooks/useAddons";
import {
  compatRangeOf,
  isAWSOwned,
  mergeInstalled,
  pickLatestForK8s,
} from "../lib/addonCatalog";
import { isAWSForbidden, isAWSThrottled, isBackendNotEKS } from "../lib/api";
import { cn } from "../lib/cn";
import type { CatalogAddon } from "../lib/types";

type OwnerFilter = "all" | "aws" | "third-party";

export function EKSAddOnsCatalogPage({ cluster }: { cluster: string }) {
  const catalog = useAddonCatalog(cluster);
  // Layered fallback: when the backend's per-cluster addons cache
  // was cold at catalog request time, `installed` is null on every
  // row. We use the SPA's existing useAddons() data to fill in
  // client-side. Both queries already run in parallel.
  const addons = useAddons(cluster);

  const [ownerFilter, setOwnerFilter] = useState<OwnerFilter>("all");
  const [typeFilter, setTypeFilter] = useState<string>("all");
  // installTarget is the catalog row whose "+ Install" button was
  // clicked. null when the dialog is closed.
  const [installTarget, setInstallTarget] = useState<CatalogAddon | null>(null);
  // Upgrade / delete targets — invoked from the kebab on installed
  // rows. Same dialogs as AddOnsPage so the action UX is identical
  // across both surfaces.
  const [upgradeTarget, setUpgradeTarget] = useState<CatalogAddon | null>(null);
  // Set when the operator clicks a row in the detail-pane's
  // version-history table; threads the chosen version to
  // UpgradeAddOnDialog as the initial selection.
  const [upgradeInitialVersion, setUpgradeInitialVersion] = useState<
    string | null
  >(null);
  const [deleteTarget, setDeleteTarget] = useState<string | null>(null);
  // Selected row for the detail pane. Stores the addon name; the
  // pane resolves it back to a CatalogAddon at render time so we
  // don't keep a stale snapshot if the catalog refetches.
  const [selectedName, setSelectedName] = useState<string | null>(null);
  const upgradeDetail = useAddon(cluster, upgradeTarget?.name ?? "");

  // Clear the pane when the selected row vanishes from the catalog —
  // happens when a delete settles (row removed from the layered
  // installed mirror), or when AWS catalog refetches return a
  // different shape. Without this the pane sticks open referencing
  // a name no longer in `rows`, dialog handlers close over a stale
  // selectedRow snapshot, and the pane drifts out of sync with the
  // table. Hook MUST live above the early returns below; guards on
  // catalog.data?.available defensively for the pre-load state.
  useEffect(() => {
    const currentNames = catalog.data?.available ?? [];
    if (
      selectedName != null &&
      !currentNames.find((r) => r.name === selectedName)
    ) {
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setSelectedName(null);
    }
  }, [catalog.data?.available, selectedName]);

  const rows = useMemo(() => {
    if (!catalog.data) return [] as CatalogAddon[];
    return mergeInstalled(catalog.data.available, addons.data);
  }, [catalog.data, addons.data]);

  const types = useMemo(() => {
    const s = new Set<string>();
    for (const r of rows) {
      if (r.type) s.add(r.type);
    }
    return Array.from(s).sort();
  }, [rows]);

  const filtered = useMemo(() => {
    return rows.filter((r) => {
      if (ownerFilter === "aws" && !isAWSOwned(r.owner)) return false;
      if (ownerFilter === "third-party" && isAWSOwned(r.owner)) return false;
      if (typeFilter !== "all" && r.type !== typeFilter) return false;
      return true;
    });
  }, [rows, ownerFilter, typeFilter]);

  if (catalog.isError && isBackendNotEKS(catalog.error)) {
    return (
      <div className="px-6 py-8">
        <h1 className="mb-2 text-[16px] font-medium">EKS add-on catalog</h1>
        <p className="text-[13px] text-ink-faint">
          Add-on catalog is an EKS feature; this cluster is not backed by
          EKS, so no catalog data is available.
        </p>
      </div>
    );
  }

  if (catalog.isError && isAWSForbidden(catalog.error)) {
    return (
      <div className="px-6 py-8">
        <h1 className="mb-2 text-[16px] font-medium">EKS add-on catalog</h1>
        <p className="text-[13px] text-ink-faint">
          Periscope's AWS role does not have permission to read the
          add-on catalog. Required IAM action:{" "}
          <code className="font-mono text-[12px]">
            eks:DescribeAddonVersions
          </code>
          . See{" "}
          <code className="font-mono text-[12px]">docs/setup/deploy.md</code>.
        </p>
      </div>
    );
  }

  if (catalog.isError && isAWSThrottled(catalog.error)) {
    return (
      <div className="px-6 py-8">
        <h1 className="mb-2 text-[16px] font-medium">EKS add-on catalog</h1>
        <p className="text-[13px] text-ink-faint">
          AWS rate-limited this request. Refresh in a moment; the cache
          will absorb subsequent calls.
        </p>
      </div>
    );
  }

  if (catalog.isLoading) {
    return (
      <div className="flex h-full items-center justify-center">
        <span className="block size-4 animate-spin rounded-full border-[1.5px] border-border-strong border-t-accent" />
      </div>
    );
  }

  if (catalog.isError) {
    return (
      <div className="px-6 py-8">
        <h1 className="mb-2 text-[16px] font-medium">EKS add-on catalog</h1>
        <p className="text-[13px] text-red">
          Failed to load catalog:{" "}
          {(catalog.error as Error)?.message ?? "unknown error"}.
        </p>
      </div>
    );
  }

  if (!catalog.data) return null;

  const totalCount = rows.length;
  const installedCount = rows.filter((r) => r.installed).length;

  const selectedRow = selectedName
    ? rows.find((r) => r.name === selectedName) ?? null
    : null;
  const selectedTransient =
    selectedRow?.installed?.status === "CREATING" ||
    selectedRow?.installed?.status === "UPDATING" ||
    selectedRow?.installed?.status === "DELETING";

  const detail = selectedRow ? (
    <AddonDetailPane
      cluster={cluster}
      selection={
        selectedRow.installed
          ? {
              kind: "installed",
              name: selectedRow.name,
              catalog: selectedRow,
            }
          : { kind: "available", catalog: selectedRow }
      }
      kubernetesVersion={catalog.data?.kubernetesVersion}
      onClose={() => setSelectedName(null)}
      onInstall={() => setInstallTarget(selectedRow)}
      onUpgrade={() => {
        setUpgradeInitialVersion(null);
        setUpgradeTarget(selectedRow);
      }}
      onUpgradeToVersion={(v) => {
        setUpgradeInitialVersion(v);
        setUpgradeTarget(selectedRow);
      }}
      onDelete={() => setDeleteTarget(selectedRow.name)}
      actionsDisabled={selectedTransient}
    />
  ) : null;

  return (
    <div className="flex h-full min-h-0 flex-col">
      <header className="shrink-0 px-6 pt-5 pb-3">
        <h1 className="text-[16px] font-medium">
          EKS add-on catalog
          {catalog.data.kubernetesVersion && (
            <span className="ml-2 font-mono text-[12px] text-ink-faint">
              · k8s {catalog.data.kubernetesVersion}
            </span>
          )}
        </h1>
        <p className="mt-0.5 text-[12px] text-ink-faint">
          {totalCount} available · {installedCount} installed
        </p>
      </header>

      <div className="shrink-0 px-6 pb-3">
        <div className="flex flex-wrap items-center gap-2">
          <FilterChip
            active={ownerFilter === "all"}
            onClick={() => setOwnerFilter("all")}
            label="All"
          />
          <FilterChip
            active={ownerFilter === "aws"}
            onClick={() => setOwnerFilter("aws")}
            label="AWS"
          />
          <FilterChip
            active={ownerFilter === "third-party"}
            onClick={() => setOwnerFilter("third-party")}
            label="Third-party"
          />
          <span className="mx-2 text-ink-faint">·</span>
          <FilterChip
            active={typeFilter === "all"}
            onClick={() => setTypeFilter("all")}
            label="Any type"
          />
          {types.map((t) => (
            <FilterChip
              key={t}
              active={typeFilter === t}
              onClick={() => setTypeFilter(t)}
              label={t}
            />
          ))}
        </div>
      </div>

      <SplitPane
        storageKey="periscope.detailWidth.v4"
        left={
          filtered.length === 0 ? (
            <p className="px-6 py-4 text-[13px] text-ink-faint">
              {totalCount === 0
                ? "AWS returned no add-on catalog for this k8s version."
                : "No add-ons match the current filters."}
            </p>
          ) : (
            <div className="flex h-full min-h-0 flex-col px-6 pb-5">
              <div className="flex min-h-0 flex-1 flex-col overflow-hidden rounded-md border border-border bg-surface">
                <div className="min-h-0 flex-1 overflow-y-auto">
                  <table className="w-full text-[12.5px]">
                    <thead className="sticky top-0 z-[1] border-b border-border bg-surface-2/90 text-[10px] uppercase tracking-[0.08em] text-ink-faint backdrop-blur-sm">
                      <tr>
                        <th className="px-3 py-2 text-left">Name</th>
                        <th className="px-3 py-2 text-left">Owner</th>
                        <th className="px-3 py-2 text-left">Type</th>
                        <th className="px-3 py-2 text-left">Latest</th>
                        <th className="hidden px-3 py-2 text-left lg:table-cell">
                          Compatible k8s
                        </th>
                        <th className="px-3 py-2 text-left">Status</th>
                        <th className="px-3 py-2 text-right">Action</th>
                      </tr>
                    </thead>
                    <tbody>
                      {filtered.map((row) => (
                        <CatalogRow
                          key={row.name}
                          row={row}
                          k8sVersion={catalog.data?.kubernetesVersion}
                          selected={selectedName === row.name}
                          onSelect={() => setSelectedName(row.name)}
                          onInstall={() => setInstallTarget(row)}
                          onUpgrade={() => setUpgradeTarget(row)}
                          onDelete={() => setDeleteTarget(row.name)}
                        />
                      ))}
                    </tbody>
                  </table>
                </div>
              </div>
            </div>
          )
        }
        right={detail}
      />

      <InstallAddOnDialog
        open={installTarget !== null}
        onClose={() => setInstallTarget(null)}
        cluster={cluster}
        addon={installTarget}
        kubernetesVersion={catalog.data?.kubernetesVersion}
      />
      <UpgradeAddOnDialog
        open={upgradeTarget !== null}
        onClose={() => {
          setUpgradeTarget(null);
          setUpgradeInitialVersion(null);
        }}
        cluster={cluster}
        catalogAddon={upgradeTarget}
        detail={upgradeDetail.data ?? null}
        kubernetesVersion={catalog.data?.kubernetesVersion}
        initialVersion={upgradeInitialVersion ?? undefined}
      />
      <DeleteAddOnModal
        open={deleteTarget !== null}
        onClose={() => setDeleteTarget(null)}
        cluster={cluster}
        addonName={deleteTarget}
      />
    </div>
  );
}

function FilterChip({
  active,
  onClick,
  label,
}: {
  active: boolean;
  onClick: () => void;
  label: string;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "rounded-sm border px-2.5 py-1 text-[11px] font-mono uppercase tracking-[0.06em] transition-colors",
        active
          ? "border-accent bg-accent-soft text-accent"
          : "border-border text-ink hover:bg-surface-2",
      )}
    >
      {label}
    </button>
  );
}

function CatalogRow({
  row,
  k8sVersion,
  selected,
  onSelect,
  onInstall,
  onUpgrade,
  onDelete,
}: {
  row: CatalogAddon;
  k8sVersion?: string;
  selected: boolean;
  onSelect: () => void;
  onInstall: () => void;
  onUpgrade: () => void;
  onDelete: () => void;
}) {
  const latest = pickLatestForK8s(row.compatibleVersions, k8sVersion);
  const compatRange = compatRangeOf(latest?.kubernetesVersions ?? []);
  const transient =
    row.installed?.status === "CREATING" ||
    row.installed?.status === "UPDATING" ||
    row.installed?.status === "DELETING";

  return (
    <tr
      className={cn(
        "cursor-pointer border-b border-border last:border-0 transition-colors hover:bg-surface-2",
        selected && "bg-accent-soft/40",
      )}
      onClick={onSelect}
    >
      <td className="px-3 py-2 font-mono">
        {row.name}
        {row.marketplaceProduct && (
          <div className="text-[10.5px] text-yellow">
            marketplace · accept EULA outside Periscope
          </div>
        )}
      </td>
      <td className="px-3 py-2 text-ink-muted">
        {row.publisher ?? row.owner ?? "—"}
      </td>
      <td className="px-3 py-2 text-ink-muted">{row.type ?? "—"}</td>
      <td className="px-3 py-2 font-mono text-ink-muted">
        {latest?.version ?? "—"}
      </td>
      <td className="hidden px-3 py-2 font-mono text-[11.5px] text-ink-muted lg:table-cell">
        {compatRange ?? "—"}
      </td>
      <td className="px-3 py-2">
        {row.installed ? (
          <span className="font-mono text-[11px] uppercase tracking-[0.06em] text-green">
            installed{" "}
            <span className="text-ink-muted">{row.installed.version}</span>
          </span>
        ) : (
          <span className="font-mono text-[11px] uppercase tracking-[0.06em] text-ink-faint">
            available
          </span>
        )}
      </td>
      <td
        className="px-3 py-2 text-right"
        onClick={(e) => e.stopPropagation()}
      >
        {row.installed ? (
          <KebabMenu
            label={`actions for ${row.name}`}
            items={[
              {
                label: "Upgrade…",
                onSelect: onUpgrade,
                disabled: transient,
              },
              {
                label: "Delete…",
                onSelect: onDelete,
                variant: "danger",
                disabled: transient,
              },
            ]}
          />
        ) : (
          <button
            type="button"
            onClick={onInstall}
            className="rounded-sm border border-accent bg-accent-soft px-2 py-1 font-mono text-[11px] text-accent hover:bg-accent/10"
          >
            + Install
          </button>
        )}
      </td>
    </tr>
  );
}
