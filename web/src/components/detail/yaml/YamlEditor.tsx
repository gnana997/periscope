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
import { useBlocker, useSearchParams } from "react-router-dom";

import { type ResourceMeta, type ResourceRef } from "../../../lib/api";
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
import { parseManagedFields, pathToManager } from "../../../lib/managedFields";
import { pathForLine } from "../../../lib/yamlPath";
import {
  MONACO_FONT_FAMILY,
  currentMonacoTheme,
  ensureMonacoConfigured,
  ensureMonacoYamlConfigured,
  registerSchema,
  useMonacoTheme,
} from "../../../lib/monacoSetup";
import { useOpenAPISchema, useResourceMeta, useEditorYaml } from "../../../hooks/useResource";
import { usePublishEditorDirty } from "../../../hooks/useEditorDirty";
import { computeOps, type Identity, type Op } from "../../../lib/yamlPatch";
import { useApplyLifecycle } from "../../../hooks/useApplyLifecycle";
import { ActionBar } from "./ActionBar";
import { ProblemsStrip } from "./ProblemsStrip";
import { ConflictBanner } from "./ConflictBanner";
import { bannerViewModel } from "./conflictBannerViewModel";
import { SchemaMissingBanner } from "./SchemaMissingBanner";
import { DriftBanner } from "./DriftBanner";
import { CoManagementBanner, type OtherOwnerSummary } from "./CoManagementBanner";
import { useDismissed } from "../../../hooks/useDismissed";
import { DriftDiffOverlay } from "./DriftDiffOverlay";
import { showToast } from "../../../lib/toastBus";
import { InlineDiff } from "./InlineDiff";
import { PatchPreviewDrawer } from "./PatchPreviewDrawer";
import { DetailError, DetailLoading } from "../states";

interface YamlEditorProps {
  cluster: string;
  source: EditorSource;
  resource: ResourceRef;
  /** Optional Monaco seed value. When set, overrides the fetched
   *  pristine YAML on first mount — used by KindEditRouter to carry
   *  form-mode edits forward when toggling form→yaml. After mount,
   *  Monaco's internal state takes over (this prop is honored once,
   *  not re-applied on subsequent renders). */
  initialValue?: string;
  /** Optional pristine override for dirty / diff / drift calculation.
   *  When set, replaces `stripForEdit(fetched)` as the anchor. Lets
   *  KindEditRouter keep "dirty since the original server YAML"
   *  semantics even when the editor opens with a form-edited
   *  intermediate value via initialValue. */
  pristineOverride?: string;
  /** Optional callback fired on every Monaco edit. Lets a parent
   *  mirror the buffer (e.g. KindEditRouter capturing yaml edits so
   *  they survive a yaml→form toggle). The editor remains internally
   *  stateful — this is a one-way "tell me what changed" hook, not
   *  a fully controlled value pattern. */
  onValueChange?: (next: string) => void;
  /** Whether to publish dirty state via usePublishEditorDirty.
   *  Defaults true. KindEditRouter passes false because it owns the
   *  publish itself, suppressing this duplicate producer. */
  publishDirty?: boolean;
}

export function YamlEditor({
  cluster,
  source,
  resource,
  initialValue,
  pristineOverride,
  onValueChange,
  publishDirty,
}: YamlEditorProps) {
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

  const fetchedPristine = stripForEdit(yamlQuery.data);
  return (
    <Editor
      cluster={cluster}
      source={source}
      resource={resource}
      pristine={pristineOverride ?? fetchedPristine}
      initialValue={initialValue ?? pristineOverride ?? fetchedPristine}
      onValueChange={onValueChange}
      publishDirty={publishDirty}
    />
  );
}

interface EditorProps {
  cluster: string;
  source: EditorSource;
  resource: ResourceRef;
  pristine: string;
  /** Initial Monaco value. When equal to `pristine` the editor opens
   *  clean; when different (caller supplied a draft from another
   *  mode) the editor opens dirty against the pristine baseline. */
  initialValue: string;
  onValueChange?: (next: string) => void;
  publishDirty?: boolean;
}

