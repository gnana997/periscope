// EditResourceModal — generic server-side-apply YAML editor.
//
// One component drives every editable surface in the SPA. Caller
// supplies the resource ref + the current YAML (typically loaded via
// the existing /yaml endpoints); the modal handles editing, dry-run
// preview, the apply roundtrip, and the 409 force-resolve flow.
//
// Editing intentionally uses a styled <textarea> rather than Monaco —
// without an OpenAPI schema (deferred to a follow-up PR), Monaco's
// chrome doesn't earn its 700KB cost. The dry-run roundtrip surfaces
// real apiserver + admission webhook errors in a panel below the
// editor, which is the same feedback Monaco-with-schema would give
// (just delivered server-side).
//
// On 409 Conflict the modal pivots to a "force apply?" confirmation
// row inline. The user opted in once, so we send Force=true on retry.
// The K8s server-side apply contract makes this the safe, granular
// way to resolve field-manager conflicts — no full-resource rewrite,
// no resourceVersion races.

import { useEffect, useRef, useState, type KeyboardEvent } from "react";
import { ApiError, api, type ApplyResult } from "../../lib/api";
import { cn } from "../../lib/cn";

export interface ResourceRef {
  cluster: string;
  group: string; // "" for core (Pod, Service, ConfigMap, Secret, …)
  version: string; // "v1", "apps/v1" already split → version = "v1"
  resource: string; // plural URL segment, e.g. "pods", "deployments"
  namespace?: string;
  name: string;
  // Optional human label, used in the modal title. Defaults to
  // "Kind name". Helps when the same modal is reused across types.
  kind?: string;
}

interface EditResourceModalProps {
  resourceRef: ResourceRef;
  initialYaml: string;
  onClose: () => void;
  onApplied?: (result: ApplyResult) => void;
}

type ApplyState =
  | { kind: "idle" }
  | { kind: "running"; phase: "dryRun" | "apply" }
  | { kind: "success"; result: ApplyResult }
  | { kind: "error"; message: string; status?: number; canForce: boolean };

