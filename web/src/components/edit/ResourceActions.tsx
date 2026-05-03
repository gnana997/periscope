// ResourceActions — the action bar shown in detail panel tab strips.
// Centralises styling + RBAC gating + mutation wiring so the
// per-kind detail components stay declarative.
//
// Lane 2 expanded the surface from {Edit YAML, Delete} to
// {Edit YAML, Edit labels, Scale (scalable kinds only), Delete}.
// Each destructive / write action has its own dedicated mutation
// hook (useEditLabels, useScaleResource, useDeleteResource) that
// optimistically updates the React Query cache before the network
// call lands, so the UI feels instant on slow links.
//
// Actions are shown unconditionally in v1 — the backend is the
// authoritative gate (impersonated K8s RBAC). useCanI() is called
// anyway so when SSAR plumbing lands in v1.x every site upgrades for
// free. See useCanI for the rationale.

import { useState } from "react";
import { ApiError } from "../../lib/api";
import type { ResourceRef, YamlKind } from "../../lib/api";
import { KIND_REGISTRY } from "../../lib/k8sKinds";
import { useCanI } from "../../hooks/useCanI";
import { useQueryClient } from "@tanstack/react-query";
import { queryKeys } from "../../lib/queryKeys";
import { useScaleResource, isScalable } from "../../hooks/mutations/useScaleResource";
import { useEditLabels } from "../../hooks/mutations/useEditLabels";
import { useDeleteResource } from "../../hooks/mutations/useDeleteResource";
import { DeleteResourceModal } from "./DeleteResourceModal";
import { ScaleModal } from "./ScaleModal";
import { EditLabelsModal } from "./EditLabelsModal";
import { EditButton } from "../detail/yaml/EditButton";

interface ResourceActionsProps {
  cluster: string;
  // The YAML kind / URL segment. Drives the GET /yaml endpoint and the
  // GVRK lookup (group/version/resource/kind) via KIND_REGISTRY — so
  // pages don't have to repeat that information at every call site.
  yamlKind: YamlKind;
  // null/undefined → cluster-scoped resource.
  namespace: string | null | undefined;
  name: string;
  // Optional: actions to render after edit/delete (e.g. "open shell").
  trailing?: React.ReactNode;
  // Called after a successful delete so the page can navigate away
  // from the now-gone selection. Edit invalidates from inside YamlEditor.
  onDeleted?: () => void;
}

interface DetailLike {
  replicas?: number;
  labels?: Record<string, string>;
}

export function ResourceActions({
  cluster,
  yamlKind,
  namespace,
  name,
  trailing,
  onDeleted,
}: ResourceActionsProps) {
  const [showDelete, setShowDelete] = useState(false);
  const [showScale, setShowScale] = useState(false);
  const [showLabels, setShowLabels] = useState(false);
  const meta = KIND_REGISTRY[yamlKind];
  const ns = namespace ?? undefined;
  const resource: ResourceRef = {
    cluster,
    group: meta.group,
    version: meta.version,
    resource: meta.resource,
    namespace: ns,
    name,
    kind: meta.kind,
  };
  const canEdit = useCanI({ verb: "patch", resource: meta.resource, namespace: ns });
  const canDelete = useCanI({ verb: "delete", resource: meta.resource, namespace: ns });
  const canScale = useCanI({
    verb: "patch",
    resource: `${meta.resource}/scale`,
    namespace: ns,
  });
  const showScaleButton = canScale && isScalable(yamlKind);

  const qc = useQueryClient();
  const detailKey = queryKeys
    .cluster(cluster)
    .kind(yamlKind)
    .detail(ns ?? "", name);
  const cachedDetail = qc.getQueryData<DetailLike>(detailKey);

  const scaleMutation = useScaleResource({
    cluster,
    kind: yamlKind,
    namespace: ns ?? "",
    name,
  });
  const labelsMutation = useEditLabels({
    cluster,
    kind: yamlKind,
    namespace: ns ?? "",
    name,
  });
  const deleteMutation = useDeleteResource({
    cluster,
    kind: yamlKind,
    namespace: ns ?? "",
    name,
  });

  const deleteError = deleteMutation.error
    ? deleteErrorShape(deleteMutation.error)
    : null;

  return (
    <div className="flex items-center gap-1.5">
      {canEdit && <EditButton />}
      {canEdit && (
        <button
          type="button"
          onClick={() => setShowLabels(true)}
          className="rounded-sm border border-border-strong px-2.5 py-1 font-mono text-[12px] text-ink-muted transition-colors hover:bg-surface-2 hover:text-ink"
        >
          labels
        </button>
      )}
      {showScaleButton && (
        <button
          type="button"
          onClick={() => setShowScale(true)}
          disabled={cachedDetail?.replicas === undefined}
          className="rounded-sm border border-border-strong px-2.5 py-1 font-mono text-[12px] text-ink-muted transition-colors hover:bg-surface-2 hover:text-ink disabled:cursor-not-allowed disabled:opacity-50"
          title={
            cachedDetail?.replicas === undefined
              ? "loading current replica count…"
              : `current replicas: ${cachedDetail.replicas}`
          }
        >
          scale
        </button>
      )}
      {canDelete && (
        <button
          type="button"
          onClick={() => setShowDelete(true)}
          className="rounded-sm border border-border-strong px-2.5 py-1 font-mono text-[12px] text-red transition-colors hover:bg-red-soft"
        >
          delete
        </button>
      )}
      {trailing}

      {showDelete && (
        <DeleteResourceModal
          resourceRef={resource}
          pending={deleteMutation.isPending}
          error={deleteError}
          onClose={() => {
            if (deleteMutation.isPending) return;
            deleteMutation.reset();
            setShowDelete(false);
          }}
          onConfirm={() => {
            deleteMutation.mutate(undefined, {
              onSuccess: () => {
                setShowDelete(false);
                onDeleted?.();
              },
            });
          }}
        />
      )}

      {showScale && showScaleButton && cachedDetail?.replicas !== undefined && (
        <ScaleModal
          cluster={cluster}
          kind={yamlKind}
          namespace={ns ?? ""}
          name={name}
          currentReplicas={cachedDetail.replicas}
          onClose={() => setShowScale(false)}
          onSubmit={(replicas) => scaleMutation.mutate({ replicas })}
        />
      )}

      {showLabels && (
        <EditLabelsModal
          title={`${meta.kind} ${name}`}
          initialLabels={cachedDetail?.labels ?? {}}
          onClose={() => setShowLabels(false)}
          onSubmit={(labels) => labelsMutation.mutate({ labels })}
        />
      )}
    </div>
  );
}

function deleteErrorShape(err: Error): { status?: number; message: string } {
  if (err instanceof ApiError) {
    return {
      status: err.status,
      message: err.bodyText?.trim() || err.message,
    };
  }
  return { message: err.message || "delete failed" };
}
