// ChartActionDialog — install + upgrade workflow modal (#74 / #75 / #76).
//
// Supersedes HelmInstallDialog (#74). Same single-pane top-to-bottom
// flow, now bilingual:
//
//   1. Operator pastes a chart ref OR (upgrade mode) lands on the
//      dialog with the current release's chart pre-filled.
//   2. For HTTP repos, also enters a chart name.
//   3. Picks a version → values + schema land in the editor.
//   4. (Install only) Picks a namespace + release name.
//   5. Edits values via HelmValuesEditor.
//   6. Click Preview → renders the manifests + (upgrade) diff inline,
//      with the RBAC denial list if any kind would be rejected.
//   7. Click Install / Upgrade → fires the real action. Atomic by
//      default (failed installs auto-rollback, no half-deployed state).
//      On success, the modal closes and the SPA navigates to the
//      release detail page.
//
// The mode prop ("install" | "upgrade") is the discriminant. Install
// mode collects ns + releaseName as inputs; upgrade mode reads them
// from props and pre-fills chart ref + values from the current
// release. The preview pane reuses the same component for both.

import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import {
  useChartValuesMutation,
  useChartVersions,
} from "../../hooks/useChartFetch";
import {
  useHelmInstallPreview,
  useHelmUpgradePreview,
  useInstallHelmRelease,
  useUpgradeHelmRelease,
} from "../../hooks/useHelm";
import type {
  ChartFetchResult,
  HelmManifestObject,
  PreviewDenial,
  PreviewResponse,
} from "../../lib/types";
import type { JSONSchema } from "../../lib/helmSchema";
import { Modal } from "../ui/Modal";
import { ApiError } from "../../lib/api";
import { showToast } from "../../lib/toastBus";
import { HelmValuesEditor } from "./HelmValuesEditor";

interface InstallModeProps {
  mode: "install";
}

interface UpgradeModeProps {
  mode: "upgrade";
  /** Current release name, taken from URL on the release detail page. */
  releaseName: string;
  /** Current release namespace. */
  namespace: string;
  /** Current chart ref — pre-fills the input. Operator can change to
   *  upgrade across charts (rare but valid). */
  initialChartRef?: string;
  /** Current chart name within an HTTP repo. */
  initialChartName?: string;
  /** Current values YAML — pre-fills the editor so operators see what
   *  they're modifying, not a blank slate. */
  initialValues?: string;
}

type ChartActionDialogProps = (InstallModeProps | UpgradeModeProps) & {
  open: boolean;
  onClose: () => void;
  cluster: string;
};