export function EditResourceModal({
  resourceRef,
  initialYaml,
  onClose,
  onApplied,
}: EditResourceModalProps) {
  const stripped = useRef(stripForEdit(initialYaml));
  const [yaml, setYaml] = useState(stripped.current);
  const [state, setState] = useState<ApplyState>({ kind: "idle" });
  const taRef = useRef<HTMLTextAreaElement | null>(null);

  const dirty = yaml !== stripped.current;
  const title = resourceRef.kind
    ? `${resourceRef.kind.toLowerCase()} ${resourceRef.name}`
    : resourceRef.name;

  useEffect(() => {
    function onKey(e: globalThis.KeyboardEvent) {
      if (e.key === "Escape" && state.kind !== "running") onClose();
      // ⌘/Ctrl+Enter submits. Only when the editor is the active element
      // so other modals stacked on top aren't disturbed.
      if (
        (e.metaKey || e.ctrlKey) &&
        e.key === "Enter" &&
        document.activeElement === taRef.current
      ) {
        e.preventDefault();
        if (dirty && state.kind !== "running") void runApply();
      }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [onClose, state.kind, dirty, yaml]);

  async function run(phase: "dryRun" | "apply", force = false) {
    setState({ kind: "running", phase });
    try {
      const result = await api.applyResource({
        cluster: resourceRef.cluster,
        group: resourceRef.group,
        version: resourceRef.version,
        resource: resourceRef.resource,
        namespace: resourceRef.namespace,
        name: resourceRef.name,
        yaml,
        dryRun: phase === "dryRun",
        force,
      });
      setState({ kind: "success", result });
      if (phase === "apply") onApplied?.(result);
    } catch (err) {
      const apiErr = err instanceof ApiError ? err : null;
      const status = apiErr?.status;
      const canForce = status === 409;
      const message =
        apiErr?.bodyText?.trim() ||
        (err as Error)?.message ||
        "apply failed";
      setState({ kind: "error", message, status, canForce });
    }
  }
  const runDryRun = () => run("dryRun");
  const runApply = () => run("apply");
  const runForceApply = () => run("apply", true);

  function handleKey(e: KeyboardEvent<HTMLTextAreaElement>) {
    const ta = e.currentTarget;
    if (e.key === "Tab") {
      e.preventDefault();
      const start = ta.selectionStart;
      const end = ta.selectionEnd;
      const next = yaml.slice(0, start) + "  " + yaml.slice(end);
      setYaml(next);
      // Restore caret after the inserted spaces. setTimeout because
      // React reconciles on the next tick.
      setTimeout(() => {
        if (taRef.current) {
          taRef.current.selectionStart = start + 2;
          taRef.current.selectionEnd = start + 2;
        }
      }, 0);
      return;
    }
    if (e.key === "Enter" && !e.shiftKey && !e.metaKey && !e.ctrlKey) {
      const start = ta.selectionStart;
      const lineStart = yaml.lastIndexOf("\n", start - 1) + 1;
      const linePrefix = yaml.slice(lineStart, start).match(/^\s*/)?.[0] ?? "";
      if (linePrefix.length === 0) return;
      e.preventDefault();
      const insert = "\n" + linePrefix;
      const next = yaml.slice(0, start) + insert + yaml.slice(ta.selectionEnd);
      setYaml(next);
      setTimeout(() => {
        if (taRef.current) {
          const pos = start + insert.length;
          taRef.current.selectionStart = pos;
          taRef.current.selectionEnd = pos;
        }
      }, 0);
    }
  }

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 px-4 py-8"
      role="dialog"
      aria-modal="true"
      aria-labelledby="edit-resource-title"
      onClick={(e) => {
        if (e.target === e.currentTarget && state.kind !== "running") onClose();
      }}
    >
      <div className="flex h-full max-h-[90vh] w-full max-w-4xl flex-col rounded-md border border-border-strong bg-surface shadow-2xl">
        {/* Header */}
        <div className="flex items-center justify-between border-b border-border px-5 py-3">
          <div className="flex items-center gap-2 font-mono text-sm">
            <span className="text-ink-faint">edit</span>
            <span id="edit-resource-title" className="text-ink">
              {title}
            </span>
            {resourceRef.namespace && (
              <span className="text-ink-faint">· {resourceRef.namespace}</span>
            )}
            <span className="text-ink-faint">· {resourceRef.cluster}</span>
          </div>
          <button
            type="button"
            onClick={onClose}
            disabled={state.kind === "running"}
            className="rounded-sm px-2 py-1 text-ink-faint transition-colors hover:bg-surface-2 hover:text-ink disabled:opacity-50"
            aria-label="Close"
          >
            ✕
          </button>
        </div>

        {/* Editor */}
        <div className="flex min-h-0 flex-1 flex-col">
          <textarea
            ref={taRef}
            value={yaml}
            onChange={(e) => setYaml(e.target.value)}
            onKeyDown={handleKey}
            spellCheck={false}
            disabled={state.kind === "running"}
            className={cn(
              "flex-1 resize-none border-0 bg-bg p-4 font-mono text-[12.5px] leading-[1.55] text-ink",
              "outline-none focus:ring-0",
            )}
            aria-label="YAML editor"
          />
          <ResultPanel state={state} />
        </div>

        {/* Footer */}
        <div className="flex items-center justify-between border-t border-border px-5 py-3">
          <div className="font-mono text-xs text-ink-faint">
            {dirty ? "modified" : "no changes"}
            {" · "}
            <kbd className="rounded-sm border border-border-strong bg-surface-2 px-1.5 py-0.5">
              ⌘/Ctrl+Enter
            </kbd>{" "}
            apply
          </div>
          <div className="flex items-center gap-2">
            <button
              type="button"
              onClick={runDryRun}
              disabled={state.kind === "running" || !dirty}
              className="rounded-sm border border-border-strong px-3 py-1.5 font-mono text-sm text-ink-muted transition-colors hover:border-ink-muted hover:text-ink disabled:cursor-not-allowed disabled:opacity-50"
            >
              dry-run
            </button>
            <ApplyButton
              state={state}
              dirty={dirty}
              onApply={runApply}
              onForce={runForceApply}
            />
          </div>
        </div>
      </div>
    </div>
  );
}

function ApplyButton({
  state,
  dirty,
  onApply,
  onForce,
}: {
  state: ApplyState;
  dirty: boolean;
  onApply: () => void;
  onForce: () => void;
}) {
  if (state.kind === "running") {
    return (
      <button
        type="button"
        disabled
        className="rounded-sm bg-accent-soft px-3 py-1.5 font-mono text-sm text-accent opacity-70"
      >
        {state.phase === "apply" ? "applying…" : "checking…"}
      </button>
    );
  }
  if (state.kind === "error" && state.canForce) {
    return (
      <button
        type="button"
        onClick={onForce}
        className="rounded-sm border border-red bg-red-soft px-3 py-1.5 font-mono text-sm text-red transition-colors hover:bg-red hover:text-bg"
      >
        force apply
      </button>
    );
  }
  return (
    <button
      type="button"
      onClick={onApply}
      disabled={!dirty}
      className="rounded-sm bg-accent px-3 py-1.5 font-mono text-sm text-bg transition-opacity hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-40"
    >
      apply
    </button>
  );
}

function ResultPanel({ state }: { state: ApplyState }) {
  if (state.kind === "idle" || state.kind === "running") return null;
  if (state.kind === "success") {
    return (
      <div className="border-t border-border bg-green-soft px-5 py-3 font-mono text-xs text-green">
        {state.result.dryRun
          ? "dry-run ok — no validation errors. apply when ready."
          : "applied successfully."}
      </div>
    );
  }
  return (
    <div className="max-h-[30%] overflow-y-auto border-t border-red bg-red-soft px-5 py-3 font-mono text-xs text-red">
      <div className="mb-1 font-semibold">
        {state.canForce
          ? "field-manager conflict"
          : state.status
            ? `error ${state.status}`
            : "error"}
      </div>
      <pre className="whitespace-pre-wrap">{state.message}</pre>
      {state.canForce && (
        <div className="mt-2 text-ink-muted">
          another field manager (likely a controller) owns one or more of
          the fields you changed. clicking{" "}
          <span className="text-red">force apply</span> takes ownership for
          periscope-spa.
        </div>
      )}
    </div>
  );
}

// stripForEdit removes the noise that doesn't belong in an editor view:
// status (server-side projection), managedFields (server-side apply
// provenance), and the resourceVersion / uid / generation that the
// server controls. The user pastes the trimmed view back; the server
// ignores stripped fields anyway, but seeing them in the editor invites
// accidental edits. The backend's L2 layer also strips these on input
// so this is purely UX.
function stripForEdit(yaml: string): string {
  if (!yaml.includes("managedFields:") && !yaml.includes("status:")) {
    return yaml;
  }
  const lines = yaml.split("\n");
  const out: string[] = [];
  let skipUntilDedentTo: number | null = null;
  for (const line of lines) {
    const indent = line.search(/\S/);
    if (skipUntilDedentTo !== null) {
      if (indent === -1 || indent > skipUntilDedentTo) continue;
      skipUntilDedentTo = null;
    }
    const trimmed = line.trimStart();
    if (
      trimmed.startsWith("managedFields:") ||
      trimmed.startsWith("status:") ||
      trimmed.startsWith("resourceVersion:") ||
      trimmed.startsWith("uid:") ||
      trimmed.startsWith("generation:") ||
      trimmed.startsWith("creationTimestamp:")
    ) {
      const isBlock = !trimmed.includes(":") || /:\s*$/.test(trimmed);
      if (isBlock) {
        skipUntilDedentTo = indent;
      }
      continue;
    }
    out.push(line);
  }
  return out.join("\n");
}