function Editor({
  cluster,
  source,
  resource,
  pristine,
  initialValue,
  onValueChange,
  publishDirty = true,
}: EditorProps) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const editorRef = useRef<monaco.editor.IStandaloneCodeEditor | null>(null);
  const [pristineLocked, setPristineLocked] = useState(pristine);
  const [, setParams] = useSearchParams();

  // Initial Monaco value is `initialValue` (which defaults to
  // `pristine` upstream when the parent doesn't override). This
  // lets KindEditRouter open YAML mode pre-seeded with form-mode
  // edits while keeping `pristineLocked` anchored on the original
  // server YAML for dirty / diff calculations.
  const [currentYaml, setCurrentYaml] = useState(initialValue);
  // Latest onValueChange callback — kept in a ref so the Monaco
  // mount effect (which has empty deps) can call the most recent
  // version without remounting the editor on prop change.
  const onValueChangeRef = useRef(onValueChange);
  useEffect(() => {
    onValueChangeRef.current = onValueChange;
  }, [onValueChange]);
  const [mode, setMode] = useState<"edit" | "diff">("edit");
  const [errorCount, setErrorCount] = useState(0);
  const [firstError, setFirstError] = useState<{ message: string; line: number } | null>(null);
  const [showPatch, setShowPatch] = useState(false);
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

  // Publish dirty bit so the Tab strip can show `yaml*`. Suppressed
  // when the parent owns the publish (KindEditRouter) — passing the
  // empty-kind sentinel makes the hook bail without touching the
  // cache, keeping the call unconditional (rules-of-hooks) without
  // clobbering the parent's writes.
  usePublishEditorDirty(
    cluster,
    publishDirty ? dirtyChannelKey(source) : "",
    resource.namespace,
    resource.name,
    dirty,
  );

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

  // ----- Apply lifecycle (issue #224) -----
  // Single reducer owns the entire apply lifecycle: dry-run, submit,
  // force-after-conflict, success, error. The hook owns side effects
  // (api.applyResource calls, AbortController, dry-run auto-clear
  // timer). YamlEditor consumes `lifecycle.state` and dispatches via
  // the action callbacks; it never holds applyState locally.
  const lifecycle = useApplyLifecycle({
    resource,
    baseline: pristineLocked,
    draft: currentYaml,
    identity,
    onCommitSuccess: async () => {
      // Awaited so the post-apply refetch lands before the editor
      // unmounts — YamlReadView then opens to fresh data without
      // the prior 400ms race-mitigation timeout.
      await invalidateAfterApply(qc, source, resource);
      setParams((prev) => {
        const next = new URLSearchParams(prev);
        next.delete("edit");
        return next;
      }, { replace: true });
    },
  });

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
    // Seed the model with `initialValue` (which equals `pristine`
    // unless the parent overrode it — KindEditRouter passes the
    // form-edited draft so Monaco shows it directly, not just in
    // the diff view). `pristine` stays separate as the dirty/diff
    // anchor.
    const model = monaco.editor.createModel(initialValue, "yaml", uri);

    const editor = monaco.editor.create(containerRef.current, {
      model,
      theme: currentMonacoTheme(),
      readOnly: false,
      automaticLayout: true,
      fontFamily: MONACO_FONT_FAMILY,
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
      const next = editor.getValue();
      setCurrentYaml(next);
      // Mirror to controlled parent (KindEditRouter) so yaml→form
      // toggles preserve the in-progress YAML edits.
      onValueChangeRef.current?.(next);
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


  // Apply / dry-run / force / cancel are all owned by useApplyLifecycle
  // (declared above near qc). The editor's onCancel additionally drops
  // ?edit=1 from the URL after telling the lifecycle to cancel.
  const onCancel = useCallback(() => {
    lifecycle.cancel();
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        next.delete("edit");
        return next;
      },
      { replace: true },
    );
  }, [lifecycle, setParams]);



  // Unsaved-changes guard — three layers:
  //
  //   - `beforeunload`: native browser warning on refresh / tab close.
  //   - `useBlocker` (below): custom confirm on cross-page navigation
  //     (sidebar click, browser back/forward). Requires the data
  //     router from main.tsx; throws under legacy <BrowserRouter>.
  //   - `useConfirmDiscard` in pages: custom confirm on search-param
  //     changes (row click, tab switch, close button) — those don't
  //     fire useBlocker since the pathname stays the same.
  useEffect(() => {
    if (!dirty) return;
    function onBeforeUnload(e: BeforeUnloadEvent) {
      e.preventDefault();
      e.returnValue = "";
    }
    window.addEventListener("beforeunload", onBeforeUnload);
    return () => window.removeEventListener("beforeunload", onBeforeUnload);
  }, [dirty]);

  // Cross-page navigation guard — when the user clicks the sidebar
  // for a different resource type, or hits browser back/forward, fire
  // the same custom confirm useConfirmDiscard uses for in-page
  // search-param changes. Requires the data router (see main.tsx);
  // useBlocker throws under the legacy <BrowserRouter>.
  const blocker = useBlocker(
    ({ currentLocation, nextLocation }) =>
      dirty && currentLocation.pathname !== nextLocation.pathname,
  );

  useEffect(() => {
    if (blocker.state !== "blocked") return;
    const ok = window.confirm(
      "You have unsaved YAML edits. Discard and continue?",
    );
    if (ok) blocker.proceed();
    else blocker.reset();
  }, [blocker]);

  // Keyboard shortcuts on the editor instance (Monaco's preferred
  // mechanism — captures inside the editor surface).
  useEffect(() => {
    const editor = editorRef.current;
    if (!editor) return;
    const cmdEnter = editor.addCommand(
      monaco.KeyMod.CtrlCmd | monaco.KeyCode.Enter,
      () => {
        lifecycle.submit();
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
  }, [lifecycle]);

  // Esc handler at the window level (Monaco's editor consumes Esc only
  // when widgets are open).
  useEffect(() => {
    function onKey(e: globalThis.KeyboardEvent) {
      const busy =
        lifecycle.state.kind === "DryRunning" ||
        lifecycle.state.kind === "Submitting" ||
        lifecycle.state.kind === "Forcing";
      if (e.key === "Escape" && !busy) {
        if (showPatch) {
          setShowPatch(false);
          return;
        }
        if (mode === "diff") {
          setMode("edit");
          return;
        }
        onCancel();
      }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [lifecycle.state.kind, mode, showPatch, onCancel]);

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

  // Co-management summary derived from managedFields. Other managers
  // (non-periscope-spa, any operation type) → grouped by manager name
  // and ranked by path count. periscope-spa Apply paths feed the
  // "retained" count in the banner copy.
  const coManagement = useMemo(() => {
    const entries = metaQuery.data?.managedFields;
    if (!entries) return { selfOwnedCount: 0, otherOwners: [] as OtherOwnerSummary[] };
    const owners = parseManagedFields(entries);
    const otherCounts = new Map<string, number>();
    let selfOwnedCount = 0;
    for (const o of owners) {
      if (o.manager === "periscope-spa") {
        if (o.operation === "Apply") selfOwnedCount++;
        continue;
      }
      otherCounts.set(o.manager, (otherCounts.get(o.manager) ?? 0) + 1);
    }
    const otherOwners: OtherOwnerSummary[] = [...otherCounts.entries()]
      .map(([manager, pathCount]) => ({ manager, pathCount }))
      .sort((a, b) => b.pathCount - a.pathCount);
    return { selfOwnedCount, otherOwners };
  }, [metaQuery.data]);

  const coManagementDismissKey = `periscope.coMgmtDismissed:${cluster}:${resource.group}/${resource.version}:${resource.resource}:${resource.namespace ?? ""}:${resource.name}`;
  const [coManagementDismissed, dismissCoManagement] = useDismissed(coManagementDismissKey);
  const showCoManagementBanner =
    mode === "edit" &&
    !coManagementDismissed &&
    coManagement.otherOwners.length > 0;

  // Banner view model derived from the unified lifecycle state. The
  // banner renders nothing in Idle / Submitting / DryRunning / Success;
  // it covers ForceRequired, Forcing, and Error.
  const banner = bannerViewModel(lifecycle.state);

  return (
    <div className="flex h-full min-h-0 min-w-0 flex-col">
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

      {drift && dirty && (
        <DriftBanner
          drift={drift}
          busy={lifecycle.state.kind === "DryRunning" || lifecycle.state.kind === "Submitting" || lifecycle.state.kind === "Forcing"}
          onShowDiff={onDriftShowDiff}
          onReload={onDriftReload}
          onDismiss={onDriftDismiss}
          showDiffEnabled
        />
      )}

      {showCoManagementBanner && (
        <CoManagementBanner
          selfOwnedCount={coManagement.selfOwnedCount}
          otherOwners={coManagement.otherOwners}
          onDismiss={dismissCoManagement}
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

      {schemaState === "missing" && mode === "edit" && (
        <SchemaMissingBanner kindLabel={gvk?.kind} />
      )}

      {/* Single conflict / error banner overlay (issue #224). Replaces
          the v1.1.x ConflictResolutionView + TakeoverDialog + raw
          ApplyErrorBanner combo. */}
      <ConflictBanner
        view={banner}
        onForce={lifecycle.force}
        onCancel={lifecycle.cancel}
        onRetry={lifecycle.retry}
        onDismiss={lifecycle.dismiss}
      />

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
        applyState={lifecycle.state}
        schemaLabel={schemaLabel}
        schemaState={schemaState}
        onCancel={onCancel}
        onTogglePatch={() => setShowPatch((s) => !s)}
        onDryRun={lifecycle.dryRun}
        onToggleDiff={() => setMode((m) => (m === "diff" ? "edit" : "diff"))}
        onApply={lifecycle.submit}
        onJumpToError={onJumpToError}
      />
    </div>
  );
}
