// DriftDiffOverlay — modal that shows the user the difference between
// their pristine baseline (the YAML they opened the editor against)
// and the server's *current* state. Triggered from the [show diff]
// button on DriftBanner.
//
// Reuses InlineDiff (same DiffEditor used for the user's own
// pristine-vs-buffer view). Fetches the fresh server YAML on open
// via a one-shot useQuery with its own key so we don't disturb the
// editor's cached pristine flow.

import { useQuery } from "@tanstack/react-query";
import { api, type YamlKind, type ClusterScopedKind } from "../../../lib/api";
import { cn } from "../../../lib/cn";
import { queryKeys } from "../../../lib/queryKeys";
import { stripForEdit } from "../../../lib/stripForEdit";
import { DetailLoading, DetailError } from "../states";
import { Modal } from "../../ui/Modal";
import { InlineDiff } from "./InlineDiff";

const CLUSTER_SCOPED_KINDS = new Set<ClusterScopedKind>([
  "namespaces",
  "pvs",
  "storageclasses",
  "clusterroles",
  "clusterrolebindings",
  "ingressclasses",
  "priorityclasses",
  "runtimeclasses",
]);

interface DriftDiffOverlayProps {
  cluster: string;
  yamlKind: YamlKind;
  namespace: string | undefined;
  name: string;
  pristineYaml: string;
  onClose(): void;
  onReload(): void;
}

export function DriftDiffOverlay({
  cluster,
  yamlKind,
  namespace,
  name,
  pristineYaml,
  onClose,
  onReload,
}: DriftDiffOverlayProps) {
  // Independent fetch — separate cache key so we don't compete with
  // the editor's pristine-flowing yamlQuery. staleTime: 0 + a fresh
  // mount on every open guarantees we see latest server state.
  const freshQuery = useQuery<string>({
    queryKey: queryKeys.cluster(cluster).kind(yamlKind).yamlDrift(namespace ?? "", name),
    queryFn: ({ signal }) =>
      CLUSTER_SCOPED_KINDS.has(yamlKind as ClusterScopedKind)
        ? api.clusterScopedYaml(cluster, yamlKind as ClusterScopedKind, name, signal)
        : api.yaml(cluster, yamlKind as Exclude<YamlKind, ClusterScopedKind>, namespace ?? "", name, signal),
    staleTime: 0,
    refetchOnMount: "always",
    gcTime: 0,
  });

  const freshStripped = freshQuery.data ? stripForEdit(freshQuery.data) : null;

  return (
    <Modal
      open
      onClose={onClose}
      labelledBy="drift-diff-title"
      size="lg"
      z={60}
      panelClassName="flex h-full max-h-[820px] flex-col overflow-hidden"
    >
      <>
        {/* Header */}
        <header className="shrink-0 border-b border-border bg-surface px-5 py-3">
          <div className="font-mono text-[10px] uppercase tracking-[0.10em] text-yellow-700 dark:text-yellow-300">
            cluster diff · viewing
          </div>
          <h2
            id="drift-diff-title"
            className="mt-0.5 font-display text-[20px] leading-tight text-ink"
          >
            Cluster state changed since you opened this editor
          </h2>
          <p className="mt-1 max-w-[720px] font-mono text-[11.5px] leading-relaxed text-ink-muted">
            Top: your pristine baseline. Bottom: the cluster's current state. Reload to drop your edits and continue from the latest.
          </p>
        </header>

        {/* Body */}
        <div className="flex min-h-0 flex-1 flex-col">
          {freshQuery.isLoading && (
            <DetailLoading label="fetching latest cluster state…" />
          )}
          {freshQuery.isError && (
            <DetailError
              message={(freshQuery.error as Error)?.message ?? "fetch failed"}
            />
          )}
          {freshStripped !== null && (
            <InlineDiff original={pristineYaml} proposed={freshStripped} />
          )}
        </div>

        {/* Footer */}
        <footer className="shrink-0 border-t border-border bg-surface px-5 py-3 flex items-center justify-end gap-2">
          <button
            type="button"
            onClick={onClose}
            className="rounded-sm border border-border-strong px-3 py-1.5 font-mono text-[11.5px] text-ink-muted transition-colors hover:border-ink-muted hover:text-ink"
          >
            close
          </button>
          <button
            type="button"
            onClick={() => {
              onReload();
              onClose();
            }}
            disabled={freshQuery.isLoading || freshQuery.isError}
            className={cn(
              "rounded-sm border px-3 py-1.5 font-mono text-[11.5px] font-medium transition-colors",
              "border-yellow-700 bg-yellow text-bg hover:brightness-110",
              "disabled:cursor-not-allowed disabled:opacity-50",
            )}
            title="Discard your edits and load the latest cluster state"
          >
            reload from cluster
          </button>
        </footer>
      </>
    </Modal>
  );
}
