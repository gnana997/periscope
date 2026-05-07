// KindEditRouter — entry point for editing the four #116 kinds
// (ConfigMap, Secret, Service, Ingress). Picks between the
// schema-aware form view (default for these kinds) and the
// existing Monaco YAML editor based on a localStorage user
// preference, with a header toggle that flips between them.
//
// Form mode uses a thinner submit pipeline (`useApplySubmit`)
// than YamlEditor: dry-run + apply with a banner for errors,
// no field-conflict resolution view. Operators who hit a 409
// can switch to YAML mode for the full ConflictResolutionView
// machinery; the toggle preserves the operator's draft via a
// confirm-discard prompt when there are unsaved form edits.

import { lazy, Suspense, useCallback, useState } from "react";
import { useSearchParams } from "react-router-dom";
import type { EditorSource } from "../../lib/customResources";
import type { ResourceRef } from "../../lib/api";
import type { SupportedKind } from "../../lib/schemaForm/k8sAllowlist";
import { useEditorYaml } from "../../hooks/useResource";
import { DetailLoading, DetailError } from "../detail/states";
import { ConfigMapForm } from "./ConfigMapForm";
import { SecretForm } from "./SecretForm";
import { ServiceForm } from "./ServiceForm";
import { IngressForm } from "./IngressForm";
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
  const [mode, setMode] = useState<Preferred>(() => readPreferred());

  if (mode === "yaml") {
    return (
      <div className="flex h-full min-h-0 flex-col">
        <ModeToggle mode={mode} onSet={(m) => { writePreferred(m); setMode(m); }} dirty={false} />
        <Suspense fallback={<DetailLoading label="loading editor…" />}>
          <YamlEditor cluster={cluster} source={source} resource={resource} />
        </Suspense>
      </div>
    );
  }

  return (
    <FormHost
      cluster={cluster}
      source={source}
      resource={resource}
      kind={kind}
      mode={mode}
      onSetMode={(m) => { writePreferred(m); setMode(m); }}
    />
  );
}

interface FormHostProps {
  cluster: string;
  source: EditorSource;
  resource: ResourceRef;
  kind: SupportedKind;
  mode: Preferred;
  onSetMode: (m: Preferred) => void;
}

function FormHost({ cluster, source, resource, kind, mode, onSetMode }: FormHostProps) {
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
        <ModeToggle mode={mode} onSet={onSetMode} dirty={false} />
        <DetailLoading label="loading yaml…" />
      </div>
    );
  }
  if (yamlQuery.isError) {
    return (
      <div className="flex h-full min-h-0 flex-col">
        <ModeToggle mode={mode} onSet={onSetMode} dirty={false} />
        <DetailError message={(yamlQuery.error as Error)?.message ?? "unknown"} />
      </div>
    );
  }

  // Once data is available, mount FormHostInner. useState's lazy
  // initializer in the inner component captures the loaded YAML as
  // pristine without an effect-driven setState. If the YAML is
  // re-fetched and changes, this component remounts (key changes
  // via resource.namespace/name) — for v1.1 we don't propagate live
  // server changes mid-edit; operators in form mode who want drift
  // detection switch to YAML mode.
  return (
    <FormHostInner
      cluster={cluster}
      source={source}
      resource={resource}
      kind={kind}
      mode={mode}
      onSetMode={onSetMode}
      pristineYaml={yamlQuery.data}
    />
  );
}

interface FormHostInnerProps extends FormHostProps {
  pristineYaml: string;
}

function FormHostInner({
  cluster,
  source,
  resource,
  kind,
  mode,
  onSetMode,
  pristineYaml,
}: FormHostInnerProps) {
  const [, setParams] = useSearchParams();
  const [baseline, setBaseline] = useState(pristineYaml);
  const [draftYaml, setDraftYaml] = useState(pristineYaml);
  const submit = useApplySubmit(source, resource);

  const dirty = draftYaml !== baseline;

  const tryToggle = useCallback(
    (next: Preferred) => {
      if (next === mode) return;
      if (dirty) {
        const ok = window.confirm(
          "Switching modes will discard your unsaved form edits. Continue?",
        );
        if (!ok) return;
      }
      onSetMode(next);
    },
    [dirty, mode, onSetMode],
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
      // Reset baseline to the just-applied YAML so the form goes
      // clean. The react-query invalidation kicked off in submit
      // will re-fetch under us; until that lands, treat the draft
      // as the new baseline so the dirty flag clears immediately.
      setBaseline(draftYaml);
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
      <ModeToggle mode={mode} onSet={tryToggle} dirty={dirty} />
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
        {submit.state.kind === "error" && (
          <SubmitErrorBanner
            message={submit.state.message}
            isConflict={submit.state.isConflict}
            onSwitchToYaml={() => tryToggle("yaml")}
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
