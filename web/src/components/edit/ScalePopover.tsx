// ScalePopover — set spec.replicas via a small popover anchored
// beneath the Scale icon button in the action row. Replaces
// ScaleModal because the modal's full-page chrome looked heavy
// against the icon-only action row introduced 2026-05-04.
//
// Reuses the same logic the modal had:
//   - useResourceMeta -> managedFields -> classifyManager for HPA
//     ownership detection (amber chip when CONTROLLER owns
//     spec.replicas).
//   - non-negative integer validation; submit disabled when input is
//     equal to current or invalid.
//   - the optimistic mutation lives in the parent (useScaleResource);
//     popover just emits onSubmit(replicas) and closes.
//
// Why a popover, not a modal:
//   - Single-input action ("set N, click apply") doesn't need
//     full-page chrome.
//   - High-frequency: operators scale things often — popover means
//     no animation interrupting the cluster nav flow.
//   - Reversible: scale-to-zero stops pods but operators can scale
//     back up; that's not the destructive irreversibility modals
//     guard against (delete is). An inline amber chip warns at
//     value=0 instead.

import * as Popover from "@radix-ui/react-popover";
import { useEffect, useMemo, useRef, useState } from "react";
import { MoveVertical } from "lucide-react";
import { cn } from "../../lib/cn";
import { KIND_REGISTRY } from "../../lib/k8sKinds";
import {
  parseManagedFields,
  pathToManager,
} from "../../lib/managedFields";
import { classifyManager } from "../../lib/managers";
import { useResourceMeta } from "../../hooks/useResource";
import { IconAction } from "../IconAction";
import type { YamlKind } from "../../lib/api";

interface ScalePopoverProps {
  cluster: string;
  kind: YamlKind;
  namespace: string;
  name: string;
  currentReplicas: number;
  disabled?: boolean;
  /** Tooltip body shown when disabled (RBAC reason). */
  disabledTooltip?: string | null;
  onSubmit: (replicas: number) => void;
}

export function ScalePopover({
  cluster,
  kind,
  namespace,
  name,
  currentReplicas,
  disabled = false,
  disabledTooltip,
  onSubmit,
}: ScalePopoverProps) {
  const [open, setOpen] = useState(false);

  // When disabled, render the IconAction outside a Popover.Root so
  // hover on the disabled button still shows the RBAC tooltip from
  // IconAction itself. Popover never opens.
  if (disabled) {
    return (
      <IconAction
        label="Scale"
        icon={<MoveVertical size={14} />}
        onClick={() => {}}
        disabled
        disabledTooltip={disabledTooltip}
      />
    );
  }

  return (
    <Popover.Root open={open} onOpenChange={setOpen}>
      <Popover.Trigger asChild>
        {/* IconAction's own click is a no-op; Popover.Trigger handles
            opening via composed event handlers. The Tooltip inside
            IconAction still works because Trigger.asChild composes
            refs, not replaces them. */}
        <IconAction
          label="Scale"
          icon={<MoveVertical size={14} />}
          onClick={() => {}}
        />
      </Popover.Trigger>
      <Popover.Portal>
        <Popover.Content
          side="bottom"
          align="end"
          sideOffset={8}
          collisionPadding={8}
          // z-50 keeps the popover above modals' backdrop (matches
          // Tooltip's z-stack convention in this app).
          className="z-50 w-[280px] rounded-md border border-border-strong bg-surface shadow-[0_8px_24px_-12px_rgba(0,0,0,0.5)] data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0"
        >
          <ScalePopoverBody
            cluster={cluster}
            kind={kind}
            namespace={namespace}
            name={name}
            currentReplicas={currentReplicas}
            onSubmit={(replicas) => {
              onSubmit(replicas);
              setOpen(false);
            }}
            onCancel={() => setOpen(false)}
          />
          {/* Arrow points to the trigger button; matches the Tooltip
              arrow pattern in tone. */}
          <Popover.Arrow className="fill-border-strong" width={10} height={5} />
        </Popover.Content>
      </Popover.Portal>
    </Popover.Root>
  );
}

interface ScalePopoverBodyProps {
  cluster: string;
  kind: YamlKind;
  namespace: string;
  name: string;
  currentReplicas: number;
  onSubmit: (replicas: number) => void;
  onCancel: () => void;
}

