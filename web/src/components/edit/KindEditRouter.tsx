// KindEditRouter — entry point for editing the four #116 kinds
// (ConfigMap, Secret, Service, Ingress). Picks between the
// schema-aware form view (default for these kinds) and the
// existing Monaco YAML editor based on a localStorage user
// preference, with a header toggle that flips between them.
//
// **Single-buffer architecture** (after rc-3 follow-up): the
// `draftYaml` state is hoisted to KindEditRouter so toggling
// between Form and YAML modes preserves operator edits in BOTH
// directions. Form mode receives `valuesYaml` + `onValuesYamlChange`
// as controlled props; YAML mode receives the same buffer via
// YamlEditor's `initialValue` + `onValueChange` controlled props,
// and reads `pristineOverride` to keep its dirty / diff calculation
// anchored on the originally-fetched server YAML rather than the
// form-edited intermediate. Cancel still has a discard prompt; the
// mode toggle no longer does.
//
// Form mode uses a thinner submit pipeline (`useApplySubmit`) than
// YamlEditor: dry-run + apply with a banner for errors, no field-
// conflict resolution view. Operators who hit a 409 can switch to
// YAML mode (now without losing their edits) for the full
// ConflictResolutionView machinery.

import { lazy, Suspense, useCallback, useEffect, useMemo, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { usePublishEditorDirty } from "../../hooks/useEditorDirty";
import type { EditorSource } from "../../lib/customResources";
import type { ResourceRef } from "../../lib/api";
import type { SupportedKind } from "../../lib/schemaForm/k8sAllowlist";
import { useEditorYaml, useOpenAPISchema, useResourceMeta } from "../../hooks/useResource";
import { DetailLoading, DetailError } from "../detail/states";
import { ConfigMapForm } from "./ConfigMapForm";
import { SecretForm } from "./SecretForm";
import { ServiceForm } from "./ServiceForm";
import { IngressForm } from "./IngressForm";
import { DeploymentForm } from "./DeploymentForm";
import { StatefulSetForm } from "./StatefulSetForm";
import { useApplySubmit } from "./useApplySubmit";
import { ActionBar } from "../detail/yaml/ActionBar";
import { PatchPreviewDrawer } from "../detail/yaml/PatchPreviewDrawer";
import { computeOps } from "../../lib/yamlPatch";
import { findSchemaForGVK, parseIdentityFromYaml } from "../../lib/k8sSchema";

const YamlEditor = lazy(() =>
  import("../detail/yaml").then((m) => ({ default: m.YamlEditor })),
);

const PREF_KEY = "periscope.editor.preferred";
type Preferred = "form" | "yaml";

function readPreferred(): Preferred {
  if (typeof window === "undefined") return "form";
  const stored = window.localStorage.getItem(PREF_KEY);
  return stored === "yaml" ? "yaml" : "form";
}

function writePreferred(value: Preferred): void {
  if (typeof window === "undefined") return;
  window.localStorage.setItem(PREF_KEY, value);
}

interface KindEditRouterProps {
  cluster: string;
  source: EditorSource;
  resource: ResourceRef;
  /** The supported kind this router is wired for. The page
   *  resolves it from the source before mounting. */
  kind: SupportedKind;
}

export function KindEditRouter({ cluster, source, resource, kind }: KindEditRouterProps) {
  // Single-fetch — both Form and YAML modes share this query via
  // TanStack Query's cache key. The pristine YAML seeds the buffer
  // on first land and stays as the dirty-tracking anchor.
  const yamlQuery = useEditorYaml(
    source,
    cluster,
    resource.namespace ?? "",
    resource.name,
    true,
  );

  if (yamlQuery.isLoading || !yamlQuery.data) {
    return (
      <div className="flex h-full min-h-0 flex-col">
        <DetailLoading label="loading yaml…" />
      </div>
    );
  }
  if (yamlQuery.isError) {
    return (
      <div className="flex h-full min-h-0 flex-col">
        <DetailError message={(yamlQuery.error as Error)?.message ?? "unknown"} />
      </div>
    );
  }

  // Inner component carries the buffer state; remounts only when
  // the resource selection changes (key includes resource identity).
  return (
    <BufferedEditor
      cluster={cluster}
      source={source}
      resource={resource}
      kind={kind}
      pristineYaml={yamlQuery.data}
    />
  );
}

interface BufferedEditorProps extends KindEditRouterProps {
  pristineYaml: string;
}

function BufferedEditor({
  cluster,
  source,
  resource,
  kind,
  pristineYaml,
}: BufferedEditorProps) {
  const [, setParams] = useSearchParams();
  const [mode, setMode] = useState<Preferred>(() => readPreferred());

  // The single canonical buffer. Both modes read from + write to
  // this. Toggling mode does NOT reset it. Apply success bumps
  // baselineYaml so dirty clears.
  const [draftYaml, setDraftYaml] = useState(pristineYaml);
  const [baselineYaml, setBaselineYaml] = useState(pristineYaml);
  const submit = useApplySubmit(source, resource);

  // Live cluster YAML + managedFields — same react-query cache keys
  // as the parent's queries, so these are free reads (no extra round
  // trip). Threaded into submit() so the retained-ownership builder
  // (#181) can extract current values for fields periscope-spa
  // already claimed but the user hasn't touched.
  const liveYamlQuery = useEditorYaml(
    source,
    cluster,
    resource.namespace ?? "",
    resource.name,
    true,
  );
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

  // ----- Form-mode action-bar state -----
  // Mirror of the affordances YamlEditor's ActionBar surfaces (ops
  // count, patch preview, dry-run, schema status) so form-mode
  // operators get the same pre-apply visibility. Diff and errors are
  // intentionally hidden — see hideDiff / hideErrors below.
  const opsForBar = useMemo(() => {
    if (draftYaml === baselineYaml) return [];
    try {
      return computeOps(baselineYaml, draftYaml);
    } catch {
      return [];
    }
  }, [baselineYaml, draftYaml]);

  const identityForBar = useMemo(
    () => parseIdentityFromYaml(draftYaml),
    [draftYaml],
  );

  // Schema query gives ActionBar the right pill state (loading /
  // loaded / missing). Same cache key the form components use under
  // the hood, so this is a free read. `kind` (SupportedKind) is the
  // PascalCase K8s kind name; `resource.group/version` come from the
  // SPA's resolved ResourceRef.
  const schemaQuery = useOpenAPISchema(
    cluster,
    resource.group,
    resource.version,
    true,
  );
  const schemaLabel = `${resource.group ? resource.group + "/" : ""}${resource.version} ${kind}`;
  const schemaState: "loading" | "loaded" | "missing" | "failed" =
    schemaQuery.isError
      ? "failed"
      : !schemaQuery.data
        ? "loading"
        : findSchemaForGVK(schemaQuery.data, {
              group: resource.group,
              version: resource.version,
              kind,
            })
          ? "loaded"
          : "missing";

  // PatchPreviewDrawer state — mirror of YamlEditor.tsx. Width
  // persists via the same localStorage key so users get a single
  // remembered width across YAML / form modes.
  const [showPatch, setShowPatch] = useState(false);
  const [patchDrawerWidth, setPatchDrawerWidth] = useState<number>(() => {
    if (typeof window === "undefined") return 420;
    const stored = window.localStorage.getItem("periscope.patchDrawerWidth");
    const n = stored ? parseInt(stored, 10) : NaN;
    return Number.isFinite(n) && n >= 280 && n <= 800 ? n : 420;
  });
  useEffect(() => {
    if (typeof window === "undefined") return;
    window.localStorage.setItem("periscope.patchDrawerWidth", String(patchDrawerWidth));
  }, [patchDrawerWidth]);

  const onPatchResizeStart = useCallback(
    (e: React.MouseEvent) => {
      e.preventDefault();
      const startX = e.clientX;
      const startWidth = patchDrawerWidth;
      document.body.style.cursor = "col-resize";
      document.body.style.userSelect = "none";
      const onMove = (ev: MouseEvent) => {
        // Drawer is on the right edge — drag LEFT widens it (subtract
        // current clientX from start). Clamp 280…800 to match YAML mode.
        const dx = startX - ev.clientX;
        setPatchDrawerWidth(Math.min(800, Math.max(280, startWidth + dx)));
      };
      const onUp = () => {
        document.body.style.cursor = "";
        document.body.style.userSelect = "";
        window.removeEventListener("mousemove", onMove);
        window.removeEventListener("mouseup", onUp);
      };
      window.addEventListener("mousemove", onMove);
      window.addEventListener("mouseup", onUp);
    },
    [patchDrawerWidth],
  );

  const dirty = draftYaml !== baselineYaml;

  // Publish dirty to the page-level useEditorDirty cache so the
  // DetailPane tab strip's `yaml*` indicator + the page's
  // useConfirmDiscard wrapping (sidebar nav, namespace switch,
  // row-click) work for ALL edit paths through this router. Single
  // publisher — overrides the publish that YamlEditor would do
  // internally (suppressed via `publishDirty={false}` below) so
  // there's only one producer per resource.
  const dirtyKind = source.kind === "builtin" ? source.yamlKind : "";
  usePublishEditorDirty(
    cluster,
    dirtyKind,
    resource.namespace,
    resource.name,
    dirty,
  );

  // Mode toggle: NO discard prompt — the buffer is preserved across
  // modes. Cancel still confirms because that exits the editor.
  const onSetMode = useCallback(
    (next: Preferred) => {
      writePreferred(next);
      setMode(next);
    },
    [],
  );

  const onCancel = useCallback(() => {
    if (dirty) {
      const ok = window.confirm("Discard unsaved changes?");
      if (!ok) return;
    }
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        next.delete("edit");
        return next;
      },
      { replace: true },
    );
  }, [dirty, setParams]);

  const onApply = useCallback(async () => {
    const ok = await submit.submit({
      baseline: baselineYaml,
      draft: draftYaml,
      // Fall back to baseline if the live query hasn't (re)resolved
      // yet — same anchor the editor was mounted against. Worse case
      // we miss a drift update; we never apply with `current = ""`.
      current: liveYamlQuery.data ?? baselineYaml,
      meta: metaQuery.data ?? null,
    });
    if (ok) {
      // Reset baseline to the just-applied YAML so the form clears
      // dirty. The react-query invalidation kicked off in submit
      // will re-fetch under us; until that lands, treat the draft
      // as the new baseline.
      setBaselineYaml(draftYaml);
    }
  }, [baselineYaml, draftYaml, liveYamlQuery.data, metaQuery.data, submit]);

  const onDryRun = useCallback(() => {
    void submit.dryRun({
      baseline: baselineYaml,
      draft: draftYaml,
      current: liveYamlQuery.data ?? baselineYaml,
      meta: metaQuery.data ?? null,
    });
  }, [baselineYaml, draftYaml, liveYamlQuery.data, metaQuery.data, submit]);

  const onValuesYamlChange = useCallback(
    (next: string) => {
      setDraftYaml(next);
      if (submit.state.kind !== "idle") submit.reset();
    },
    [submit],
  );

  return (
    <div className="flex h-full min-h-0 flex-col">
      <ModeToggle mode={mode} onSet={onSetMode} dirty={dirty} />
      {mode === "yaml" ? (
        // YAML mode: hand the canonical buffer to YamlEditor.
        // - initialValue seeds Monaco with the current draftYaml
        //   (so toggling form→yaml preserves form edits).
        // - pristineOverride keeps YamlEditor's dirty/diff/drift
        //   calculation anchored on the original server YAML
        //   (baselineYaml), not on the form-edited intermediate.
        // - onValueChange mirrors every Monaco edit back into our
        //   draftYaml (so toggling yaml→form preserves YAML edits).
        // - publishDirty={false} suppresses YamlEditor's own
        //   useEditorDirty publish — KindEditRouter is the single
        //   producer (above).
        <Suspense fallback={<DetailLoading label="loading editor…" />}>
          <YamlEditor
            cluster={cluster}
            source={source}
            resource={resource}
            initialValue={draftYaml}
            pristineOverride={baselineYaml}
            onValueChange={setDraftYaml}
            publishDirty={false}
          />
        </Suspense>
      ) : (
        <div className="flex flex-1 flex-col overflow-hidden">
          {/*
           * Form scroll area + PatchPreviewDrawer share a flex row so
           * the drawer pushes (not overlays) the form. Same layout
           * YamlEditor uses for the Monaco editor + drawer pair.
           */}
          <div className="flex min-h-0 flex-1 flex-row">
            <div className="flex-1 overflow-auto px-3 py-3">
              {kind === "ConfigMap" && (
                <ConfigMapForm
                  cluster={cluster}
                  valuesYaml={draftYaml}
                  onValuesYamlChange={onValuesYamlChange}
                  mode="edit"
                />
              )}
              {kind === "Secret" && (
                <SecretForm
                  cluster={cluster}
                  valuesYaml={draftYaml}
                  onValuesYamlChange={onValuesYamlChange}
                  mode="edit"
                />
              )}
              {kind === "Service" && (
                <ServiceForm
                  cluster={cluster}
                  valuesYaml={draftYaml}
                  onValuesYamlChange={onValuesYamlChange}
                  mode="edit"
                />
              )}
              {kind === "Ingress" && (
                <IngressForm
                  cluster={cluster}
                  valuesYaml={draftYaml}
                  onValuesYamlChange={onValuesYamlChange}
                  mode="edit"
                />
              )}
              {kind === "Deployment" && (
                <DeploymentForm
                  cluster={cluster}
                  valuesYaml={draftYaml}
                  onValuesYamlChange={onValuesYamlChange}
                  mode="edit"
                />
              )}
              {kind === "StatefulSet" && (
                <StatefulSetForm
                  cluster={cluster}
                  valuesYaml={draftYaml}
                  onValuesYamlChange={onValuesYamlChange}
                  mode="edit"
                />
              )}
            </div>
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
                  ops={opsForBar}
                  identity={identityForBar}
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
          {/*
           * SubmitErrorBanner intentionally lives OUTSIDE the form's
           * overflow-auto scroll area so it stays visible regardless
           * of how tall the form is. The previous placement (inside
           * the scroll div, below every form section) buried errors
           * for non-trivial resources — operators had to scroll to
           * the bottom to discover that their apply had failed. This
           * mirrors YamlEditor's ApplyErrorBanner placement (right
           * above ActionBar, sibling of the scrollable editor).
           */}
          {submit.state.kind === "error" && (
            <SubmitErrorBanner
              message={submit.state.message}
              isConflict={submit.state.isConflict}
              onSwitchToYaml={() => onSetMode("yaml")}
              onDismiss={() => submit.reset()}
            />
          )}
          <ActionBar
            mode="edit"
            opsCount={opsForBar.length}
            // hideErrors below suppresses both the indicator and the
            // "fix N schema errors first" apply gate — form-mode
            // validation is handled inside the form components and
            // doesn't surface a count up here.
            errorCount={0}
            dirty={dirty}
            applyState={submit.state}
            schemaLabel={schemaLabel}
            schemaState={schemaState}
            metaPending={metaQuery.isPending}
            hideDiff
            hideErrors
            onCancel={onCancel}
            onTogglePatch={() => setShowPatch((s) => !s)}
            onDryRun={onDryRun}
            onApply={onApply}
          />
        </div>
      )}
    </div>
  );
}

