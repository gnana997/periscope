// ResourceActions — the action bar shown in detail panel tab strips.
// Centralises styling + RBAC gating + mutation wiring so the
// per-kind detail components stay declarative.
//
// For built-in kinds: shows {Edit YAML, Edit labels, Scale (scalable
// kinds only), Delete}. Each destructive / write action runs through
// a dedicated mutation hook (useEditLabels, useScaleResource,
// useDeleteResource) that optimistically updates the React Query
// cache before the network call lands, so the UI feels instant on
// slow links.
//
// For Custom Resources: shows {Edit YAML, Delete} only — Scale and
// Edit Labels assume a built-in cache shape (KIND_REGISTRY,
// LIST_ITEMS_KEY) that doesn't generalise to arbitrary CRDs. Adding
// optimistic CR labels/scale is tracked as a follow-up.
//
// Actions are shown unconditionally in v1 — the backend is the
// authoritative gate (impersonated K8s RBAC). useCanI() is called
// anyway so when SSAR plumbing lands in v1.x every site upgrades for
// free. See useCanI for the rationale.

import { useState } from "react";
import { skipToken, useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiError, api, type ResourceRef } from "../../lib/api";
import {
  gvrkFromSource,
  type EditorSource,
} from "../../lib/customResources";
import { queryKeys } from "../../lib/queryKeys";
import { useCanI } from "../../hooks/useCanI";
import { useScaleResource, isScalable } from "../../hooks/mutations/useScaleResource";
import { useEditLabels } from "../../hooks/mutations/useEditLabels";
import { useDeleteResource } from "../../hooks/mutations/useDeleteResource";
import { useRolloutRestart, isRestartable } from "../../hooks/mutations/useRolloutRestart";
import { useToggleSuspend } from "../../hooks/mutations/useToggleSuspend";
import { useTriggerCronJob } from "../../hooks/mutations/useTriggerCronJob";
import { useToggleCordon } from "../../hooks/mutations/useToggleCordon";
import { cn } from "../../lib/cn";
import { DeleteResourceModal } from "./DeleteResourceModal";
import { ScaleModal } from "./ScaleModal";
import { EditLabelsModal } from "./EditLabelsModal";
import { ConfirmActionModal } from "../ui/ConfirmActionModal";
import { EditButton } from "../detail/yaml/EditButton";

interface ResourceActionsProps {
  cluster: string;
  source: EditorSource;
  // null/undefined → cluster-scoped resource.
  namespace: string | null | undefined;
  name: string;
  // Optional: actions to render after edit/delete (e.g. "open shell").
  trailing?: React.ReactNode;
  // Called after a successful delete so the page can navigate away
  // from the now-gone selection. Edit invalidates from inside YamlEditor.
  onDeleted?: () => void;
}

export function ResourceActions(props: ResourceActionsProps) {
  // Branch on source kind. Built-ins use the full Lane 2 mutation
  // wiring; CRs use a simpler delete-only path that doesn't depend
  // on KIND_REGISTRY / list-shape lookups.
  if (props.source.kind === "builtin") {
    return <BuiltinActions {...props} source={props.source} />;
  }
  return <CustomResourceActions {...props} source={props.source} />;
}

interface DetailLike {
  replicas?: number;
  labels?: Record<string, string>;
}

