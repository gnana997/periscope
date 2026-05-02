// YamlEditor — inline writable YAML editor. Lives inside the YAML tab
// body when the `?edit=1` URL param is set.
//
//
// Owns:
//   - Monaco lifecycle (writable model + URI scoped to this resource)
//   - pristineRef (frozen YAML at mount; source of truth for diff +
//     minimal-patch generation)
//   - Mode (edit | diff | conflict) and applyState (idle | dryRunning |
//     applying | success | error)
//   - Apply orchestration: dry-run → apply → invalidate caches → drop
//     ?edit=1
//   - Schema lazy-loading: when useOpenAPISchema resolves, register
//     the matching schema with monaco-yaml so validation/hover/
//     autocomplete kick in
//
// Keyboard:
//   Cmd/Ctrl+Enter      → apply (dry-run then real)
//   Cmd/Ctrl+Shift+D    → toggle inline diff
//   Esc                 → cancel (when not running)

import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import * as monaco from "monaco-editor";
import { useQueryClient } from "@tanstack/react-query";
import { useSearchParams } from "react-router-dom";

import { ApiError, api, type ResourceRef } from "../../../lib/api";
import { cn } from "../../../lib/cn";
import {
  buildMonacoSchemaConfig,
  findSchemaForGVK,
  gvkFromIdentity,
  modelURIForResource,
  parseIdentityFromYaml,
} from "../../../lib/k8sSchema";
import {
  currentMonacoTheme,
  ensureMonacoConfigured,
  ensureMonacoYamlConfigured,
  registerSchema,
  useMonacoTheme,
} from "../../../lib/monacoSetup";
import { useOpenAPISchema, useYaml } from "../../../hooks/useResource";
import { usePublishEditorDirty } from "../../../hooks/useEditorDirty";
import {
  buildMinimalSSA,
  computeOps,
  MultiDocumentError,
  type Identity,
  type Op,
} from "../../../lib/yamlPatch";
import type { YamlKind } from "../../../lib/api";
import { ActionBar, type ApplyState } from "./ActionBar";
import { ProblemsStrip } from "./ProblemsStrip";
import { ApplyErrorBanner } from "./ApplyErrorBanner";
import { showToast } from "../../../lib/toastBus";
import { ConflictBanner } from "./ConflictBanner";
import { InlineDiff } from "./InlineDiff";
import { PatchPreviewDrawer } from "./PatchPreviewDrawer";
import { DetailError, DetailLoading } from "../states";

interface YamlEditorProps {
  cluster: string;
  yamlKind: YamlKind;
  resource: ResourceRef;
}

export function YamlEditor({ cluster, yamlKind, resource }: YamlEditorProps) {
  const yamlQuery = useYaml(
    cluster,
    yamlKind,
    resource.namespace ?? "",
    resource.name,
    true,
  );

  if (yamlQuery.isLoading) return <DetailLoading label="loading yaml…" />;
  if (yamlQuery.isError) {
    const err = yamlQuery.error;
    return <DetailError message={(err as Error)?.message ?? "unknown"} />;
  }
  if (!yamlQuery.data) return null;

  return (
    <Editor
      cluster={cluster}
      yamlKind={yamlKind}
      resource={resource}
      pristine={stripForEdit(yamlQuery.data)}
    />
  );
}

interface EditorProps {
  cluster: string;
  yamlKind: YamlKind;
  resource: ResourceRef;
  pristine: string;
}