function ModeToggle({
  mode,
  onSet,
  dirty,
}: {
  mode: Preferred;
  onSet: (next: Preferred) => void;
  dirty: boolean;
}) {
  return (
    <div className="flex items-center justify-between border-b border-border bg-surface px-3 py-1.5">
      <div className="flex items-center gap-1">
        <ToggleButton active={mode === "form"} onClick={() => onSet("form")}>
          form
        </ToggleButton>
        <ToggleButton active={mode === "yaml"} onClick={() => onSet("yaml")}>
          yaml
        </ToggleButton>
        {dirty ? (
          <span className="ml-2 font-mono text-[10.5px] uppercase tracking-[0.14em] text-yellow">
            unsaved
          </span>
        ) : null}
      </div>
      <span className="font-mono text-[10.5px] uppercase tracking-[0.14em] text-ink-faint">
        {mode === "form" ? "schema-aware editor" : "yaml editor"}
      </span>
    </div>
  );
}

function ToggleButton({
  active,
  onClick,
  children,
}: {
  active: boolean;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={
        active
          ? "rounded-sm bg-bg px-2.5 py-0.5 font-mono text-[12px] text-ink shadow-sm"
          : "rounded-sm px-2.5 py-0.5 font-mono text-[12px] text-ink-muted hover:text-ink"
      }
    >
      {children}
    </button>
  );
}

