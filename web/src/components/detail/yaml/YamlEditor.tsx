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

import { ApiError, api, type ResourceMeta, type ResourceRef } from "../../../lib/api";
import {
  type EditorSource,
  dirtyChannelKey,
  invalidateAfterApply,
  invalidateEditorYaml,
} from "../../../lib/customResources";
import { cn } from "../../../lib/cn";
import {
  buildMonacoSchemaConfig,
  findSchemaForGVK,
  gvkFromIdentity,
  modelURIForResource,
  parseIdentityFromYaml,
} from "../../../lib/k8sSchema";
import { describeDrift } from "../../../lib/drift";
import { stripForEdit } from "../../../lib/stripForEdit";
import { classifyManager } from "../../../lib/managers";
import {
  parseManagedFields,
  pathToManager,
  normalizeStatusFieldPath,
} from "../../../lib/managedFields";
import { pathForLine } from "../../../lib/yamlPath";
import {
  currentMonacoTheme,
  ensureMonacoConfigured,
  ensureMonacoYamlConfigured,
  registerSchema,
  useMonacoTheme,
} from "../../../lib/monacoSetup";
import { useOpenAPISchema, useResourceMeta, useEditorYaml } from "../../../hooks/useResource";
import { usePublishEditorDirty } from "../../../hooks/useEditorDirty";
import {
  buildMinimalSSA,
  computeOps,
  MultiDocumentError,
  type Identity,
  type Op,
} from "../../../lib/yamlPatch";
import { ActionBar, type ApplyState } from "./ActionBar";
import { ProblemsStrip } from "./ProblemsStrip";
import { ApplyErrorBanner } from "./ApplyErrorBanner";
import { DriftBanner } from "./DriftBanner";
import { DriftDiffOverlay } from "./DriftDiffOverlay";
import { showToast } from "../../../lib/toastBus";
import { ConflictResolutionView, type FieldConflict, type Resolution } from "./ConflictResolutionView";
import { TakeoverDialog } from "./TakeoverDialog";
import { InlineDiff } from "./InlineDiff";
import { PatchPreviewDrawer } from "./PatchPreviewDrawer";
import { DetailError, DetailLoading } from "../states";

interface YamlEditorProps {
  cluster: string;
  source: EditorSource;
  resource: ResourceRef;
}

export function YamlEditor({ cluster, source, resource }: YamlEditorProps) {
  const yamlQuery = useEditorYaml(
    source,
    cluster,
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
      source={source}
      resource={resource}
      pristine={stripForEdit(yamlQuery.data)}
    />
  );
}

interface EditorProps {
  cluster: string;
  source: EditorSource;
  resource: ResourceRef;
  pristine: string;
}

