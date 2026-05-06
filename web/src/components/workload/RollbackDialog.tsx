// RollbackDialog — workload rollback for Deployment / StatefulSet /
// DaemonSet (issue #71).
//
// What's in the box (kept inline as sub-components rather than spread
// across files — the dialog is the only consumer of each):
//   - GitOpsBanner       — yellow warning when the workload is
//                          managed by ArgoCD / Helm / Flux.
//   - PausedRolloutPane  — replaces the picker for paused Deployments;
//                          offers a Resume button so the operator
//                          can recover without dropping to kubectl.
//   - RevisionPicker     — list of revisions with current marker,
//                          change-cause, age, image summary.
//   - RevisionDiff       — Monaco inline diff of current vs selected
//                          pod template (YAML).
//   - HpaNote            — informational chip when an HPA targets the
//                          workload.
//   - ReasonField        — optional one-liner that flows into the
//                          kubernetes.io/change-cause annotation and
//                          the audit row.

import { useEffect, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { stringify as yamlStringify } from "yaml";

import { Modal } from "../ui/Modal";
import { InlineDiff } from "../detail/yaml/InlineDiff";
import { ApiError, api, type RollbackableKind } from "../../lib/api";
import { queryKeys } from "../../lib/queryKeys";
import { showToast } from "../../lib/toastBus";
import { useRevisionHistory } from "../../hooks/useRevisionHistory";
import type {
  ManagedBy,
  Revision,
  RevisionHistory,
  RollbackResponse,
} from "../../lib/types";

interface RollbackDialogProps {
  open: boolean;
  onClose: () => void;
  cluster: string;
  kind: RollbackableKind;
  namespace: string;
  name: string;
}

export function RollbackDialog(props: RollbackDialogProps) {
  const { open, onClose, cluster, kind, namespace, name } = props;
  const labelId = "rollback-dialog-title";
  const qc = useQueryClient();

  const history = useRevisionHistory({
    cluster,
    kind,
    namespace,
    name,
    enabled: open,
  });

  // Reset selection state on each open so a stale pick doesn't carry
  // over when the user closes mid-decision and re-opens later. The
  // alternative — keying the Modal on `open` — would tear down the
  // Monaco diff editor on every toggle, so the setState-in-effect is
  // worth the lint exemption.
  const [selectedRevision, setSelectedRevision] = useState<number | null>(null);
  const [reason, setReason] = useState("");
  useEffect(() => {
    if (open) {
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setSelectedRevision(null);
      setReason("");
    }
  }, [open]);

  const rollback = useMutation<RollbackResponse, ApiError | Error, number>({
    mutationFn: (revision) =>
      api.rollback(cluster, kind, namespace, name, {
        revision,
        reason: reason.trim() || undefined,
      }),
    onSuccess: (resp) => {
      showToast(
        `rolled back ${kindLabel(kind)} ${name} → revision ${
          resp.newRevision || "(observing)"
        }`,
        "success",
      );
      // Invalidate the kind subtree so the detail pane / list pick up
      // the new revision and rolling-update state on the next tick.
      qc.invalidateQueries({ queryKey: queryKeys.cluster(cluster).kind(kind).all });
      qc.invalidateQueries({
        queryKey: queryKeys.cluster(cluster).kind(kind).revisions(namespace, name),
      });
      onClose();
    },
    onError: (err) => {
      showToast(`rollback failed: ${friendlyError(err)}`, "error");
    },
  });

  const data = history.data;
  const isPaused = data?.paused === true;

  return (
    <Modal open={open} onClose={onClose} labelledBy={labelId} size="lg">
      <header className="flex items-baseline justify-between border-b border-border px-5 py-3">
        <h2
          id={labelId}
          className="font-display text-[20px] italic text-ink"
        >
          Rollback {kindLabel(kind)} <span className="text-ink-muted">·</span>{" "}
          <span className="font-mono text-[14px] text-ink-muted not-italic">
            {namespace} / {name}
          </span>
        </h2>
        <button
          type="button"
          onClick={onClose}
          className="font-mono text-[12px] text-ink-faint hover:text-ink"
        >
          close
        </button>
      </header>

      {history.isPending && (
        <div className="flex items-center justify-center px-5 py-12">
          <div className="font-mono text-[12px] text-ink-faint">
            loading revision history…
          </div>
        </div>
      )}

      {history.isError && (
        <div className="px-5 py-6">
          <div className="border border-red bg-red/5 px-4 py-3 text-[13px] text-ink">
            could not load revision history:{" "}
            <span className="font-mono">{friendlyError(history.error)}</span>
          </div>
        </div>
      )}

      {data && (
        <>
          {data.managedBy && <GitOpsBanner managedBy={data.managedBy} />}

          {isPaused ? (
            <PausedRolloutPane
              cluster={cluster}
              kind={kind}
              namespace={namespace}
              name={name}
              onResumed={() => {
                qc.invalidateQueries({
                  queryKey: queryKeys
                    .cluster(cluster)
                    .kind(kind)
                    .revisions(namespace, name),
                });
              }}
              onClose={onClose}
            />
          ) : (
            <RollbackBody
              data={data}
              selected={selectedRevision}
              onSelect={setSelectedRevision}
              reason={reason}
              onReasonChange={setReason}
              onConfirm={() => {
                if (selectedRevision != null) rollback.mutate(selectedRevision);
              }}
              onCancel={onClose}
              submitting={rollback.isPending}
            />
          )}
        </>
      )}
    </Modal>
  );
}

// ─── body ────────────────────────────────────────────────────────────

interface RollbackBodyProps {
  data: RevisionHistory;
  selected: number | null;
  onSelect: (rev: number) => void;
  reason: string;
  onReasonChange: (r: string) => void;
  onConfirm: () => void;
  onCancel: () => void;
  submitting: boolean;
}

function RollbackBody(props: RollbackBodyProps) {
  const { data, selected, onSelect, reason, onReasonChange, onConfirm, onCancel, submitting } = props;
  const current = data.revisions.find((r) => r.isCurrent);
  const target = selected != null ? data.revisions.find((r) => r.revision === selected) : null;

  return (
    <div className="grid grid-cols-1 gap-0 lg:grid-cols-[minmax(360px,_45%)_1fr]">
      {/* Left: revision list */}
      <div className="max-h-[55vh] overflow-y-auto border-b border-border lg:border-b-0 lg:border-r">
        <RevisionPicker
          revisions={data.revisions}
          selected={selected}
          onSelect={onSelect}
        />
      </div>

      {/* Right: diff + footer */}
      <div className="flex min-w-0 flex-col">
        <div className="min-h-[280px] max-h-[55vh] overflow-hidden border-b border-border">
          {target && current ? (
            <RevisionDiff current={current} target={target} />
          ) : (
            <div className="flex h-full min-h-[280px] items-center justify-center px-6 text-center">
              <p className="font-mono text-[12px] text-ink-faint">
                pick a revision on the left to preview the diff
              </p>
            </div>
          )}
        </div>

        <div className="space-y-3 px-5 py-4">
          {data.hpaTarget && <HpaNote hpa={data.hpaTarget} />}
          <ReasonField value={reason} onChange={onReasonChange} />
          <div className="flex items-center justify-end gap-2 pt-1">
            <button
              type="button"
              onClick={onCancel}
              disabled={submitting}
              className="border border-border px-3 py-1 font-mono text-[12px] text-ink-muted hover:text-ink disabled:opacity-50"
            >
              cancel
            </button>
            <button
              type="button"
              onClick={onConfirm}
              disabled={selected == null || submitting}
              className="border border-accent bg-accent px-3 py-1 font-mono text-[12px] text-white disabled:opacity-50"
            >
              {submitting
                ? "rolling back…"
                : selected != null
                  ? `roll back to revision ${selected}`
                  : "roll back"}
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}

// ─── revision picker ────────────────────────────────────────────────

interface RevisionPickerProps {
  revisions: Revision[];
  selected: number | null;
  onSelect: (rev: number) => void;
}

function RevisionPicker({ revisions, selected, onSelect }: RevisionPickerProps) {
  return (
    <ul className="divide-y divide-border">
      {revisions.map((rev) => {
        const active = selected === rev.revision;
        const disabled = rev.isCurrent;
        return (
          <li key={rev.revision}>
            <button
              type="button"
              disabled={disabled}
              onClick={() => onSelect(rev.revision)}
              aria-pressed={active}
              className={[
                "flex w-full items-start gap-3 px-4 py-2.5 text-left",
                disabled
                  ? "cursor-default opacity-60"
                  : active
                    ? "bg-accent/10"
                    : "hover:bg-bg-soft",
              ].join(" ")}
            >
              <span
                className={[
                  "mt-0.5 font-mono text-[12px]",
                  rev.isCurrent ? "text-accent" : active ? "text-accent" : "text-ink-faint",
                ].join(" ")}
                aria-hidden="true"
              >
                {rev.isCurrent ? "●" : active ? "◉" : "○"}
              </span>
              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-2">
                  <span className="font-mono text-[13px] text-ink">
                    rev {rev.revision}
                  </span>
                  {rev.isCurrent && (
                    <span className="font-mono text-[10.5px] uppercase tracking-wider text-accent">
                      current
                    </span>
                  )}
                  <span className="font-mono text-[11px] text-ink-faint">
                    {formatAge(rev.createdAt)}
                  </span>
                </div>
                {rev.changeCause && (
                  <div className="truncate font-mono text-[12px] text-ink-muted">
                    {rev.changeCause}
                  </div>
                )}
                <div className="mt-0.5 flex flex-wrap items-center gap-x-2 gap-y-0.5 font-mono text-[11px] text-ink-faint">
                  {rev.images.map((img, i) => (
                    <span key={`${img}-${i}`} className="truncate">
                      {img}
                    </span>
                  ))}
                </div>
              </div>
            </button>
          </li>
        );
      })}
    </ul>
  );
}

// ─── inline diff ─────────────────────────────────────────────────────

function RevisionDiff({ current, target }: { current: Revision; target: Revision }) {
  // Monaco diff wants strings; convert each pod template to YAML so
  // operators see the same shape they edit elsewhere in Periscope.
  const currentYaml = yamlStringify(current.podTemplate);
  const targetYaml = yamlStringify(target.podTemplate);
  return (
    <div className="h-full min-h-[280px]">
      <InlineDiff original={currentYaml} proposed={targetYaml} />
    </div>
  );
}

// ─── GitOps banner ───────────────────────────────────────────────────

function GitOpsBanner({ managedBy }: { managedBy: ManagedBy }) {
  const label = controllerLabel(managedBy.controller);
  return (
    <div className="border-b border-border bg-yellow/10 px-5 py-3">
      <p className="text-[13px] text-ink">
        <span className="font-mono text-[11px] uppercase tracking-wider text-yellow">
          managed by {label}
        </span>
        {managedBy.instance && (
          <span className="ml-2 font-mono text-[12px] text-ink-muted">
            ({managedBy.instance})
          </span>
        )}
      </p>
      <p className="mt-1 text-[12.5px] text-ink-muted">
        a rollback applied here will be reconciled away on the next sync
        unless you also revert the source. consider pausing sync first or
        reverting the controller's source-of-truth instead.
      </p>
    </div>
  );
}

// ─── paused-deployment pane ─────────────────────────────────────────

interface PausedRolloutPaneProps {
  cluster: string;
  kind: RollbackableKind;
  namespace: string;
  name: string;
  onResumed: () => void;
  onClose: () => void;
}

function PausedRolloutPane(props: PausedRolloutPaneProps) {
  const { cluster, kind, namespace, name, onResumed, onClose } = props;
  // Resume is a one-line strategic merge patch via the existing apply
  // surface. Keeps the dialog self-contained without a new mutation
  // hook for a single-shot affordance.
  const resume = useMutation<unknown, ApiError | Error, void>({
    mutationFn: async () => {
      const yaml = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: ${name}
  namespace: ${namespace}
spec:
  paused: false
`;
      const url = `/api/clusters/${encodeURIComponent(cluster)}/resources/apps/v1/${encodeURIComponent(kind)}/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}?force=true`;
      const res = await fetch(url, {
        method: "PATCH",
        headers: { "Content-Type": "application/x-yaml" },
        body: yaml,
      });
      if (!res.ok) {
        const text = await res.text().catch(() => "");
        throw new ApiError(`resume failed: ${res.status}`, res.status, text);
      }
      return res.json();
    },
    onSuccess: () => {
      showToast(
        `${name} resumed — open the dialog again to roll back`,
        "success",
      );
      onResumed();
      onClose();
    },
    onError: (err) => {
      showToast(`resume failed: ${friendlyError(err)}`, "error");
    },
  });

  return (
    <div className="px-5 py-6">
      <p className="text-[13px] text-ink">
        This Deployment is{" "}
        <span className="font-mono text-yellow">paused</span> — the rollback
        patch would land but the controller wouldn't act on it until you
        resume the rollout.
      </p>
      <p className="mt-2 text-[12.5px] text-ink-muted">
        Resume first; then open this dialog again to pick a revision.
      </p>
      <div className="mt-4 flex items-center justify-end gap-2">
        <button
          type="button"
          onClick={onClose}
          className="border border-border px-3 py-1 font-mono text-[12px] text-ink-muted hover:text-ink"
        >
          cancel
        </button>
        <button
          type="button"
          onClick={() => resume.mutate()}
          disabled={resume.isPending}
          className="border border-accent bg-accent px-3 py-1 font-mono text-[12px] text-white disabled:opacity-50"
        >
          {resume.isPending ? "resuming…" : "resume rollout"}
        </button>
      </div>
    </div>
  );
}

// ─── HPA note + reason field ─────────────────────────────────────────

function HpaNote({ hpa }: { hpa: string }) {
  return (
    <p className="font-mono text-[11.5px] text-ink-muted">
      <span className="text-ink-faint">note: </span>
      hpa <span className="text-ink">{hpa}</span> targets this workload —
      replicas remain hpa-managed; rollback only changes the pod template.
    </p>
  );
}

function ReasonField({
  value,
  onChange,
}: {
  value: string;
  onChange: (v: string) => void;
}) {
  return (
    <label className="block">
      <span className="font-mono text-[10.5px] uppercase tracking-wider text-ink-faint">
        reason (optional)
      </span>
      <input
        type="text"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder="why? (recorded in revision history + audit)"
        maxLength={200}
        className="mt-1 w-full border border-border bg-bg px-2.5 py-1.5 font-mono text-[12.5px] text-ink placeholder:text-ink-faint focus:border-accent focus:outline-none"
      />
    </label>
  );
}

// ─── helpers ─────────────────────────────────────────────────────────

function controllerLabel(c: ManagedBy["controller"]): string {
  switch (c) {
    case "argocd":
      return "argocd";
    case "helm":
      return "helm";
    case "flux":
      return "flux";
  }
}

function kindLabel(k: RollbackableKind): string {
  switch (k) {
    case "deployments":
      return "deployment";
    case "statefulsets":
      return "statefulset";
    case "daemonsets":
      return "daemonset";
  }
}

function formatAge(iso: string): string {
  const ts = Date.parse(iso);
  if (Number.isNaN(ts)) return "";
  const ms = Date.now() - ts;
  const sec = Math.max(1, Math.round(ms / 1000));
  if (sec < 60) return `${sec}s ago`;
  const min = Math.round(sec / 60);
  if (min < 60) return `${min}m ago`;
  const hr = Math.round(min / 60);
  if (hr < 48) return `${hr}h ago`;
  const day = Math.round(hr / 24);
  return `${day}d ago`;
}

function friendlyError(err: unknown): string {
  if (err instanceof ApiError) {
    return `${err.status} ${err.message}${err.bodyText ? ` — ${err.bodyText.trim()}` : ""}`;
  }
  if (err instanceof Error) return err.message;
  return String(err);
}