function BuiltinActions({
  cluster,
  source,
  namespace,
  name,
  trailing,
  onDeleted,
}: ResourceActionsProps & {
  source: Extract<EditorSource, { kind: "builtin" }>;
}) {
  const [showDelete, setShowDelete] = useState(false);
  const [showScale, setShowScale] = useState(false);
  const [showLabels, setShowLabels] = useState(false);
  const [showRestart, setShowRestart] = useState(false);
  const [showSuspend, setShowSuspend] = useState(false);
  const [showTrigger, setShowTrigger] = useState(false);
  const [showCordon, setShowCordon] = useState(false);

  const yamlKind = source.yamlKind;
  const meta = gvrkFromSource(source);
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

  const detailKey = queryKeys
    .cluster(cluster)
    .kind(yamlKind)
    .detail(ns ?? "", name);
  // Subscribe-only read (skipToken = no fetch). When the describe tab
  // populates the cache, this re-renders so per-kind buttons whose
  // visibility/text depend on cached fields (suspend, unschedulable)
  // appear/flip without waiting for the user to toggle the panel.
  const { data: cachedDetail } = useQuery<DetailLike>({
    queryKey: detailKey,
    queryFn: skipToken,
  });

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


  // Phase 5: workload-level + node-level + cronjob ops actions.
  // Each hook is gated on the kind so the mutation isn't constructed
  // for kinds that don't support it (still cheap — useMutation is
  // setup-only — but keeps the React tree reads obvious).
  const showRestartButton =
    canEdit && isRestartable(yamlKind);
  const showSuspendButton = canEdit && yamlKind === "cronjobs";
  const canCreateJobs = useCanI({
    verb: "create",
    resource: "jobs",
    namespace: ns,
  });
  const showTriggerButton = yamlKind === "cronjobs" && canCreateJobs;
  const showCordonButton = canEdit && yamlKind === "nodes";

  const cachedCronJob = cachedDetail as { suspend?: boolean } | undefined;
  const cachedNode = cachedDetail as { unschedulable?: boolean } | undefined;

  const restartMutation = useRolloutRestart({
    cluster,
    kind: yamlKind,
    namespace: ns ?? "",
    name,
  });
  const suspendMutation = useToggleSuspend({
    cluster,
    namespace: ns ?? "",
    name,
  });
  const triggerMutation = useTriggerCronJob({
    cluster,
    namespace: ns ?? "",
    name,
  });
  const cordonMutation = useToggleCordon({
    cluster,
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
      {showRestartButton && (
        <button
          type="button"
          onClick={() => setShowRestart(true)}
          className="rounded-sm border border-border-strong px-2.5 py-1 font-mono text-[12px] text-ink-muted transition-colors hover:bg-surface-2 hover:text-ink"
        >
          restart
        </button>
      )}
      {showSuspendButton && cachedCronJob !== undefined && (
        <button
          type="button"
          onClick={() => setShowSuspend(true)}
          className={cn(
            "rounded-sm border px-2.5 py-1 font-mono text-[12px] transition-colors",
            cachedCronJob.suspend
              ? "border-yellow/40 bg-yellow/10 text-yellow hover:brightness-110"
              : "border-border-strong text-ink-muted hover:bg-surface-2 hover:text-ink",
          )}
        >
          {cachedCronJob.suspend ? "resume" : "suspend"}
        </button>
      )}
      {showTriggerButton && (
        <button
          type="button"
          onClick={() => setShowTrigger(true)}
          className="rounded-sm border border-border-strong px-2.5 py-1 font-mono text-[12px] text-ink-muted transition-colors hover:bg-surface-2 hover:text-ink"
        >
          trigger now
        </button>
      )}
      {showCordonButton && cachedNode !== undefined && (
        <button
          type="button"
          onClick={() => setShowCordon(true)}
          className={cn(
            "rounded-sm border px-2.5 py-1 font-mono text-[12px] transition-colors",
            cachedNode.unschedulable
              ? "border-yellow/40 bg-yellow/10 text-yellow hover:brightness-110"
              : "border-border-strong text-ink-muted hover:bg-surface-2 hover:text-ink",
          )}
        >
          {cachedNode.unschedulable ? "uncordon" : "cordon"}
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

      <ConfirmActionModal
        open={showRestart}
        title={`Restart ${meta.kind} ${name}?`}
        body={
          <>
            Cycles all pods of <span className="text-ink">{name}</span>. Pods
            roll out one batch at a time per the workload's strategy; total
            ready pods drop briefly during the rollout.
          </>
        }
        confirmLabel="restart"
        variant="danger"
        pending={restartMutation.isPending}
        error={restartMutation.error?.message ?? null}
        onCancel={() => {
          if (restartMutation.isPending) return;
          restartMutation.reset();
          setShowRestart(false);
        }}
        onConfirm={() => {
          restartMutation.mutate(undefined, {
            onSuccess: () => setShowRestart(false),
          });
        }}
      />

      <ConfirmActionModal
        open={showSuspend}
        title={
          cachedCronJob?.suspend
            ? `Resume cronjob ${name}?`
            : `Suspend cronjob ${name}?`
        }
        body={
          cachedCronJob?.suspend ? (
            <>
              The schedule resumes immediately and will fire on the next
              cron tick.
            </>
          ) : (
            <>
              The schedule will not fire while suspended. In-flight Jobs
              continue running.
            </>
          )
        }
        confirmLabel={cachedCronJob?.suspend ? "resume" : "suspend"}
        variant="warn"
        pending={suspendMutation.isPending}
        error={suspendMutation.error?.message ?? null}
        onCancel={() => {
          if (suspendMutation.isPending) return;
          suspendMutation.reset();
          setShowSuspend(false);
        }}
        onConfirm={() => {
          suspendMutation.mutate(
            { suspend: !(cachedCronJob?.suspend ?? false) },
            { onSuccess: () => setShowSuspend(false) },
          );
        }}
      />

      <ConfirmActionModal
        open={showTrigger}
        title={`Trigger cronjob ${name} now?`}
        body={
          <>
            Creates a new Job from this CronJob's spec.jobTemplate. The
            CronJob's schedule isn't affected — this is an out-of-band
            run.
          </>
        }
        confirmLabel="trigger"
        variant="warn"
        pending={triggerMutation.isPending}
        error={triggerMutation.error?.message ?? null}
        onCancel={() => {
          if (triggerMutation.isPending) return;
          triggerMutation.reset();
          setShowTrigger(false);
        }}
        onConfirm={() => {
          triggerMutation.mutate(undefined, {
            onSuccess: () => setShowTrigger(false),
          });
        }}
      />

      <ConfirmActionModal
        open={showCordon}
        title={
          cachedNode?.unschedulable
            ? `Uncordon node ${name}?`
            : `Cordon node ${name}?`
        }
        body={
          cachedNode?.unschedulable ? (
            <>The scheduler will resume placing new pods on this node.</>
          ) : (
            <>
              The scheduler will skip this node for new pod placements.
              Existing pods stay running. Use Drain (coming separately)
              to evict them.
            </>
          )
        }
        confirmLabel={cachedNode?.unschedulable ? "uncordon" : "cordon"}
        variant="warn"
        pending={cordonMutation.isPending}
        error={cordonMutation.error?.message ?? null}
        onCancel={() => {
          if (cordonMutation.isPending) return;
          cordonMutation.reset();
          setShowCordon(false);
        }}
        onConfirm={() => {
          cordonMutation.mutate(
            { unschedulable: !(cachedNode?.unschedulable ?? false) },
            { onSuccess: () => setShowCordon(false) },
          );
        }}
      />
    </div>
  );
}

function CustomResourceActions({
  cluster,
  source,
  namespace,
  name,
  trailing,
  onDeleted,
}: ResourceActionsProps & {
  source: Extract<EditorSource, { kind: "custom" }>;
}) {
  const [showDelete, setShowDelete] = useState(false);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<{ status?: number; message: string } | null>(null);

  const meta = gvrkFromSource(source);
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

  const qc = useQueryClient();

  return (
    <div className="flex items-center gap-1.5">
      {canEdit && <EditButton />}
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
          pending={pending}
          error={error}
          onClose={() => {
            if (pending) return;
            setError(null);
            setShowDelete(false);
          }}
          onConfirm={async () => {
            setPending(true);
            setError(null);
            try {
              await api.deleteResource({
                cluster,
                group: meta.group,
                version: meta.version,
                resource: meta.resource,
                namespace: ns,
                name,
              });
              await qc.invalidateQueries({
                queryKey: queryKeys
                  .cluster(cluster)
                  .cr(source.cr.group, source.cr.version, source.cr.resource).all,
              });
              setShowDelete(false);
              onDeleted?.();
            } catch (e) {
              setError(deleteErrorShape(e instanceof Error ? e : new Error(String(e))));
            } finally {
              setPending(false);
            }
          }}
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