function SubmitErrorBanner({
  message,
  isConflict,
  onSwitchToYaml,
  onDismiss,
}: {
  message: string;
  isConflict: boolean;
  onSwitchToYaml: () => void;
  onDismiss: () => void;
}) {
  return (
    <div className="mt-3 rounded-sm border border-red/40 bg-red/5 px-3 py-2 text-[12.5px] text-ink">
      <div className="flex items-baseline justify-between gap-3">
        <span className="font-mono text-[10.5px] uppercase tracking-[0.14em] text-red">
          {isConflict ? "field manager conflict" : "apply failed"}
        </span>
        <button
          type="button"
          onClick={onDismiss}
          className="font-mono text-[11px] text-ink-faint hover:text-red"
        >
          dismiss
        </button>
      </div>
      <p className="mt-1 whitespace-pre-wrap font-mono text-[12px] text-ink-muted">{message}</p>
      {isConflict ? (
        <p className="mt-2 text-[12px] text-ink-muted">
          form mode doesn't surface the per-field conflict view —{" "}
          <button
            type="button"
            onClick={onSwitchToYaml}
            className="font-mono text-accent hover:underline"
          >
            switch to YAML mode
          </button>{" "}
          to resolve fields individually.
        </p>
      ) : null}
    </div>
  );
}