function Editor({ cluster, yamlKind, resource, pristine }: EditorProps) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const editorRef = useRef<monaco.editor.IStandaloneCodeEditor | null>(null);
  const [pristineLocked] = useState(pristine);
  const abortRef = useRef<AbortController | null>(null);
  const [, setParams] = useSearchParams();

  const [currentYaml, setCurrentYaml] = useState(pristine);
  const [mode, setMode] = useState<"edit" | "diff" | "conflict">("edit");
  const [applyState, setApplyState] = useState<ApplyState>({ kind: "idle" });
  const [errorCount, setErrorCount] = useState(0);
  const [firstError, setFirstError] = useState<{ message: string; line: number } | null>(null);
  const [showPatch, setShowPatch] = useState(false);
  const [patchDrawerWidth, setPatchDrawerWidth] = useState<number>(() => {
    if (typeof window === "undefined") return 420;
    const stored = window.localStorage.getItem("periscope.patchDrawerWidth");
    const n = stored ? parseInt(stored, 10) : NaN;
    return Number.isFinite(n) && n >= 280 && n <= 800 ? n : 420;
  });
  // Persist on change. The drag handler updates state on mousemove; this
  // effect just mirrors state to localStorage so reload preserves the
  // user's preferred width.
  useEffect(() => {
    if (typeof window === "undefined") return;
    window.localStorage.setItem("periscope.patchDrawerWidth", String(patchDrawerWidth));
  }, [patchDrawerWidth]);
  // Drag-to-resize: mousedown on the handle starts a window-level drag.
  // Pixel deltas grow the drawer when the user drags LEFT (subtract clientX
  // delta from start). Cursor + select are forced on body so the cursor
  // doesn't flicker over Monaco mid-drag.
  const onPatchResizeStart = useCallback((e: React.MouseEvent) => {
    e.preventDefault();
    const startX = e.clientX;
    const startWidth = patchDrawerWidth;
    document.body.style.cursor = "col-resize";
    document.body.style.userSelect = "none";
    const onMove = (ev: MouseEvent) => {
      const dx = startX - ev.clientX;
      const next = Math.min(800, Math.max(280, startWidth + dx));
      setPatchDrawerWidth(next);
    };
    const onUp = () => {
      document.body.style.cursor = "";
      document.body.style.userSelect = "";
      window.removeEventListener("mousemove", onMove);
      window.removeEventListener("mouseup", onUp);
    };
    window.addEventListener("mousemove", onMove);
    window.addEventListener("mouseup", onUp);
  }, [patchDrawerWidth]);


  const dirty = currentYaml !== pristineLocked;

  // Compute identity once from the pristine buffer; identity-from-edited
  // would race the user mid-keystroke. Apply uses the resource prop's
  // group/version/resource for routing — identity is for schema lookup.
  const identity = useMemo<Identity | null>(
    () => parseIdentityFromYaml(pristine),
    [pristine],
  );
  const gvk = useMemo(
    () => (identity ? gvkFromIdentity(identity) : null),
    [identity],
  );

  // Compute ops lazily — only when the user clicks apply or shows the
  // patch drawer. Per-keystroke parsing would be wasted work.
  const opsForCurrentBuffer = useCallback((): Op[] => {
    try {
      return computeOps(pristineLocked, currentYaml);
    } catch {
      return [];
    }
  }, [currentYaml, pristineLocked]);

  // Cache the ops for drawer rendering (cheap; under ~50 ops typical).
  const ops = useMemo(() => {
    if (!dirty) return [];
    return opsForCurrentBuffer();
  }, [dirty, opsForCurrentBuffer]);

  // Publish dirty bit so the Tab strip can show `yaml*`.
  usePublishEditorDirty(cluster, yamlKind, resource.namespace, resource.name, dirty);

  useMonacoTheme();

  // Schema lazy-load. enabled gated on identity being parseable.
  const schemaQuery = useOpenAPISchema(
    cluster,
    gvk?.group ?? "",
    gvk?.version ?? "",
    Boolean(gvk),
  );

  // Editor mount — create model with cluster-scoped URI so monaco-yaml's
  // fileMatch can route validation correctly when the schema arrives.
  useEffect(() => {
    if (!containerRef.current || !gvk) return;

    ensureMonacoConfigured();
    ensureMonacoYamlConfigured();

    const modelURI = modelURIForResource({
      cluster,
      group: gvk.group,
      version: gvk.version,
      kind: gvk.kind,
      namespace: resource.namespace,
      name: resource.name,
    });
    const uri = monaco.Uri.parse(modelURI);
    // Re-use existing model if React StrictMode double-mounts before
    // dispose runs. Monaco rejects createModel on duplicate URIs.
    const existing = monaco.editor.getModel(uri);
    if (existing) existing.dispose();
    const model = monaco.editor.createModel(pristine, "yaml", uri);

    const editor = monaco.editor.create(containerRef.current, {
      model,
      theme: currentMonacoTheme(),
      readOnly: false,
      automaticLayout: true,
      fontFamily: '"Geist Mono Variable", ui-monospace, "SF Mono", Menlo, monospace',
      fontSize: 12.5,
      fontLigatures: true,
      lineHeight: 19,
      minimap: { enabled: false },
      scrollBeyondLastLine: false,
      smoothScrolling: true,
      cursorBlinking: "smooth",
      cursorSmoothCaretAnimation: "on",
      renderLineHighlight: "all",
      renderWhitespace: "selection",
      glyphMargin: false, // Phase 2 turns this on for ownership badges
      folding: true,
      foldingStrategy: "indentation",
      showFoldingControls: "mouseover",
      bracketPairColorization: { enabled: false },
      guides: {
        indentation: true,
        highlightActiveIndentation: true,
        bracketPairs: false,
      },
      scrollbar: {
        vertical: "auto",
        horizontal: "auto",
        verticalScrollbarSize: 10,
        horizontalScrollbarSize: 10,
      },
      padding: { top: 10, bottom: 10 },
      stickyScroll: { enabled: true, maxLineCount: 4 },
      unicodeHighlight: { ambiguousCharacters: false },
    });
    editorRef.current = editor;
    editor.focus();

    const contentSub = editor.onDidChangeModelContent(() => {
      setCurrentYaml(editor.getValue());
    });

    const markersSub = monaco.editor.onDidChangeMarkers((uris) => {
      if (!uris.some((u) => u.toString() === uri.toString())) return;
      const marks = monaco.editor.getModelMarkers({ resource: uri });
      const errs = marks.filter((m) => m.severity >= monaco.MarkerSeverity.Warning);
      setErrorCount(errs.length);
      const sorted = [...errs].sort((a, b) => a.startLineNumber - b.startLineNumber);
      const first = sorted[0];
      setFirstError(first ? { message: first.message, line: first.startLineNumber } : null);
    });

    return () => {
      contentSub.dispose();
      markersSub.dispose();
      editor.getModel()?.dispose();
      editor.dispose();
      editorRef.current = null;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Wire schema into monaco-yaml when both editor + schema are ready.
  useEffect(() => {
    if (!schemaQuery.data || !gvk) return;
    const modelURI = modelURIForResource({
      cluster,
      group: gvk.group,
      version: gvk.version,
      kind: gvk.kind,
      namespace: resource.namespace,
      name: resource.name,
    });
    const config = buildMonacoSchemaConfig(schemaQuery.data, gvk, modelURI);
    if (!config) return; // CRD without bundled schema — graceful degrade
    registerSchema(config);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [schemaQuery.data, gvk?.group, gvk?.version, gvk?.kind]);

  // Schema status — drives the ActionBar pill. "loading" while fetching,
  // "loaded" once registered with monaco-yaml, "missing" for CRDs whose
  // GVK isn't in the bundled schema (graceful degrade), "failed" on
  // network/RBAC errors. Match the editor only renders this strip when
  // we can actually attempt a schema lookup (i.e. identity parsed).
  const schemaState: "loading" | "loaded" | "missing" | "failed" = !gvk
    ? "loading"
    : schemaQuery.isError
      ? "failed"
      : !schemaQuery.data
        ? "loading"
        : findSchemaForGVK(schemaQuery.data, gvk)
          ? "loaded"
          : "missing";

  // Detect destructive edits live (multi-doc paste, identity edits)
  // and toast the user. Identity changes are server-rejected anyway,
  // but catching them inline saves a round-trip. Refs guarantee a
  // single toast per transition into the bad state — no spam on every
  // keystroke afterwards.
  const prevMultiDoc = useRef(false);
  const prevDriftKey = useRef<string>("");
  useEffect(() => {
    if (currentYaml === pristine) {
      prevMultiDoc.current = false;
      prevDriftKey.current = "";
      return;
    }
    const isMulti = /\n---\s*\n/.test(currentYaml);
    if (isMulti && !prevMultiDoc.current) {
      showToast("multi-document YAML isn't supported — keep one resource per editor", "warn");
    }
    prevMultiDoc.current = isMulti;
    if (isMulti) return;
    const before = parseIdentityFromYaml(pristine);
    const after = parseIdentityFromYaml(currentYaml);
    if (!before || !after) return;
    const drifted: string[] = [];
    if (before.apiVersion !== after.apiVersion) drifted.push("apiVersion");
    if (before.kind !== after.kind) drifted.push("kind");
    if (before.name !== after.name) drifted.push("metadata.name");
    if ((before.namespace ?? "") !== (after.namespace ?? "")) drifted.push("metadata.namespace");
    const driftKey = drifted.join("|");
    if (driftKey !== prevDriftKey.current) {
      prevDriftKey.current = driftKey;
      if (drifted.length > 0) {
        showToast(`don't change ${drifted.join(", ")} — the apiserver will reject this apply`, "warn");
      }
    }
  }, [currentYaml, pristine]);


  // Apply orchestration. force=false on the first attempt; ConflictBanner
  // calls back with force=true when the user opts in.
  const qc = useQueryClient();
  const runApply = useCallback(
    async (force: boolean) => {
      if (!identity) return;
      const ops = opsForCurrentBuffer();
      if (ops.length === 0) return;

      let body: string;
      try {
        body = buildMinimalSSA(ops, identity);
      } catch (e) {
        if (e instanceof MultiDocumentError) {
          setApplyState({ kind: "error", message: e.message });
          return;
        }
        throw e;
      }

      // Cancel any prior in-flight apply
      abortRef.current?.abort();
      const ac = new AbortController();
      abortRef.current = ac;

      const args = {
        cluster: resource.cluster,
        group: resource.group,
        version: resource.version,
        resource: resource.resource,
        namespace: resource.namespace,
        name: resource.name,
        yaml: body,
      };

      try {
        setApplyState({ kind: "dryRunning" });
        await api.applyResource({ ...args, dryRun: true, force }, ac.signal);
        setApplyState({ kind: "applying" });
        await api.applyResource({ ...args, dryRun: false, force }, ac.signal);
        setApplyState({ kind: "success" });

        // Invalidate caches so list/detail/events/yaml/meta all refetch.
        qc.invalidateQueries({ queryKey: [yamlKind] });
        qc.invalidateQueries({
          queryKey: ["yaml", cluster, yamlKind, resource.namespace ?? "", resource.name],
        });
        qc.invalidateQueries({
          queryKey: [`${singularize(yamlKind)}-detail`, cluster, resource.namespace ?? "", resource.name],
        });
        qc.invalidateQueries({
          queryKey: ["events", cluster, yamlKind, resource.namespace ?? "", resource.name],
        });
        qc.invalidateQueries({
          queryKey: ["meta", cluster, resource.group, resource.version, resource.resource, resource.namespace ?? "", resource.name],
        });

        // Drop ?edit=1 → unmount → YamlReadView takes over
        setTimeout(() => {
          setParams((prev) => {
            const next = new URLSearchParams(prev);
            next.delete("edit");
            return next;
          }, { replace: true });
        }, 400);
      } catch (e) {
        if (ac.signal.aborted) return;
        const apiErr = e instanceof ApiError ? e : null;
        const status = apiErr?.status;
        const message = apiErr?.bodyText || (e as Error)?.message || "apply failed";
        setApplyState({ kind: "error", message });
        if (status === 409) setMode("conflict");
      }
    },
    [identity, opsForCurrentBuffer, resource, cluster, yamlKind, qc, setParams],
  );

  // Cancel — drops ?edit=1 (and aborts any running apply)
  const onCancel = useCallback(() => {
    abortRef.current?.abort();
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        next.delete("edit");
        return next;
      },
      { replace: true },
    );
  }, [setParams]);

  // Keyboard shortcuts on the editor instance (Monaco's preferred
  // mechanism — captures inside the editor surface).
  useEffect(() => {
    const editor = editorRef.current;
    if (!editor) return;
    const cmdEnter = editor.addCommand(
      monaco.KeyMod.CtrlCmd | monaco.KeyCode.Enter,
      () => {
        void runApply(false);
      },
    );
    const cmdShiftD = editor.addCommand(
      monaco.KeyMod.CtrlCmd | monaco.KeyMod.Shift | monaco.KeyCode.KeyD,
      () => {
        setMode((m) => (m === "diff" ? "edit" : "diff"));
      },
    );
    return () => {
      // Monaco's addCommand returns a string ID — there's no public
      // remove API. The editor disposal in the mount effect cleans up.
      void cmdEnter;
      void cmdShiftD;
    };
  }, [runApply]);

  // Esc handler at the window level (Monaco's editor consumes Esc only
  // when widgets are open).
  useEffect(() => {
    function onKey(e: globalThis.KeyboardEvent) {
      if (e.key === "Escape" && applyState.kind !== "applying" && applyState.kind !== "dryRunning") {
        if (showPatch) {
          setShowPatch(false);
          return;
        }
        if (mode === "diff" || mode === "conflict") {
          setMode("edit");
          return;
        }
        onCancel();
      }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [applyState.kind, mode, showPatch, onCancel]);

  const onJumpToError = useCallback(() => {
    if (errorCount === 0) return;
    const editor = editorRef.current;
    if (!editor) return;
    const model = editor.getModel();
    if (!model) return;
    const marks = monaco.editor.getModelMarkers({ resource: model.uri });
    const first = marks.find((m) => m.severity >= monaco.MarkerSeverity.Warning);
    if (first) {
      editor.revealLineInCenter(first.startLineNumber);
      editor.setPosition({ lineNumber: first.startLineNumber, column: first.startColumn });
      editor.focus();
    }
  }, [errorCount]);

  const schemaLabel = gvk
    ? `${gvk.group ? gvk.group + "/" : ""}${gvk.version} ${gvk.kind}`
    : undefined;

  return (
    <div className="flex h-full min-h-0 min-w-0 flex-col">
      {mode === "conflict" && applyState.kind === "error" && (
        <ConflictBanner
          message={applyState.message}
          onForce={() => {
            setMode("edit");
            void runApply(true);
          }}
          onCancel={() => {
            setMode("edit");
            setApplyState({ kind: "idle" });
          }}
        />
      )}

      <div className="relative flex min-h-0 min-w-0 flex-1">
        <div className={cn("relative min-h-0 min-w-0 flex-1", mode === "diff" && "hidden")}>
          <div ref={containerRef} className="h-full min-h-0" />
        </div>
        {mode === "diff" && (
          <div className="min-h-0 flex-1">
            <InlineDiff original={pristineLocked} proposed={currentYaml} />
          </div>
        )}
        {showPatch && (
          <>
            <div
              className="w-1 cursor-col-resize bg-border hover:bg-accent"
              onMouseDown={onPatchResizeStart}
              role="separator"
              aria-orientation="vertical"
            />
            <PatchPreviewDrawer
              width={patchDrawerWidth}
              ops={ops}
              identity={identity}
              cluster={resource.cluster}
              group={resource.group}
              version={resource.version}
              resource={resource.resource}
              namespace={resource.namespace}
              name={resource.name}
              onClose={() => setShowPatch(false)}
            />
          </>
        )}
      </div>

      {applyState.kind === "error" && mode !== "conflict" && (
        <ApplyErrorBanner
          message={applyState.message}
          onDismiss={() => setApplyState({ kind: "idle" })}
        />
      )}

      <ProblemsStrip
        errorCount={errorCount}
        firstError={firstError}
        onJump={onJumpToError}
      />
      <ActionBar
        mode={mode}
        opsCount={ops.length}
        errorCount={errorCount}
        dirty={dirty}
        applyState={applyState}
        schemaLabel={schemaLabel}
        schemaState={schemaState}
        onCancel={onCancel}
        onTogglePatch={() => setShowPatch((s) => !s)}
        onDryRun={async () => {
          if (!identity) return;
          const ops = opsForCurrentBuffer();
          if (ops.length === 0) return;
          let body: string;
          try {
            body = buildMinimalSSA(ops, identity);
          } catch (e) {
            setApplyState({ kind: "error", message: (e as Error).message });
            return;
          }
          abortRef.current?.abort();
          const ac = new AbortController();
          abortRef.current = ac;
          try {
            setApplyState({ kind: "dryRunning" });
            await api.applyResource(
              {
                cluster: resource.cluster,
                group: resource.group,
                version: resource.version,
                resource: resource.resource,
                namespace: resource.namespace,
                name: resource.name,
                yaml: body,
                dryRun: true,
              },
              ac.signal,
            );
            setApplyState({ kind: "success" });
            setTimeout(() => setApplyState({ kind: "idle" }), 1500);
          } catch (e) {
            if (ac.signal.aborted) return;
            const apiErr = e instanceof ApiError ? e : null;
            setApplyState({
              kind: "error",
              message: apiErr?.bodyText || (e as Error)?.message || "dry-run failed",
            });
          }
        }}
        onToggleDiff={() => setMode((m) => (m === "diff" ? "edit" : "diff"))}
        onApply={() => void runApply(false)}
        onJumpToError={onJumpToError}
      />
    </div>
  );
}

/* ============================================================
   stripForEdit — trim server-managed sub-blocks from the YAML
   before it becomes the pristine buffer. yamlPatch.computeOps
   also strips metadata at diff time; this is purely a *display*
   concern (don't show the user a wall of `managedFields:`).
   ============================================================ */
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

// Map plural → singular for invalidating *-detail cache keys on apply
// success. Mirrors the convention in useResource.ts.
function singularize(kind: string): string {
  if (kind.endsWith("s")) return kind.slice(0, -1);
  return kind;
}