function ScalePopoverBody({
  cluster,
  kind,
  namespace,
  name,
  currentReplicas,
  onSubmit,
  onCancel,
}: ScalePopoverBodyProps) {
  const [value, setValue] = useState<string>(String(currentReplicas));
  const inputRef = useRef<HTMLInputElement | null>(null);
  const meta = KIND_REGISTRY[kind];

  // Auto-focus + select on open. Popover.Content unmounts when
  // closed, so this fires fresh each open — no need to react to a
  // separate `open` prop.
  useEffect(() => {
    const t = setTimeout(() => {
      inputRef.current?.focus();
      inputRef.current?.select();
    }, 30);
    return () => clearTimeout(t);
  }, []);

  // Pull managedFields once on open to spot HPA ownership of
  // spec.replicas. The query is enabled, so it'll fire on first
  // open and use cached data on subsequent opens within the TTL.
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
    return classifyManager(manager);
  }, [metaQuery.data]);

  const showHpaWarning =
    replicasOwner !== null && replicasOwner.category === "CONTROLLER";

  // Validation: non-negative integer.
  const parsed = Number(value);
  const valid =
    value.trim() !== "" && Number.isInteger(parsed) && parsed >= 0;
  const unchanged = valid && parsed === currentReplicas;
  const submitDisabled = !valid || unchanged;
  const willStopAllPods = valid && parsed === 0 && currentReplicas > 0;

  function bump(delta: number) {
    const n = Math.max(0, (Number.isInteger(parsed) ? parsed : 0) + delta);
    setValue(String(n));
  }

  function submit() {
    if (submitDisabled) return;
    onSubmit(parsed);
  }

  function onKeyDown(e: React.KeyboardEvent<HTMLInputElement>) {
    if ((e.metaKey || e.ctrlKey) && e.key === "Enter") {
      e.preventDefault();
      submit();
    } else if (e.key === "Enter") {
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
    <div className="px-3 py-3">
      {/* Header row — kind + name compact, current replicas right-aligned */}
      <div className="mb-2 flex items-baseline justify-between font-mono text-[11px]">
        <span className="text-ink-muted">replicas</span>
        <span className="text-ink-faint">
          currently <span className="text-ink">{currentReplicas}</span>
        </span>
      </div>

      {/* Stepper row */}
      <div className="flex items-stretch gap-1">
        <button
          type="button"
          onClick={() => bump(-1)}
          disabled={!valid || parsed <= 0}
          aria-label="decrease replicas"
          className="rounded-sm border border-border-strong bg-bg px-3 font-mono text-sm text-ink-muted hover:text-ink disabled:cursor-not-allowed disabled:opacity-40"
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
          aria-label={`${meta.kind} ${name} replicas`}
          aria-invalid={!valid}
          className={cn(
            "min-w-0 flex-1 rounded-sm border bg-bg px-3 py-1.5 text-center font-mono text-sm text-ink outline-none",
            valid ? "border-border" : "border-red",
            "focus:border-ink-muted",
          )}
        />
        <button
          type="button"
          onClick={() => bump(1)}
          disabled={!valid && value.trim() !== ""}
          aria-label="increase replicas"
          className="rounded-sm border border-border-strong bg-bg px-3 font-mono text-sm text-ink-muted hover:text-ink disabled:cursor-not-allowed disabled:opacity-40"
        >
          +
        </button>
      </div>

      {!valid && (
        <p className="mt-1.5 font-mono text-[10.5px] text-red">
          must be a non-negative integer
        </p>
      )}

      {/* HPA chip — only when a controller owns spec.replicas */}
      {showHpaWarning && replicasOwner && (
        <div className="mt-3 rounded-sm border-l-[3px] border-yellow bg-yellow-soft px-2.5 py-1.5">
          <div className="font-mono text-[10px] font-medium text-yellow">
            {replicasOwner.display} controls replicas
          </div>
          <p className="mt-0.5 text-[11px] leading-tight text-ink">
            {replicasOwner.consequence ||
              "A controller will likely overwrite your scale on the next reconcile."}
          </p>
        </div>
      )}

      {/* Scale-to-zero chip — inline warn, doesn't block submit. The
          delete button is the right surface for irreversible
          destructive operations; scaling to 0 is reversible. */}
      {willStopAllPods && (
        <div className="mt-3 rounded-sm border-l-[3px] border-yellow bg-yellow-soft px-2.5 py-1.5">
          <div className="font-mono text-[10px] font-medium text-yellow">
            scaling to zero
          </div>
          <p className="mt-0.5 text-[11px] leading-tight text-ink">
            All pods for this {meta.kind.toLowerCase()} will be stopped.
          </p>
        </div>
      )}

      {/* Footer — cancel + apply, right-aligned */}
      <div className="mt-3 flex items-center justify-end gap-1.5">
        <button
          type="button"
          onClick={onCancel}
          className="rounded-sm border border-border-strong px-2.5 py-1 font-mono text-[11.5px] text-ink-muted transition-colors hover:border-ink-muted hover:text-ink"
        >
          cancel
        </button>
        <button
          type="button"
          onClick={submit}
          disabled={submitDisabled}
          title="Enter or ⌘/Ctrl+Enter to submit"
          className="rounded-sm bg-accent px-2.5 py-1 font-mono text-[11.5px] text-bg transition-opacity hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-40"
        >
          apply
        </button>
      </div>
    </div>
  );
}
