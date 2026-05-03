// ScaleModal — set spec.replicas for a Deployment / StatefulSet /
// ReplicaSet. The mutation lives in useScaleResource and is optimistic
// — by the time this modal closes the StatStrip already shows the new
// value, so the modal itself does not render a spinner.
//
// HPA awareness: if managedFields shows a CONTROLLER-category manager
// (notably horizontal-pod-autoscaler) owns spec.replicas, render an
// amber chip warning the user that the HPA will likely overwrite their
// scale within a control-loop interval. Non-blocking — operators
// sometimes deliberately bump replicas to clear a backlog quickly and
// accept that the HPA will normalize back.

import { useEffect, useMemo, useRef, useState } from "react";
import { cn } from "../../lib/cn";
import { KIND_REGISTRY } from "../../lib/k8sKinds";
import {
  parseManagedFields,
  pathToManager,
} from "../../lib/managedFields";
import { classifyManager } from "../../lib/managers";
import { useResourceMeta } from "../../hooks/useResource";
import { Modal } from "../ui/Modal";
import type { YamlKind } from "../../lib/api";

interface ScaleModalProps {
  cluster: string;
  kind: YamlKind;
  namespace: string;
  name: string;
  currentReplicas: number;
  onClose: () => void;
  onSubmit: (replicas: number) => void;
}

export function ScaleModal({
  cluster,
  kind,
  namespace,
  name,
  currentReplicas,
  onClose,
  onSubmit,
}: ScaleModalProps) {
  const [value, setValue] = useState<string>(String(currentReplicas));
  const inputRef = useRef<HTMLInputElement | null>(null);
  const meta = KIND_REGISTRY[kind];

  // Auto-focus & select on mount so the most common interaction
  // (overwrite the current value) is one keystroke.
  useEffect(() => {
    const t = setTimeout(() => {
      inputRef.current?.focus();
      inputRef.current?.select();
    }, 50);
    return () => clearTimeout(t);
  }, []);

  // Pull managedFields to spot HPA ownership of spec.replicas.
  const metaQuery = useResourceMeta(
    cluster,
    {
      group: meta.group,
      version: meta.version,
      resource: meta.resource,
      namespace,
      name,
    },
    true,
  );

  const replicasOwner = useMemo(() => {
    const owners = parseManagedFields(metaQuery.data?.managedFields);
    const map = pathToManager(owners);
    const manager = map.get("spec.replicas");
    if (!manager) return null;
    const info = classifyManager(manager);
    return info;
  }, [metaQuery.data]);

  const showHpaWarning =
    replicasOwner !== null && replicasOwner.category === "CONTROLLER";

  // Validation: non-negative integer.
  const parsed = Number(value);
  const valid =
    value.trim() !== "" &&
    Number.isInteger(parsed) &&
    parsed >= 0;
  const unchanged = valid && parsed === currentReplicas;
  const submitDisabled = !valid || unchanged;

  function bump(delta: number) {
    const n = Math.max(0, (Number.isInteger(parsed) ? parsed : 0) + delta);
    setValue(String(n));
  }

  function submit() {
    if (submitDisabled) return;
    onSubmit(parsed);
    onClose();
  }

  function onKeyDown(e: React.KeyboardEvent<HTMLInputElement>) {
    if ((e.metaKey || e.ctrlKey) && e.key === "Enter") {
      e.preventDefault();
      submit();
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      bump(1);
    } else if (e.key === "ArrowDown") {
      e.preventDefault();
      bump(-1);
    }
  }

  return (
    <Modal open onClose={onClose} labelledBy="scale-title" size="sm">
      <div className="border-b border-border px-5 py-3 font-mono text-sm">
        <span className="text-ink-muted">scale</span>{" "}
        <span id="scale-title" className="text-ink">
          {meta.kind} {name}
        </span>
      </div>

      <div className="space-y-4 px-5 py-4">
        <div>
          <label
            htmlFor="scale-replicas"
            className="mb-1.5 block font-mono text-[11px] text-ink-muted"
          >
            replicas (current: {currentReplicas})
          </label>
          <div className="flex items-stretch gap-1">
            <button
              type="button"
              onClick={() => bump(-1)}
              disabled={!valid || parsed <= 0}
              aria-label="decrease replicas"
              className="rounded-sm border border-border-strong bg-surface px-3 font-mono text-sm text-ink-muted hover:text-ink disabled:cursor-not-allowed disabled:opacity-40"
            >
              −
            </button>
            <input
              ref={inputRef}
              id="scale-replicas"
              type="number"
              inputMode="numeric"
              min={0}
              step={1}
              value={value}
              onChange={(e) => setValue(e.target.value)}
              onKeyDown={onKeyDown}
              className={cn(
                "min-w-0 flex-1 rounded-sm border bg-bg px-3 py-2 text-center font-mono text-sm text-ink outline-none",
                valid ? "border-border" : "border-red",
                "focus:border-ink-muted",
              )}
              aria-invalid={!valid}
              aria-describedby={valid ? undefined : "scale-error"}
            />
            <button
              type="button"
              onClick={() => bump(1)}
              disabled={!valid && value.trim() !== ""}
              aria-label="increase replicas"
              className="rounded-sm border border-border-strong bg-surface px-3 font-mono text-sm text-ink-muted hover:text-ink disabled:cursor-not-allowed disabled:opacity-40"
            >
              +
            </button>
          </div>
          {!valid && (
            <p
              id="scale-error"
              className="mt-1.5 font-mono text-[11px] text-red"
            >
              must be a non-negative integer
            </p>
          )}
        </div>

        {showHpaWarning && replicasOwner && (
          <div className="rounded-sm border-l-[3px] border-yellow bg-yellow-soft px-3 py-2">
            <div className="font-mono text-[10.5px] font-medium text-yellow">
              {replicasOwner.display} controls replicas
            </div>
            <p className="mt-1 text-[12px] leading-relaxed text-ink">
              {replicasOwner.consequence ||
                "A controller will likely overwrite your scale on the next reconcile."}
            </p>
          </div>
        )}
      </div>

      <div className="flex items-center justify-end gap-2 border-t border-border px-5 py-3">
        <button
          type="button"
          onClick={onClose}
          className="rounded-sm border border-border-strong px-3 py-1.5 font-mono text-sm text-ink-muted transition-colors hover:border-ink-muted hover:text-ink"
        >
          cancel
        </button>
        <button
          type="button"
          onClick={submit}
          disabled={submitDisabled}
          className="rounded-sm bg-accent px-3 py-1.5 font-mono text-sm text-bg transition-opacity hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-40"
          title="⌘/Ctrl+Enter to submit"
        >
          scale
        </button>
      </div>
    </Modal>
  );
}
