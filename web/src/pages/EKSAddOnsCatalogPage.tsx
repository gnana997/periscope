// EKSAddOnsCatalogPage — /clusters/{c}/eks/addons/catalog (issue
// #119, PR-1).
//
// Read-only browse of every AWS-published add-on available on this
// cluster's K8s version. Pairs with AddOnsPage (#117): catalog is
// "what could I install", AddOnsPage is "what's installed and is it
// stale". Filter chips narrow by ownership (AWS vs third-party) and
// by type (networking / storage / observability / security).
//
// Install / Upgrade / Delete actions are intentionally absent in
// PR-1. Available rows show a disabled "+ Install" stub that PR-2
// wires; installed rows link to AddOnsPage for the existing detail
// surface (and pick up Upgrade/Delete kebabs in PR-3).

import { useMemo, useState } from "react";
import { useAddonCatalog } from "../hooks/useAddonCatalog";
import { useAddons } from "../hooks/useAddons";
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

  return (
    <div className="flex h-full min-h-0 flex-col overflow-y-auto px-6 py-5">
      <header className="mb-4">
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

      <div className="mb-3 flex flex-wrap items-center gap-2">
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

      {filtered.length === 0 ? (
        <p className="text-[13px] text-ink-faint">
          {totalCount === 0
            ? "AWS returned no add-on catalog for this k8s version."
            : "No add-ons match the current filters."}
        </p>
      ) : (
        <div className="overflow-hidden rounded-md border border-border bg-surface">
          <table className="w-full text-[12.5px]">
            <thead className="border-b border-border bg-surface-2/40 text-[10px] uppercase tracking-[0.08em] text-ink-faint">
              <tr>
                <th className="px-3 py-2 text-left">Name</th>
                <th className="px-3 py-2 text-left">Owner</th>
                <th className="px-3 py-2 text-left">Type</th>
                <th className="px-3 py-2 text-left">Latest</th>
                <th className="px-3 py-2 text-left">Compatible k8s</th>
                <th className="px-3 py-2 text-left">Status</th>
                <th className="px-3 py-2 text-right">Action</th>
              </tr>
            </thead>
            <tbody>
              {filtered.map((row) => (
                <CatalogRow
                  key={row.name}
                  cluster={cluster}
                  row={row}
                  k8sVersion={catalog.data?.kubernetesVersion}
                />
              ))}
            </tbody>
          </table>
        </div>
      )}
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
  cluster,
  row,
  k8sVersion,
}: {
  cluster: string;
  row: CatalogAddon;
  k8sVersion?: string;
}) {
  const latest = pickLatestForK8s(row.compatibleVersions, k8sVersion);
  const compatRange = compatRangeOf(latest?.kubernetesVersions ?? []);

  return (
    <tr className="border-b border-border last:border-0 transition-colors hover:bg-surface-2">
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
      <td className="px-3 py-2 font-mono text-[11.5px] text-ink-muted">
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
      <td className="px-3 py-2 text-right">
        {row.installed ? (
          <a
            href={`/clusters/${encodeURIComponent(cluster)}/addons`}
            className="font-mono text-[11px] text-accent hover:underline"
          >
            manage →
          </a>
        ) : (
          // PR-2 wires this. Disabled stub keeps the layout final.
          <button
            type="button"
            disabled
            className="cursor-not-allowed rounded-sm border border-border px-2 py-1 text-[11px] text-ink-faint"
            title="Install action ships in a follow-up PR (#119, PR-2)"
          >
            + Install
          </button>
        )}
      </td>
    </tr>
  );
}