export function ChartActionDialog(props: ChartActionDialogProps) {
  const { open, onClose, cluster } = props;
  const labelId = "chart-action-dialog-title";
  const navigate = useNavigate();

  // ── Form state ─────────────────────────────────────────────────
  const [chartRef, setChartRef] = useState(
    props.mode === "upgrade" ? (props.initialChartRef ?? "") : "",
  );
  const [chartName, setChartName] = useState(
    props.mode === "upgrade" ? (props.initialChartName ?? "") : "",
  );
  const [version, setVersion] = useState("");
  const [valuesYaml, setValuesYaml] = useState(
    props.mode === "upgrade" ? (props.initialValues ?? "") : "",
  );
  const [chart, setChart] = useState<ChartFetchResult | null>(null);

  // Install-mode only: ns + release name as operator inputs.
  const [installNamespace, setInstallNamespace] = useState("default");
  const [installReleaseName, setInstallReleaseName] = useState("");

  // Computed ns + release name — reads from props in upgrade mode.
  const targetNamespace =
    props.mode === "upgrade" ? props.namespace : installNamespace;
  const targetReleaseName =
    props.mode === "upgrade" ? props.releaseName : installReleaseName;

  // ── Preview state ─────────────────────────────────────────────
  const [showPreview, setShowPreview] = useState(false);
  const [preview, setPreview] = useState<PreviewResponse | null>(null);

  // ── Chart fetch (existing flow from #74) ──────────────────────
  const isOCI = chartRef.startsWith("oci://");
  const versionsEnabled =
    chartRef.length > 0 && (isOCI || chartName.length > 0);
  const versions = useChartVersions({
    cluster,
    ref: chartRef,
    chartName,
    enabled: versionsEnabled,
  });
  const valuesMutation = useChartValuesMutation(cluster);

  // ── Action mutations (#75 + #76) ───────────────────────────────
  const installPreviewMutation = useHelmInstallPreview(cluster);
  const upgradePreviewMutation = useHelmUpgradePreview(cluster);
  const installMutation = useInstallHelmRelease(cluster);
  const upgradeMutation = useUpgradeHelmRelease(
    cluster,
    targetNamespace,
    targetReleaseName,
  );
  const previewPending =
    installPreviewMutation.isPending || upgradePreviewMutation.isPending;
  const actionPending = installMutation.isPending || upgradeMutation.isPending;

  // Fetch values automatically when version is picked (vs. the
  // older two-click flow). Better UX in upgrade mode where operators
  // expect the values to populate without an extra button.
  const fetchValues = () => {
    if (!chartRef || !version) return;
    valuesMutation.mutate(
      { ref: chartRef, chart: chartName || undefined, version },
      {
        onSuccess: (result) => {
          setChart(result);
          // Only overwrite valuesYaml in install mode; upgrade mode
          // keeps the operator's edits-on-top-of-current.
          if (props.mode === "install") {
            setValuesYaml(result.values);
          } else if (!valuesYaml) {
            setValuesYaml(result.values);
          }
          // Reset preview when chart version changes — the rendered
          // manifests would be stale.
          setPreview(null);
        },
        onError: (err) => {
          showToast(`fetch failed: ${friendlyError(err)}`, "error");
        },
      },
    );
  };

  // ── Submit handlers ───────────────────────────────────────────
  const onPreviewClick = () => {
    setShowPreview(true);
    if (props.mode === "install") {
      installPreviewMutation.mutate(
        {
          ref: chartRef,
          chartName: chartName || undefined,
          version,
          namespace: installNamespace,
          releaseName: installReleaseName,
          values: valuesYaml,
        },
        {
          onSuccess: setPreview,
          onError: (err) => showToast(`preview failed: ${friendlyError(err)}`, "error"),
        },
      );
    } else {
      upgradePreviewMutation.mutate(
        {
          namespace: targetNamespace,
          name: targetReleaseName,
          body: {
            ref: chartRef,
            chartName: chartName || undefined,
            version,
            values: valuesYaml,
          },
        },
        {
          onSuccess: setPreview,
          onError: (err) => showToast(`preview failed: ${friendlyError(err)}`, "error"),
        },
      );
    }
  };

  const onActionClick = () => {
    if (props.mode === "install") {
      installMutation.mutate(
        {
          ref: chartRef,
          chartName: chartName || undefined,
          version,
          namespace: installNamespace,
          releaseName: installReleaseName,
          values: valuesYaml,
        },
        {
          onSuccess: (result) => {
            showToast(
              `installed ${result.release.name} (revision ${result.release.revision})`,
              "success",
            );
            onCloseAndReset();
            navigate(
              `/clusters/${encodeURIComponent(cluster)}/helm/${encodeURIComponent(
                result.release.namespace,
              )}/${encodeURIComponent(result.release.name)}`,
            );
          },
          onError: (err) => {
            showToast(`install failed: ${friendlyError(err)}`, "error");
          },
        },
      );
    } else {
      upgradeMutation.mutate(
        {
          ref: chartRef,
          chartName: chartName || undefined,
          version,
          values: valuesYaml,
        },
        {
          onSuccess: (result) => {
            const msg = result.rolledBack
              ? `upgrade rolled back (now at revision ${result.release.revision})`
              : `upgraded to revision ${result.release.revision}`;
            showToast(msg, result.rolledBack ? "warning" : "success");
            onCloseAndReset();
          },
          onError: (err) => {
            showToast(`upgrade failed: ${friendlyError(err)}`, "error");
          },
        },
      );
    }
  };

  // ── Lifecycle ─────────────────────────────────────────────────
  const onCloseAndReset = () => {
    onClose();
    setTimeout(() => {
      // In upgrade mode, props.initialChartRef etc. may persist across
      // open/close; we reset to the props' initial state, not blank.
      setChartRef(
        props.mode === "upgrade" ? (props.initialChartRef ?? "") : "",
      );
      setChartName(
        props.mode === "upgrade" ? (props.initialChartName ?? "") : "",
      );
      setVersion("");
      setValuesYaml(
        props.mode === "upgrade" ? (props.initialValues ?? "") : "",
      );
      setChart(null);
      setInstallNamespace("default");
      setInstallReleaseName("");
      setShowPreview(false);
      setPreview(null);
    }, 0);
  };

  // Auto-fetch values when version changes — better UX than
  // requiring a second click. This is the one flow change vs. #74.
  useEffect(() => {
    if (chartRef && version) {
      fetchValues();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- intentionally narrow
  }, [version]);

  // ── Validation gates ──────────────────────────────────────────
  const valuesReady = chart != null;
  const targetIdentityReady =
    props.mode === "upgrade"
      ? true
      : Boolean(installNamespace.trim()) && Boolean(installReleaseName.trim());
  const previewReady =
    valuesReady && targetIdentityReady && version.length > 0;
  const actionReady =
    previewReady && (preview == null || (preview.denied?.length ?? 0) === 0);

  return (
    <Modal
      open={open}
      onClose={onCloseAndReset}
      labelledBy={labelId}
      size="lg"
      panelClassName="max-h-[90vh] flex flex-col"
    >
      <header className="flex shrink-0 items-baseline justify-between border-b border-border px-5 py-3">
        <h2 id={labelId} className="font-display text-[20px] italic text-ink">
          {props.mode === "install" ? "Install Helm chart" : "Upgrade Helm release"}{" "}
          <span className="text-ink-muted">·</span>{" "}
          <span className="font-mono text-[14px] text-ink-muted not-italic">
            {props.mode === "upgrade" ? `${props.releaseName} ` : ""}
            {cluster}
          </span>
        </h2>
        <button
          type="button"
          onClick={onCloseAndReset}
          className="font-mono text-[12px] text-ink-faint hover:text-ink"
        >
          close
        </button>
      </header>

      <div className="min-h-0 flex-1 overflow-y-auto space-y-4 px-5 py-4">
        <RefInputBlock
          chartRef={chartRef}
          chartName={chartName}
          onRefChange={(next) => {
            setChartRef(next);
            setVersion("");
            setChart(null);
            setPreview(null);
          }}
          onChartNameChange={(next) => {
            setChartName(next);
            setVersion("");
            setChart(null);
            setPreview(null);
          }}
          onFetchClick={() => versions.refetch()}
          versionsLoading={versions.isFetching}
          versionsError={versions.error}
        />

        {versions.data && versions.data.versions.length > 0 ? (
          <VersionPickerBlock
            versions={versions.data.versions}
            value={version}
            onChange={setVersion}
            valuesLoading={valuesMutation.isPending}
            valuesError={valuesMutation.error}
          />
        ) : null}

        {/* Install mode only: target namespace + release name. */}
        {props.mode === "install" && chart ? (
          <TargetIdentityBlock
            namespace={installNamespace}
            releaseName={installReleaseName}
            onNamespaceChange={(s) => {
              setInstallNamespace(s);
              setPreview(null);
            }}
            onReleaseNameChange={(s) => {
              setInstallReleaseName(s);
              setPreview(null);
            }}
          />
        ) : null}

        {chart ? <ChartHeaderBlock chart={chart} /> : null}
        {chart ? (
          <HelmValuesEditor
            valuesYaml={valuesYaml}
            schema={chart.schema as JSONSchema | undefined}
            onValuesYamlChange={(s) => {
              setValuesYaml(s);
              setPreview(null);
            }}
          />
        ) : null}

        {/* Collapsible preview pane — only renders after Preview is
            clicked and there's a result OR the request is pending. */}
        {showPreview ? (
          <PreviewPaneBlock
            mode={props.mode}
            pending={previewPending}
            preview={preview}
            onClose={() => {
              setShowPreview(false);
              setPreview(null);
            }}
          />
        ) : null}
      </div>

      <footer className="flex shrink-0 flex-wrap items-center justify-between gap-x-3 gap-y-2 border-t border-border bg-surface-2 px-5 py-3">
        <span className="font-mono text-[11px] text-ink-faint">
          {props.mode === "install"
            ? "atomic install — failures auto-roll-back"
            : "atomic upgrade — failures auto-roll-back to previous revision"}
        </span>
        <div className="flex flex-wrap items-center gap-2">
          <button
            type="button"
            onClick={onCloseAndReset}
            disabled={actionPending}
            className="border border-border px-3 py-1 font-mono text-[12px] text-ink-muted hover:text-ink disabled:opacity-50"
          >
            cancel
          </button>
          <button
            type="button"
            onClick={onPreviewClick}
            disabled={!previewReady || previewPending || actionPending}
            className="border border-border px-3 py-1 font-mono text-[12px] text-ink hover:bg-surface disabled:cursor-not-allowed disabled:opacity-50"
          >
            {previewPending ? "previewing…" : "preview"}
          </button>
          <button
            type="button"
            onClick={onActionClick}
            disabled={!actionReady || actionPending}
            className="border border-accent bg-accent px-3 py-1 font-mono text-[12px] text-white disabled:cursor-not-allowed disabled:opacity-50"
          >
            {actionPending
              ? props.mode === "install"
                ? "installing…"
                : "upgrading…"
              : props.mode}
          </button>
        </div>
      </footer>
    </Modal>
  );
}

// ─── Sub-blocks ─────────────────────────────────────────────────────

function RefInputBlock({
  chartRef,
  chartName,
  onRefChange,
  onChartNameChange,
  onFetchClick,
  versionsLoading,
  versionsError,
}: {
  chartRef: string;
  chartName: string;
  onRefChange: (s: string) => void;
  onChartNameChange: (s: string) => void;
  onFetchClick: () => void;
  versionsLoading: boolean;
  versionsError: unknown;
}) {
  const isOCI = chartRef.startsWith("oci://");
  return (
    <section className="space-y-2">
      <label className="font-mono text-[10.5px] uppercase tracking-[0.18em] text-ink-faint">
        chart reference
      </label>
      <div className="flex flex-wrap gap-2">
        <input
          type="text"
          value={chartRef}
          onChange={(e) => onRefChange(e.target.value)}
          placeholder="https://charts.bitnami.com/bitnami  or  oci://ghcr.io/owner/charts/foo"
          className="min-w-0 flex-1 rounded-sm border border-border bg-bg px-2.5 py-1.5 font-mono text-[12.5px] text-ink placeholder:text-ink-faint focus:border-accent focus:outline-none"
        />
        {!isOCI && chartRef.length > 0 ? (
          <input
            type="text"
            value={chartName}
            onChange={(e) => onChartNameChange(e.target.value)}
            placeholder="chart name (e.g. nginx)"
            className="w-full rounded-sm border border-border bg-bg px-2.5 py-1.5 font-mono text-[12.5px] text-ink placeholder:text-ink-faint focus:border-accent focus:outline-none sm:w-48"
          />
        ) : null}
        <button
          type="button"
          onClick={onFetchClick}
          disabled={chartRef.length === 0 || (!isOCI && chartName.length === 0)}
          className="border border-accent bg-accent px-3 py-1 font-mono text-[12px] text-white disabled:opacity-50"
        >
          {versionsLoading ? "fetching…" : "fetch"}
        </button>
      </div>
      {versionsError ? (
        <p className="font-mono text-[12px] text-red">
          {friendlyError(versionsError)}
        </p>
      ) : null}
    </section>
  );
}

function VersionPickerBlock({
  versions,
  value,
  onChange,
  valuesLoading,
  valuesError,
}: {
  versions: string[];
  value: string;
  onChange: (v: string) => void;
  valuesLoading: boolean;
  valuesError: unknown;
}) {
  return (
    <section className="space-y-2">
      <label className="font-mono text-[10.5px] uppercase tracking-[0.18em] text-ink-faint">
        version
      </label>
      <div className="flex flex-wrap gap-2">
        <select
          value={value}
          onChange={(e) => onChange(e.target.value)}
          disabled={valuesLoading}
          className="min-w-0 flex-1 rounded-sm border border-border bg-bg px-2.5 py-1.5 font-mono text-[12.5px] text-ink focus:border-accent focus:outline-none disabled:opacity-50"
        >
          <option value="">— pick a version ({versions.length} available) —</option>
          {versions.map((v) => (
            <option key={v} value={v}>
              {v}
            </option>
          ))}
        </select>
        {valuesLoading ? (
          <span className="self-center font-mono text-[12px] text-ink-faint">
            loading values…
          </span>
        ) : null}
      </div>
      {valuesError ? (
        <p className="font-mono text-[12px] text-red">
          {friendlyError(valuesError)}
        </p>
      ) : null}
    </section>
  );
}

function TargetIdentityBlock({
  namespace,
  releaseName,
  onNamespaceChange,
  onReleaseNameChange,
}: {
  namespace: string;
  releaseName: string;
  onNamespaceChange: (s: string) => void;
  onReleaseNameChange: (s: string) => void;
}) {
  return (
    <section className="space-y-2">
      <label className="font-mono text-[10.5px] uppercase tracking-[0.18em] text-ink-faint">
        target identity
      </label>
      <div className="flex flex-wrap gap-2">
        <input
          type="text"
          value={namespace}
          onChange={(e) => onNamespaceChange(e.target.value)}
          placeholder="namespace"
          className="min-w-0 flex-1 rounded-sm border border-border bg-bg px-2.5 py-1.5 font-mono text-[12.5px] text-ink placeholder:text-ink-faint focus:border-accent focus:outline-none"
        />
        <input
          type="text"
          value={releaseName}
          onChange={(e) => onReleaseNameChange(e.target.value)}
          placeholder="release name"
          className="min-w-0 flex-1 rounded-sm border border-border bg-bg px-2.5 py-1.5 font-mono text-[12.5px] text-ink placeholder:text-ink-faint focus:border-accent focus:outline-none"
        />
      </div>
    </section>
  );
}

function ChartHeaderBlock({ chart }: { chart: ChartFetchResult }) {
  return (
    <section className="rounded-sm border border-border bg-surface px-4 py-3">
      <div className="flex flex-wrap items-baseline justify-between gap-3">
        <h3 className="font-display text-[18px] italic text-ink">
          {chart.meta.name}{" "}
          <span className="font-mono text-[12px] text-ink-muted not-italic">
            {chart.meta.version}
          </span>
        </h3>
        <span className="font-mono text-[10.5px] uppercase tracking-[0.18em] text-ink-faint">
          chart {chart.meta.apiVersion}
        </span>
      </div>
      {chart.meta.description ? (
        <p className="mt-1 text-[13px] text-ink-muted">{chart.meta.description}</p>
      ) : null}
      <div className="mt-2 flex flex-wrap gap-x-4 gap-y-1 font-mono text-[11px] text-ink-faint">
        {chart.meta.appVersion ? <span>app: {chart.meta.appVersion}</span> : null}
        {chart.meta.kubeVersion ? (
          <span>k8s: {chart.meta.kubeVersion}</span>
        ) : null}
        {chart.meta.type ? <span>type: {chart.meta.type}</span> : null}
        {chart.schema ? (
          <span className="text-data">schema: yes (form mode)</span>
        ) : (
          <span>schema: none (yaml mode)</span>
        )}
      </div>
    </section>
  );
}

function PreviewPaneBlock({
  mode,
  pending,
  preview,
  onClose,
}: {
  mode: "install" | "upgrade";
  pending: boolean;
  preview: PreviewResponse | null;
  onClose: () => void;
}) {
  const denied = preview?.denied ?? null;
  return (
    <section className="rounded-sm border border-border bg-surface">
      <header className="flex items-center justify-between border-b border-border px-3 py-2">
        <span className="font-mono text-[11px] uppercase tracking-[0.18em] text-ink-faint">
          preview · dry-run
        </span>
        <button
          type="button"
          onClick={onClose}
          className="font-mono text-[11px] text-ink-faint hover:text-ink"
        >
          hide
        </button>
      </header>

      {pending ? (
        <p className="px-3 py-3 font-mono text-[12px] text-ink-faint">
          rendering…
        </p>
      ) : preview == null ? (
        <p className="px-3 py-3 font-mono text-[12px] text-ink-faint">
          no preview yet — click preview to render
        </p>
      ) : (
        <div className="space-y-3 px-3 py-3">
          {denied && denied.length > 0 ? (
            <DenialList denied={denied} />
          ) : (
            <p className="font-mono text-[11px] text-data">
              ✓ rbac pre-flight passed — no denials
            </p>
          )}
          <ManifestList manifests={preview.manifests} />
          {mode === "upgrade" && preview.diff ? (
            <DiffSummary diff={preview.diff} />
          ) : null}
        </div>
      )}
    </section>
  );
}

function DenialList({ denied }: { denied: PreviewDenial[] }) {
  return (
    <div className="space-y-1">
      <p className="font-mono text-[11px] uppercase tracking-[0.16em] text-red">
        rbac pre-flight denied {denied.length} resource{denied.length === 1 ? "" : "s"}
      </p>
      <ul className="space-y-0.5 font-mono text-[11.5px] text-ink-muted">
        {denied.map((d, i) => (
          <li key={i}>
            <span className="text-red">×</span>{" "}
            <span>
              {d.verb} {d.group ? `${d.group}/` : ""}
              {d.resource}
              {d.namespace ? ` in ${d.namespace}` : ""}
              {d.name ? ` (${d.name})` : ""}
            </span>
            <span className="ml-2 text-ink-faint">— {d.reason}</span>
          </li>
        ))}
      </ul>
      <p className="font-mono text-[10.5px] text-ink-faint">
        the apiserver would reject these — install / upgrade is blocked until your role is broadened
      </p>
    </div>
  );
}

function ManifestList({ manifests }: { manifests: HelmManifestObject[] }) {
  if (manifests.length === 0) {
    return (
      <p className="font-mono text-[11.5px] text-ink-faint">
        no manifests rendered
      </p>
    );
  }
  return (
    <details className="font-mono text-[11.5px]">
      <summary className="cursor-pointer text-ink hover:text-accent">
        {manifests.length} manifest{manifests.length === 1 ? "" : "s"} (click to expand)
      </summary>
      <ul className="mt-1.5 space-y-0.5 pl-4 text-ink-muted">
        {manifests.map((m, i) => (
          <li key={i}>
            <span className="text-ink-faint">{m.apiVersion}</span>{" "}
            <span className="text-ink">{m.kind}</span>
            {m.namespace ? (
              <span className="text-ink-faint">/{m.namespace}</span>
            ) : null}
            <span className="text-ink">/{m.name}</span>
          </li>
        ))}
      </ul>
    </details>
  );
}

function DiffSummary({
  diff,
}: {
  diff: NonNullable<PreviewResponse["diff"]>;
}) {
  const counts = diff.changes.reduce(
    (acc, c) => {
      acc[c.kind] = (acc[c.kind] ?? 0) + 1;
      return acc;
    },
    {} as Record<string, number>,
  );
  return (
    <div className="space-y-1 font-mono text-[11.5px]">
      <p className="text-ink-faint uppercase tracking-[0.16em] text-[10.5px]">
        diff vs current cluster state
      </p>
      <p className="text-ink">
        {diff.changes.length} change{diff.changes.length === 1 ? "" : "s"}
        {Object.entries(counts).map(([k, v]) => (
          <span key={k} className="ml-3 text-ink-muted">
            {k}: {v}
          </span>
        ))}
      </p>
    </div>
  );
}

function friendlyError(err: unknown): string {
  if (err instanceof ApiError) {
    return `${err.status} ${err.message}${err.bodyText ? ` — ${err.bodyText.trim()}` : ""}`;
  }
  if (err instanceof Error) return err.message;
  return String(err);
}
