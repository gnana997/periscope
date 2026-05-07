import { useMemo, useState } from "react";
import { AddonDetailBody } from "../components/eks/AddonDetailBody";
import { DeleteAddOnModal } from "../components/eks/DeleteAddOnModal";
import { UpgradeAddOnDialog } from "../components/eks/UpgradeAddOnDialog";
import { KebabMenu } from "../components/ui/KebabMenu";
import { useAddon, useAddons } from "../hooks/useAddons";
import { useAddonCatalog } from "../hooks/useAddonCatalog";
import { isAWSForbidden, isAWSThrottled, isBackendNotEKS } from "../lib/api";
import { cn } from "../lib/cn";
import type { AddonHealthGlyph, AddonSummary, CatalogAddon } from "../lib/types";

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
  // Catalog query runs in parallel — needed by the upgrade dialog
  // (compatible-versions list) when an operator clicks Upgrade. It's
  // already cached server-side at 6h, so this is cheap.
  const catalog = useAddonCatalog(cluster);
  const catalogByName = useMemo(() => {
    const m = new Map<string, CatalogAddon>();
    if (catalog.data?.available) {
      for (const a of catalog.data.available) m.set(a.name, a);
    }
    return m;
  }, [catalog.data]);

  // Action targets: which addon's kebab the operator just clicked.
  // null = no dialog open. Upgrade needs (catalogAddon + detail);
  // delete needs only the name.
  const [upgradeTarget, setUpgradeTarget] = useState<string | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<string | null>(null);
  // Detail blob for the upgrade dialog. Lazy fetch — only fires
  // when an upgrade target is set, since useAddon respects the
  // enabled flag inside the hook (Boolean(cluster && name)).
  const upgradeDetail = useAddon(cluster, upgradeTarget ?? "");

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
                <th className="px-3 py-2 text-right">Actions</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((row) => (
                <AddonRow
                  key={row.name}
                  cluster={cluster}
                  addon={row}
                  onUpgrade={() => setUpgradeTarget(row.name)}
                  onDelete={() => setDeleteTarget(row.name)}
                />
              ))}
            </tbody>
          </table>
        </div>
      )}

      <UpgradeAddOnDialog
        open={upgradeTarget !== null}
        onClose={() => setUpgradeTarget(null)}
        cluster={cluster}
        catalogAddon={
          upgradeTarget ? catalogByName.get(upgradeTarget) ?? null : null
        }
        detail={upgradeDetail.data ?? null}
        kubernetesVersion={
          catalog.data?.kubernetesVersion ?? data?.clusterKubernetesVersion
        }
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

function AddonRow({
  cluster,
  addon,
  onUpgrade,
  onDelete,
}: {
  cluster: string;
  addon: AddonSummary;
  onUpgrade: () => void;
  onDelete: () => void;
}) {
  const [open, setOpen] = useState(false);
  // Disable Upgrade / Delete kebab items while AWS is mid-transition —
  // sending another mutation while CREATING/UPDATING/DELETING is in
  // flight produces opaque AWS errors. The status flips back via
  // the polling refetch; the kebab re-enables.
  const transient =
    addon.status === "CREATING" ||
    addon.status === "UPDATING" ||
    addon.status === "DELETING";
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
        <td className="px-3 py-2 text-right">
          <KebabMenu
            label={`actions for ${addon.name}`}
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
        </td>
      </tr>
      {open && (
        <tr>
          <td
            colSpan={7}
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