function Editor({ cluster, source, resource, pristine }: EditorProps) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const editorRef = useRef<monaco.editor.IStandaloneCodeEditor | null>(null);
  const [pristineLocked, setPristineLocked] = useState(pristine);
  const abortRef = useRef<AbortController | null>(null);
  const [, setParams] = useSearchParams();

  const [currentYaml, setCurrentYaml] = useState(pristine);
  const [mode, setMode] = useState<"edit" | "diff" | "conflict">("edit");
  const [applyState, setApplyState] = useState<ApplyState>({ kind: "idle" });
  const [errorCount, setErrorCount] = useState(0);
  const [firstError, setFirstError] = useState<{ message: string; line: number } | null>(null);
  const [showPatch, setShowPatch] = useState(false);
  const [conflicts, setConflicts] = useState<FieldConflict[]>([]);
  const [resolutions, setResolutions] = useState<Map<string, Resolution>>(new Map());
  const [showTakeover, setShowTakeover] = useState(false);
  const ownerDecorationsRef = useRef<string[]>([]);
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
  usePublishEditorDirty(cluster, dirtyChannelKey(source), resource.namespace, resource.name, dirty);

  useMonacoTheme();

  // Schema lazy-load. enabled gated on identity being parseable.
  const schemaQuery = useOpenAPISchema(
    cluster,
    gvk?.group ?? "",
    gvk?.version ?? "",
    Boolean(gvk),
  );

  // Resource metadata (managedFields + resourceVersion). Drives the
  // owner-glyph margin and Phase 3 drift detection. Polls every 15s
  // (configured in useResourceMeta).
  const metaQuery = useResourceMeta(
    cluster,
    {
      group: resource.group,
      version: resource.version,
      resource: resource.resource,
      namespace: resource.namespace,
      name: resource.name,
    },
    true,
  );

  const qc = useQueryClient();

  // ----- Drift detection (Phase 3) -----
  // pristineMeta is the meta the user mounted against. As metaQuery
  // polls fresh data every 15s, describeDrift compares the two to
  // spot real spec writes by other actors. Clean buffer → silently
  // swap pristine YAML (no banner); dirty buffer → render
  // <DriftBanner> with reload/dismiss/show-diff actions.
  //
  // State (rather than ref) so it can be read during render to
  // compute the `drift` memo that gates the banner. Updated on three
  // events: first arrival, post-clean-refresh, and explicit reload.
  //
  // dismissedAtRV: the resourceVersion the user last clicked dismiss
  // on. Drift is suppressed while metaQuery's rv equals this; a newer
  // rv (i.e. fresh drift) re-shows the banner.
  const [pristineMeta, setPristineMeta] = useState<ResourceMeta | null>(null);
  const [dismissedAtRV, setDismissedAtRV] = useState<string | null>(null);

  // Capture pristine on first arrival. Setting state in an effect is
  // the textbook "sync external (server) value into local state" use
  // case the rule's docs accept; we cannot derive pristineMeta from
  // metaQuery.data because pristine has to *persist* across polls.
  useEffect(() => {
    if (!metaQuery.data || pristineMeta) return;
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setPristineMeta(metaQuery.data);
  }, [metaQuery.data, pristineMeta]);

  const drift = useMemo(() => {
    if (!metaQuery.data || !pristineMeta) return null;
    if (
      dismissedAtRV !== null &&
      metaQuery.data.resourceVersion === dismissedAtRV
    ) {
      return null;
    }
    return describeDrift(pristineMeta, metaQuery.data);
  }, [metaQuery.data, pristineMeta, dismissedAtRV]);

  // Clean-buffer auto-refresh. Forwards pristineMeta to the polled
  // value so the next describeDrift doesn't fire on the same write,
  // then invalidates the YAML query. The pristine-swap effect below
  // picks up the fresh data and updates editor + cursor.
  //
  // Same justification as the capture effect above: pristineMeta is
  // server-derived state that has to persist across polls.
  useEffect(() => {
    if (!drift || dirty || !metaQuery.data) return;
    console.debug(
      `[periscope] drift refresh (clean): ${drift.manager} (${drift.category}) touched ${drift.paths.length} field(s)`,
      drift.paths.slice(0, 3),
    );
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setPristineMeta(metaQuery.data);
    invalidateEditorYaml(qc, source, cluster, resource.namespace ?? "", resource.name);
  }, [drift, dirty, metaQuery.data, qc, source, cluster, resource.namespace, resource.name]);

  const onDriftDismiss = useCallback(() => {
    if (!metaQuery.data) return;
    setDismissedAtRV(metaQuery.data.resourceVersion);
  }, [metaQuery.data]);

  const onDriftReload = useCallback(() => {
    if (dirty) {
      const ok = window.confirm(
        "Discard your unsaved edits and load the latest cluster state?",
      );
      if (!ok) return;
    }
    // Clear the dirty buffer so the pristine-swap effect (below) will
    // accept the new pristine when yamlQuery refetches.
    const editor = editorRef.current;
    if (editor) editor.setValue(pristineLocked);
    setCurrentYaml(pristineLocked);
    if (metaQuery.data) setPristineMeta(metaQuery.data);
    setDismissedAtRV(null);
    invalidateEditorYaml(qc, source, cluster, resource.namespace ?? "", resource.name);
  }, [
    dirty,
    pristineLocked,
    metaQuery.data,
    qc,
    source,
    cluster,
    resource.namespace,
    resource.name,
  ]);

  const [showDriftDiff, setShowDriftDiff] = useState(false);
  const onDriftShowDiff = useCallback(() => {
    setShowDriftDiff(true);
  }, []);
  const onDriftDiffClose = useCallback(() => {
    setShowDriftDiff(false);
  }, []);

  // Pristine swap: when yamlQuery refetches after a drift-triggered
  // invalidate (or any other cause) AND the buffer is clean, swap the
  // editor's pristine baseline + Monaco model contents to the new
  // YAML. Cursor position is preserved across the swap so the user
  // doesn't visually jump.
  //
  // The setState-in-effect rule is correct in spirit, but this is the
  // textbook "sync external change to internal state" case the rule's
  // own docs accept: the prop `pristine` came from a server refetch
  // we triggered, and the component has to drop its frozen baseline
  // to match. There is no derive-during-render alternative since the
  // baseline must persist across re-renders to drive `dirty`.
  useEffect(() => {
    if (dirty) return;
    if (pristine === pristineLocked) return;
    const editor = editorRef.current;
    const pos = editor?.getPosition() ?? null;
    /* eslint-disable react-hooks/set-state-in-effect */
    setPristineLocked(pristine);
    setCurrentYaml(pristine);
    /* eslint-enable react-hooks/set-state-in-effect */
    editor?.setValue(pristine);
    if (metaQuery.data) setPristineMeta(metaQuery.data);
    if (pos && editor) {
      // Restore on the next paint after Monaco re-renders the model.
      queueMicrotask(() => editor.setPosition(pos));
    }
  }, [pristine, dirty, pristineLocked, metaQuery.data]);

  // ----- Conflict / glyph helpers -----

  // opPathToString matches the dotted form pathForLine emits:
  //   spec.containers[name=nginx].image
  // (no leading dot, brackets glued to parent).
  const opPathToString = useCallback((segments: Op["path"]): string => {
    const parts: string[] = [];
    for (const seg of segments) {
      if (typeof seg === "string") parts.push(seg);
      else if ("idx" in seg) parts.push(`[${seg.idx}]`);
      else {
        const [k, v] = Object.entries(seg)[0];
        parts.push(`[${k}=${v}]`);
      }
    }
    return parts.join(".").replace(/\.\[/g, "[");
  }, []);

  // parseConflictCauses extracts field-level conflicts from a 409
  // Status response. Apiserver shape:
  //   { details: { causes: [{ reason, message: 'conflict with "X" using ...', field: ".spec.replicas" }] } }
  const parseConflictCauses = useCallback(
    (bodyText: string | undefined, currentOps: Op[]): FieldConflict[] => {
      if (!bodyText) return [];
      let parsed: unknown;
      try {
        parsed = JSON.parse(bodyText);
      } catch {
        return [];
      }
      const status = parsed as { details?: { causes?: Array<{ reason?: string; message?: string; field?: string }> } };
      const causes = status?.details?.causes;
      if (!Array.isArray(causes)) return [];

      const out: FieldConflict[] = [];
      for (const cause of causes) {
        if (cause.reason !== "FieldManagerConflict") continue;
        const path = normalizeStatusFieldPath(cause.field ?? "");
        const m = (cause.message ?? "").match(/conflict with "([^"]+)"/);
        const manager = m ? m[1] : "unknown";
        // Pull "mine" value from the current ops list if we have it.
        let mine: string | undefined;
        for (const op of currentOps) {
          if (opPathToString(op.path) === path && (op.op === "replace" || op.op === "add")) {
            mine = String((op as { value: unknown }).value);
            break;
          }
        }
        out.push({ path, manager, mine });
      }
      return out;
    },
    [opPathToString],
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
      glyphMargin: true,
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

    // Fire once if the *pristine* (unedited) buffer has validation
    // markers — almost always indicates a strip/schema mismatch on
    // our side rather than user error. Surfaces in DevTools so we can
    // diagnose "I opened the editor and it already shows errors"
    // reports without the user having to dig.
    let pristineWarned = false;
    const markersSub = monaco.editor.onDidChangeMarkers((uris) => {
      if (!uris.some((u) => u.toString() === uri.toString())) return;
      const marks = monaco.editor.getModelMarkers({ resource: uri });
      const errs = marks.filter((m) => m.severity >= monaco.MarkerSeverity.Warning);
      setErrorCount(errs.length);
      const sorted = [...errs].sort((a, b) => a.startLineNumber - b.startLineNumber);
      const first = sorted[0];
      setFirstError(first ? { message: first.message, line: first.startLineNumber } : null);

      if (
        !pristineWarned &&
        errs.length > 0 &&
        editor.getValue() === pristine
      ) {
        pristineWarned = true;
        console.warn(
          `[periscope] pristine YAML has ${errs.length} validation marker(s) before any edit — likely a strip/schema mismatch. Examples:`,
          sorted.slice(0, 3).map((m) => ({
            line: m.startLineNumber,
            message: m.message,
          })),
        );
      }
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

  // Owner-glyph margin: paint a colored 4px bar in the gutter for
  // every line whose YAML path is in metadata.managedFields, owned by
  // a manager other than periscope-spa. Hover the gutter to see who
  // owns that field. Reruns when meta refreshes (e.g. after an apply).
  useEffect(() => {
    const editor = editorRef.current;
    if (!editor) return;
    const model = editor.getModel();
    if (!model) {
      ownerDecorationsRef.current = editor.deltaDecorations(ownerDecorationsRef.current, []);
      return;
    }
    if (!metaQuery.data) {
      ownerDecorationsRef.current = editor.deltaDecorations(ownerDecorationsRef.current, []);
      return;
    }
    const owners = parseManagedFields(metaQuery.data.managedFields).filter(
      (o) => o.manager !== "periscope-spa",
    );
    if (owners.length === 0) {
      ownerDecorationsRef.current = editor.deltaDecorations(ownerDecorationsRef.current, []);
      return;
    }
    const ownerMap = pathToManager(owners);

    const decorations: monaco.editor.IModelDeltaDecoration[] = [];
    for (let i = 1; i <= model.getLineCount(); i++) {
      const path = pathForLine(model, i);
      if (!path) continue;
      const manager = ownerMap.get(path);
      if (!manager) continue;
      const cat = classifyManager(manager).category;
      decorations.push({
        range: new monaco.Range(i, 1, i, 1),
        options: {
          glyphMarginClassName: `glyph-owner glyph-owner--${cat.toLowerCase()}`,
          glyphMarginHoverMessage: {
            value: `**owned by \`${manager}\`** *(${cat})*\n\n${classifyManager(manager).consequence}`,
          },
        },
      });
    }
    ownerDecorationsRef.current = editor.deltaDecorations(
      ownerDecorationsRef.current,
      decorations,
    );
    // Also re-run on currentYaml changes — line numbers shift as user edits.
  }, [metaQuery.data, currentYaml]);


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

        // Invalidate list/detail/events/yaml/meta so all open views refetch.
        invalidateAfterApply(qc, source, resource);

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
        if (status === 409) {
          // Phase 2: parse field-level conflicts. If we got at least
          // one cause we recognise, switch to the resolution view.
          // If parsing fails (older apiserver, weird response shape),
          // fall back to the generic error chip.
          const parsed = parseConflictCauses(apiErr?.bodyText, ops);
          if (parsed.length > 0) {
            setConflicts(parsed);
            setResolutions(new Map());
            setApplyState({ kind: "idle" });
            setMode("conflict");
            return;
          }
        }
        setApplyState({ kind: "error", message });
      }
    },
    // ops captured intentionally — we want the snapshot at apply time, not
    // the latest after the user edits while waiting for the response
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [identity, opsForCurrentBuffer, resource, cluster, source, qc, setParams, parseConflictCauses, ops],
  );

  // Apply with per-field resolutions from ConflictResolutionView. Filter
  // out ops the user chose to "revert" (those won't be sent — apiserver
  // keeps the manager's value), then apply with force=true if any
  // remaining ops were "keep mine" (we're seizing ownership of those).
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

  const runApplyResolved = useCallback(async () => {
    if (!identity) return;
    const allOps = opsForCurrentBuffer();

    // Filter: drop revert-mine fields from the patch entirely.
    const filteredOps = allOps.filter((op) => {
      const p = opPathToString(op.path);
      return resolutions.get(p) !== "revert";
    });

    // If everything was reverted, there's nothing to send. Treat as
    // "user is done" and drop ?edit=1 (no-op apply).
    if (filteredOps.length === 0) {
      onCancel();
      return;
    }

    let body: string;
    try {
      body = buildMinimalSSA(filteredOps, identity);
    } catch (e) {
      setApplyState({ kind: "error", message: (e as Error).message });
      return;
    }

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
    const force = [...resolutions.values()].some((r) => r === "keep");

    try {
      setApplyState({ kind: "applying" });
      await api.applyResource({ ...args, dryRun: false, force }, ac.signal);
      setApplyState({ kind: "success" });
      invalidateAfterApply(qc, source, resource);
      setShowTakeover(false);
      setConflicts([]);
      setResolutions(new Map());
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
      const message = apiErr?.bodyText || (e as Error)?.message || "apply failed";
      setApplyState({ kind: "error", message });
      setShowTakeover(false);
    }
  }, [identity, opsForCurrentBuffer, resolutions, opPathToString, resource, source, qc, setParams, onCancel]);

  // Standalone dry-run (the "dry-run" button in ActionBar). Same 409
  // handling as runApply — if the dry-run hits a field-manager
  // conflict, populate conflicts state + switch to ConflictResolutionView
  // so the user can resolve per-field. Without this, dry-run 409 used
  // to fall through to the generic ApplyErrorBanner with raw text.
  const runDryRun = useCallback(async () => {
    if (!identity) return;
    const currentOps = opsForCurrentBuffer();
    if (currentOps.length === 0) return;
    let body: string;
    try {
      body = buildMinimalSSA(currentOps, identity);
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
      const status = apiErr?.status;
      const message = apiErr?.bodyText || (e as Error)?.message || "dry-run failed";
      if (status === 409) {
        const parsed = parseConflictCauses(apiErr?.bodyText, currentOps);
        if (parsed.length > 0) {
          setConflicts(parsed);
          setResolutions(new Map());
          setApplyState({ kind: "idle" });
          setMode("conflict");
          return;
        }
      }
      setApplyState({ kind: "error", message });
    }
  }, [identity, opsForCurrentBuffer, resource, parseConflictCauses]);



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
      {mode === "conflict" ? (
        <ConflictResolutionView
          conflicts={conflicts}
          resolutions={resolutions}
          onResolve={(path, choice) => {
            setResolutions((prev) => {
              const next = new Map(prev);
              if (choice === null) next.delete(path);
              else next.set(path, choice);
              return next;
            });
          }}
          onJumpTo={(path) => {
            // Drop into edit mode, focus the first line whose path matches.
            setMode("edit");
            const editor = editorRef.current;
            if (!editor) return;
            const model = editor.getModel();
            if (!model) return;
            for (let i = 1; i <= model.getLineCount(); i++) {
              if (pathForLine(model, i) === path) {
                editor.revealLineInCenter(i);
                editor.setPosition({ lineNumber: i, column: 1 });
                editor.focus();
                return;
              }
            }
          }}
          onBackToEdit={() => {
            setMode("edit");
            setApplyState({ kind: "idle" });
          }}
          onApply={() => {
            // If any field is "keep mine", show takeover dialog first.
            const anyKeep = [...resolutions.values()].some((r) => r === "keep");
            if (anyKeep) {
              setShowTakeover(true);
            } else {
              void runApplyResolved();
            }
          }}
          busy={applyState.kind === "applying" || applyState.kind === "dryRunning"}
        />
      ) : null}

      <div className={cn("relative flex min-h-0 min-w-0 flex-1", mode === "conflict" && "hidden")}>
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

      {drift && dirty && mode !== "conflict" && (
        <DriftBanner
          drift={drift}
          busy={applyState.kind === "dryRunning" || applyState.kind === "applying"}
          onShowDiff={onDriftShowDiff}
          onReload={onDriftReload}
          onDismiss={onDriftDismiss}
          showDiffEnabled
        />
      )}

      {showDriftDiff && (
        <DriftDiffOverlay
          source={source}
          cluster={cluster}
          namespace={resource.namespace}
          name={resource.name}
          pristineYaml={pristineLocked}
          onClose={onDriftDiffClose}
          onReload={onDriftReload}
        />
      )}

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
        onDryRun={() => void runDryRun()}
        onToggleDiff={() => setMode((m) => (m === "diff" ? "edit" : "diff"))}
        onApply={() => void runApply(false)}
        onJumpToError={onJumpToError}
      />

      {showTakeover && (
        <TakeoverDialog
          fields={conflicts
            .filter((c) => resolutions.get(c.path) === "keep")
            .map((c) => ({ path: c.path, manager: c.manager }))}
          onCancel={() => setShowTakeover(false)}
          onConfirm={() => {
            setShowTakeover(false);
            void runApplyResolved();
          }}
        />
      )}
    </div>
  );
}