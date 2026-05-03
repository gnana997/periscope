// HelmReleasesPage — /clusters/:cluster/helm
//
// Read-only list of Helm releases the user can see (issue #9 v1).
// Cluster-wide; the backend impersonates the user, so visibility
// matches the user's K8s RBAC on the storage Secrets/ConfigMaps.

import { useMemo } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { useHelmReleases } from "../hooks/useHelm";
import type { HelmReleaseSummary } from "../lib/types";
import { ageFrom, nameMatches } from "../lib/format";
import { PageHeader } from "../components/page/PageHeader";
import { FilterStrip } from "../components/page/FilterStrip";
import { DataTable, type Column } from "../components/table/DataTable";
import {
  EmptyState,
  ErrorState,
  ForbiddenState,
  LoadingState,
} from "../components/table/states";
import { isForbidden } from "../components/table/isForbidden";
import { cn } from "../lib/cn";

export function HelmReleasesPage({ cluster }: { cluster: string }) {
  const navigate = useNavigate();
  const [params, setParams] = useSearchParams();
  const search = params.get("q") ?? "";
  const statusFilter = params.get("status");

  const setParam = (key: string, value: string | null) => {
    const next = new URLSearchParams(params);
    if (value === null || value === "") next.delete(key);
    else next.set(key, value);
    setParams(next, { replace: true });
  };

  const query = useHelmReleases(cluster);
  const all = useMemo<HelmReleaseSummary[]>(
    () => query.data?.releases ?? [],
    [query.data],
  );

  const statuses = useMemo(() => {
    const s = new Set<string>();
    for (const r of all) if (r.status) s.add(r.status);
    return Array.from(s).sort();
  }, [all]);

  const filtered = useMemo(() => {
    let out = all;
    if (search) out = out.filter((r) => nameMatches(r.name, search));
    if (statusFilter) out = out.filter((r) => r.status === statusFilter);
    return out;
  }, [all, search, statusFilter]);

  const columns: Column<HelmReleaseSummary>[] = [
    {
      key: "name",
      header: "name",
      weight: 3,
      cellClassName: "font-mono text-ink",
      accessor: (r) => r.name,
    },
    {
      key: "namespace",
      header: "namespace",
      weight: 1.5,
      cellClassName: "font-mono text-ink-muted",
      accessor: (r) => r.namespace,
    },
    {
      key: "chart",
      header: "chart",
      weight: 2,
      cellClassName: "font-mono text-ink-muted",
      accessor: (r) =>
        r.chartName + (r.chartVersion ? `-${r.chartVersion}` : ""),
    },
    {
      key: "appVersion",
      header: "app version",
      weight: 1,
      cellClassName: "font-mono text-ink-muted",
      accessor: (r) => r.appVersion || "—",
    },
    {
      key: "status",
      header: "status",
      weight: 1,
      cellClassName: "font-mono",
      accessor: (r) => <StatusPill status={r.status} />,
    },
    {
      key: "revision",
      header: "rev",
      weight: 0.4,
      align: "right",
      cellClassName: "font-mono text-ink-muted tabular",
      accessor: (r) => `r${r.revision}`,
    },
    {
      key: "updated",
      header: "updated",
      weight: 0.7,
      align: "right",
      cellClassName: "font-mono text-ink-muted tabular",
      accessor: (r) => (r.updated ? ageFrom(r.updated) : "—"),
    },
  ];

  return (
    <div className="flex h-full min-h-0 flex-col">
      <PageHeader
        title="Helm"
        subtitle={
          query.isSuccess
            ? `${all.length} ${all.length === 1 ? "release" : "releases"}${
                query.data?.truncated ? " (truncated)" : ""
              }`
            : undefined
        }
      />
      <FilterStrip
        search={search}
        onSearch={(v) => setParam("q", v)}
        statusFilter={statusFilter}
        statusOptions={statuses}
        onStatusFilter={(s) => setParam("status", s)}
        resultCount={filtered.length}
        totalCount={all.length}
      />
      {query.data?.truncated && (
        <div className="border-b border-yellow/40 bg-yellow-soft px-6 py-2 font-mono text-[11.5px] text-yellow">
          showing first {all.length} releases — refine filters or contact your
          cluster admin if you need to see more.
        </div>
      )}
      <div className="flex min-h-0 flex-1 flex-col">
        {query.isLoading ? (
          <LoadingState resource="releases" />
        ) : query.isError ? (
          isForbidden(query.error) ? (
            <ForbiddenState
              resource="helm releases"
              message="needs cluster-wide list permission on the helm storage Secrets (or ConfigMaps)."
            />
          ) : (
            <ErrorState
              title="couldn't reach the cluster"
              message={(query.error as Error).message}
            />
          )
        ) : filtered.length === 0 ? (
          all.length === 0 ? (
            <HelmEmptyState />
          ) : (
            <EmptyState resource="helm releases" namespace={null} />
          )
        ) : (
          <DataTable<HelmReleaseSummary>
            columns={columns}
            rows={filtered}
            rowKey={(r) => `${r.namespace}/${r.name}`}
            onRowClick={(r) =>
              navigate(
                `/clusters/${encodeURIComponent(cluster)}/helm/${encodeURIComponent(
                  r.namespace,
                )}/${encodeURIComponent(r.name)}`,
              )
            }
          />
        )}
      </div>
    </div>
  );
}

// HelmEmptyState distinguishes "no Helm releases here" from "no
// permission" (the ForbiddenState branch above) and from the search
// empty state (default EmptyState). Hints at the non-default storage
// driver case so operators using HELM_DRIVER=configmap (auto-probed)
// or sql (not v1) get the right diagnostic.
function HelmEmptyState() {
  return (
    <div className="flex flex-1 flex-col items-center justify-center gap-3 px-6 py-16 text-center">
      <h3 className="text-[14px] font-medium text-ink">no helm releases</h3>
      <p className="max-w-md text-[12.5px] text-ink-muted">
        Either nothing is Helm-managed here, or the cluster uses a non-default
        Helm storage driver Periscope can't read (e.g. SQL).
      </p>
      <p className="max-w-md text-[11.5px] text-ink-faint">
        Visibility follows your K8s RBAC on the helm storage Secrets — releases
        in namespaces you can't list secrets in won't appear.
      </p>
    </div>
  );
}

// StatusPill renders the Helm release status with the same dual-channel
// encoding the rest of the app uses (color + glyph): green deployed,
// yellow pending, red failed, muted superseded.
function StatusPill({ status }: { status: string }) {
  const tone = statusTone(status);
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 rounded-sm px-1.5 py-0.5 font-mono text-[11px]",
        tone === "green" && "bg-green-soft text-green",
        tone === "yellow" && "bg-yellow-soft text-yellow",
        tone === "red" && "bg-red-soft text-red",
        tone === "muted" && "text-ink-faint",
      )}
    >
      <span
        aria-hidden
        className={cn(
          "block size-1.5 shrink-0 rounded-full",
          tone === "green" && "bg-green",
          tone === "yellow" && "bg-yellow",
          tone === "red" && "bg-red",
          tone === "muted" && "bg-ink-faint/50",
        )}
      />
      {status || "unknown"}
    </span>
  );
}

function statusTone(status: string): "green" | "yellow" | "red" | "muted" {
  switch (status) {
    case "deployed":
      return "green";
    case "pending-install":
    case "pending-upgrade":
    case "pending-rollback":
    case "uninstalling":
      return "yellow";
    case "failed":
      return "red";
    case "superseded":
    case "uninstalled":
      return "muted";
    default:
      return "muted";
  }
}
