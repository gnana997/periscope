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

import { lazy, Suspense, useCallback, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { usePublishEditorDirty } from "../../hooks/useEditorDirty";
import type { EditorSource } from "../../lib/customResources";
import type { ResourceRef } from "../../lib/api";
import type { SupportedKind } from "../../lib/schemaForm/k8sAllowlist";
import { useEditorYaml } from "../../hooks/useResource";
import { DetailLoading, DetailError } from "../detail/states";
import { ConfigMapForm } from "./ConfigMapForm";
import { SecretForm } from "./SecretForm";
import { ServiceForm } from "./ServiceForm";
import { IngressForm } from "./IngressForm";
import { DeploymentForm } from "./DeploymentForm";
import { StatefulSetForm } from "./StatefulSetForm";
import { useApplySubmit } from "./useApplySubmit";

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
    const ok = await submit.submit(draftYaml);
    if (ok) {
      // Reset baseline to the just-applied YAML so the form clears
      // dirty. The react-query invalidation kicked off in submit
      // will re-fetch under us; until that lands, treat the draft
      // as the new baseline.
      setBaselineYaml(draftYaml);
    }
  }, [draftYaml, submit]);

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
            {submit.state.kind === "error" && (
              <SubmitErrorBanner
                message={submit.state.message}
                isConflict={submit.state.isConflict}
                onSwitchToYaml={() => onSetMode("yaml")}
                onDismiss={() => submit.reset()}
              />
            )}
          </div>
          <FormActionBar
            dirty={dirty}
            busy={submit.state.kind === "dryRunning" || submit.state.kind === "applying"}
            onApply={onApply}
            onCancel={onCancel}
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

function FormActionBar({
  dirty,
  busy,
  onApply,
  onCancel,
}: {
  dirty: boolean;
  busy: boolean;
  onApply: () => void;
  onCancel: () => void;
}) {
  return (
    <div className="flex items-center justify-end gap-2 border-t border-border bg-surface px-3 py-2">
      <button
        type="button"
        onClick={onCancel}
        disabled={busy}
        className="rounded-sm border border-border bg-bg px-3 py-1 font-mono text-[12px] text-ink hover:border-ink-faint disabled:opacity-50"
      >
        cancel
      </button>
      <button
        type="button"
        onClick={onApply}
        disabled={!dirty || busy}
        className="rounded-sm border border-accent bg-accent px-3 py-1 font-mono text-[12px] text-bg hover:opacity-90 disabled:opacity-50"
      >
        {busy ? "applying…" : "apply changes"}
      </button>
    </div>
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
