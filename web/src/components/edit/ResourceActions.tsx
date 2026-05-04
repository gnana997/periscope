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
// Actions render unconditionally — but disabled with a mode-aware
// tooltip when the user lacks the K8s RBAC permission. useCanIBatch
// asks the backend for every gated action in one POST so SSRR can
// batch-route the per-namespace checks. See useCanI / RFC 0002.
//
// We deliberately render disabled-not-hidden so the tier model is
// legible: a triage-tier user *sees* the boundary of their role
// instead of having actions silently disappear.

import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useCachedQueryData } from "../../hooks/useCachedQueryData";
import { ApiError, api, type ResourceRef } from "../../lib/api";
import {
  gvrkFromSource,
  type EditorSource,
} from "../../lib/customResources";
import { queryKeys } from "../../lib/queryKeys";
import { useCanIBatch } from "../../hooks/useCanI";
import { useScaleResource, isScalable } from "../../hooks/mutations/useScaleResource";
import { useEditLabels } from "../../hooks/mutations/useEditLabels";
import { useDeleteResource } from "../../hooks/mutations/useDeleteResource";
import { useRolloutRestart, isRestartable } from "../../hooks/mutations/useRolloutRestart";
import { useToggleSuspend } from "../../hooks/mutations/useToggleSuspend";
import { useTriggerCronJob } from "../../hooks/mutations/useTriggerCronJob";
import { useToggleCordon } from "../../hooks/mutations/useToggleCordon";
import { DeleteResourceModal } from "./DeleteResourceModal";
import { ScalePopover } from "./ScalePopover";
import { EditLabelsModal } from "./EditLabelsModal";
import { ConfirmActionModal } from "../ui/ConfirmActionModal";
import { EditButton } from "../detail/yaml/EditButton";
import {
  Ban,
  CircleCheck,
  Pause,
  Play,
  PlayCircle,
  RotateCw,
  Tag,
  Trash2,
} from "lucide-react";
import { IconAction } from "../IconAction";

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
  // One batched can-i call answers every action on this toolbar in
  // a single round-trip. Order matches `decisions[]` indexing below.
  const [canEdit, canDelete, canScale, canCreateJobs] = useCanIBatch(cluster, [
    { verb: "patch", resource: meta.resource, namespace: ns },
    { verb: "delete", resource: meta.resource, namespace: ns },
    { verb: "patch", resource: meta.resource, subresource: "scale", namespace: ns },
    { verb: "create", resource: "jobs", namespace: ns },
  ]);
  const showScaleButton = isScalable(yamlKind);

  const detailKey = queryKeys
    .cluster(cluster)
    .kind(yamlKind)
    .detail(ns ?? "", name);
  // Subscribe-only read of the detail cache. When the describe tab
  // populates this key, the action toolbar re-renders so per-kind
  // buttons whose visibility/text depend on cached fields (suspend,
  // unschedulable) appear/flip without waiting for the user to toggle
  // the panel.
  //
  // We DON'T register a useQuery observer here on purpose: doing so
  // with `queryFn: skipToken` poisons the merged query.options.queryFn
  // (React Query stores one queryFn per Query, taken from the most
  // recent observer to update). Any subsequent invalidation would
  // refetch with skipToken and produce a "Missing queryFn" rejection,
  // leaving the detail panel stuck at its prior payload. See
  // useCachedQueryData for the cache-only subscription this uses
  // instead.
  const cachedDetail = useCachedQueryData<DetailLike>(detailKey);

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
  // setup-only — but keeps the React tree reads obvious). The
  // RBAC dimension is now handled by per-button disabled state +
  // tooltip below; the kind dimension stays as visibility gates so
  // we don't render "scale" on a ConfigMap.
  const showRestartButton = isRestartable(yamlKind);
  const showSuspendButton = yamlKind === "cronjobs";
  const showTriggerButton = yamlKind === "cronjobs";
  const showCordonButton = yamlKind === "nodes";

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

  // Disabled-button styling shared by every gated action. Greys the
  // button and shows a not-allowed cursor while leaving the DOM node
  // present so Radix Tooltip's pointer-event capture still works.

  // Replicas-loading state for scale: distinct from "no permission",
  // tooltip differs.
  const replicasLoading = cachedDetail?.replicas === undefined;
  const scaleDisabled = !canScale.allowed || replicasLoading;
  const scaleTooltip = canScale.allowed
    ? replicasLoading
      ? "loading current replica count…"
      : `current replicas: ${cachedDetail!.replicas}`
    : canScale.tooltip;

  return (
    <div className="flex items-center gap-1.5">
      <EditButton
        disabled={!canEdit.allowed}
        disabledTooltip={canEdit.tooltip}
      />
      <IconAction
        label="Labels"
        icon={<Tag size={14} />}
        onClick={() => setShowLabels(true)}
        disabled={!canEdit.allowed}
        disabledTooltip={canEdit.tooltip}
      />
      {showScaleButton && cachedDetail?.replicas !== undefined && (
        <ScalePopover
          cluster={cluster}
          kind={yamlKind}
          namespace={ns ?? ""}
          name={name}
          currentReplicas={cachedDetail.replicas}
          disabled={scaleDisabled}
          disabledTooltip={scaleTooltip}
          onSubmit={(replicas) => scaleMutation.mutate({ replicas })}
        />
      )}
      <IconAction
        label="Delete"
        icon={<Trash2 size={14} />}
        onClick={() => setShowDelete(true)}
        disabled={!canDelete.allowed}
        disabledTooltip={canDelete.tooltip}
        tone="danger"
      />
      {showRestartButton && (
        <IconAction
          label="Restart"
          icon={<RotateCw size={14} />}
          onClick={() => setShowRestart(true)}
          disabled={!canEdit.allowed}
          disabledTooltip={canEdit.tooltip}
        />
      )}
      {showSuspendButton && cachedCronJob !== undefined && (
        <IconAction
          label={cachedCronJob.suspend ? "Resume" : "Suspend"}
          icon={
            cachedCronJob.suspend ? <Play size={14} /> : <Pause size={14} />
          }
          onClick={() => setShowSuspend(true)}
          disabled={!canEdit.allowed}
          disabledTooltip={canEdit.tooltip}
          tone={cachedCronJob.suspend ? "active" : "default"}
          pressed={cachedCronJob.suspend}
        />
      )}
      {showTriggerButton && (
        <IconAction
          label="Trigger now"
          icon={<PlayCircle size={14} />}
          onClick={() => setShowTrigger(true)}
          disabled={!canCreateJobs.allowed}
          disabledTooltip={canCreateJobs.tooltip}
        />
      )}
      {showCordonButton && cachedNode !== undefined && (
        <IconAction
          label={cachedNode.unschedulable ? "Uncordon" : "Cordon"}
          icon={
            cachedNode.unschedulable ? (
              <CircleCheck size={14} />
            ) : (
              <Ban size={14} />
            )
          }
          onClick={() => setShowCordon(true)}
          disabled={!canEdit.allowed}
          disabledTooltip={canEdit.tooltip}
          tone={cachedNode.unschedulable ? "active" : "default"}
          pressed={cachedNode.unschedulable}
        />
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
  const [canEdit, canDelete] = useCanIBatch(cluster, [
    { verb: "patch", resource: meta.resource, namespace: ns },
    { verb: "delete", resource: meta.resource, namespace: ns },
  ]);

  const qc = useQueryClient();

  return (
    <div className="flex items-center gap-1.5">
      <EditButton
        disabled={!canEdit.allowed}
        disabledTooltip={canEdit.tooltip}
      />
      <IconAction
        label="Delete"
        icon={<Trash2 size={14} />}
        onClick={() => setShowDelete(true)}
        disabled={!canDelete.allowed}
        disabledTooltip={canDelete.tooltip}
        tone="danger"
      />
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
