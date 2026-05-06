// ChartActionDialog — install + upgrade workflow modal (#74 / #75 / #76).
//
// Two-column "proposal ↔ rendered" layout. Left rail collects the
// inputs (chart ref, version, target, values); right panel shows
// helm's response (chart metadata, manifests, diff, or RBAC denial).
//
// Mode prop is the discriminant. install mode collects ns + release
// name; upgrade mode reads them from props (URL params upstream).
// The right panel's default tab differs by mode: install defaults to
// "manifest" (what would be created); upgrade defaults to "diff"
// (what would change). Both modes share the same components.
//
// Pre-flight RBAC denials replace the right panel entirely — no tabs,
// no diff. The action button shows "install · blocked by rbac" until
// the operator's role is broadened.
//
// Equivalent helm CLI footer is the unforgettable move: a live-updating
// copyable shell command that operators can audit or run themselves.
// Matches the dashboard's voice — "we're not hiding anything; here's
// the command this dialog would run."

import { useEffect, useMemo, useState } from "react";
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
  HelmDiffResponse,
  HelmManifestObject,
  PreviewDenial,
  PreviewResponse,
} from "../../lib/types";
import type { JSONSchema } from "../../lib/helmSchema";
import { Modal } from "../ui/Modal";
import { ApiError } from "../../lib/api";
import { showToast } from "../../lib/toastBus";
import { HelmValuesEditor } from "./HelmValuesEditor";
import { MonacoYAML } from "./MonacoYAML";
import { InlineDiff } from "../detail/yaml/InlineDiff";
import { cn } from "../../lib/cn";

interface InstallModeProps {
  mode: "install";
}

interface UpgradeModeProps {
  mode: "upgrade";
  releaseName: string;
  namespace: string;
  initialChartRef?: string;
  initialChartName?: string;
  initialValues?: string;
}

type ChartActionDialogProps = (InstallModeProps | UpgradeModeProps) & {
  open: boolean;
  onClose: () => void;
  cluster: string;
};

type RenderedTab = "manifest" | "diff" | "resources";

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

  // Install-mode inputs.
  const [installNamespace, setInstallNamespace] = useState("default");
  const [installReleaseName, setInstallReleaseName] = useState("");

  const targetNamespace =
    props.mode === "upgrade" ? props.namespace : installNamespace;
  const targetReleaseName =
    props.mode === "upgrade" ? props.releaseName : installReleaseName;

  // ── Preview state ─────────────────────────────────────────────
  const [preview, setPreview] = useState<PreviewResponse | null>(null);
  const [activeTab, setActiveTab] = useState<RenderedTab>(
    props.mode === "install" ? "manifest" : "diff",
  );

  // ── Chart fetch ────────────────────────────────────────────────
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

  // Auto-fetch values whenever a version is picked. Better UX than
  // requiring a second click.
  useEffect(() => {
    if (chartRef && version) {
      valuesMutation.mutate(
        { ref: chartRef, chart: chartName || undefined, version },
        {
          onSuccess: (result) => {
            setChart(result);
            // Install mode: replace values with chart defaults.
            // Upgrade mode: keep operator's existing values; only
            // populate from defaults if the editor is currently empty.
            if (props.mode === "install" || !valuesYaml) {
              setValuesYaml(result.values);
            }
            setPreview(null);
          },
          onError: (err) => {
            showToast(`fetch failed: ${friendlyError(err)}`, "error");
          },
        },
      );
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- intentionally narrow
  }, [version]);

  // ── Submit handlers ───────────────────────────────────────────
  const onPreviewClick = () => {
    const onSuccess = (result: PreviewResponse) => {
      setPreview(result);
      // If denials are present, the panel renders the denial state
      // and the active tab is moot. If preview is clean, jump the
      // active tab to "diff" (upgrade) or "manifest" (install) so
      // operator sees the answer without needing to click a tab.
      const denied = result.denied?.length ?? 0;
      if (denied === 0) {
        setActiveTab(props.mode === "install" ? "manifest" : "diff");
      }
    };
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
          onSuccess,
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
          onSuccess,
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
            showToast(msg, result.rolledBack ? "warn" : "success");
            onCloseAndReset();
          },
          onError: (err) => {
            showToast(`upgrade failed: ${friendlyError(err)}`, "error");
          },
        },
      );
    }
  };

  const onCloseAndReset = () => {
    onClose();
    setTimeout(() => {
      setChartRef(props.mode === "upgrade" ? (props.initialChartRef ?? "") : "");
      setChartName(props.mode === "upgrade" ? (props.initialChartName ?? "") : "");
      setVersion("");
      setValuesYaml(props.mode === "upgrade" ? (props.initialValues ?? "") : "");
      setChart(null);
      setInstallNamespace("default");
      setInstallReleaseName("");
      setPreview(null);
      setActiveTab(props.mode === "install" ? "manifest" : "diff");
    }, 0);
  };

  // ── Validation gates ──────────────────────────────────────────
  const valuesReady = chart != null;
  const targetIdentityReady =
    props.mode === "upgrade"
      ? true
      : Boolean(installNamespace.trim()) && Boolean(installReleaseName.trim());
  const previewReady =
    valuesReady && targetIdentityReady && version.length > 0;
  const denied = preview?.denied ?? null;
  const hasDenials = denied != null && denied.length > 0;
  const actionReady = previewReady && !hasDenials;

  // Panel state discriminator drives the right column.
  const panelState: "empty" | "chart" | "preview" | "denied" = !chart
    ? "empty"
    : hasDenials
      ? "denied"
      : preview
        ? "preview"
        : "chart";

  return (
    <Modal
      open={open}
      onClose={onCloseAndReset}
      labelledBy={labelId}
      size="lg"
      panelClassName="max-w-[900px] h-[90vh] flex flex-col"
    >
      {/* ── Header ───────────────────────────────────────────── */}
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

      {/* ── Two-column body: proposal ↔ rendered ───────────────── */}
      <div className="grid min-h-0 flex-1 grid-cols-[40fr_60fr] divide-x divide-border">
        <ProposalRail
          mode={props.mode}
          chartRef={chartRef}
          onChartRefChange={(s) => {
            setChartRef(s);
            setVersion("");
            setChart(null);
            setPreview(null);
          }}
          chartName={chartName}
          onChartNameChange={(s) => {
            setChartName(s);
            setVersion("");
            setChart(null);
            setPreview(null);
          }}
          onFetchClick={() => versions.refetch()}
          versions={versions.data?.versions ?? []}
          versionsLoading={versions.isFetching}
          versionsError={versions.error}
          version={version}
          onVersionChange={setVersion}
          valuesLoading={valuesMutation.isPending}
          valuesError={valuesMutation.error}
          installNamespace={installNamespace}
          onInstallNamespaceChange={(s) => {
            setInstallNamespace(s);
            setPreview(null);
          }}
          installReleaseName={installReleaseName}
          onInstallReleaseNameChange={(s) => {
            setInstallReleaseName(s);
            setPreview(null);
          }}
          chart={chart}
          valuesYaml={valuesYaml}
          onValuesYamlChange={(s) => {
            setValuesYaml(s);
            setPreview(null);
          }}
        />

        <RenderedPanel
          mode={props.mode}
          state={panelState}
          chart={chart}
          preview={preview}
          previewPending={previewPending}
          activeTab={activeTab}
          onTabChange={setActiveTab}
        />
      </div>

      {/* ── Footer: equivalent CLI + action buttons ─────────────── */}
      <footer className="shrink-0 border-t border-border bg-surface-2">
        <EquivalentCLI
          mode={props.mode}
          chartRef={chartRef}
          chartName={chartName}
          version={version}
          namespace={targetNamespace}
          releaseName={targetReleaseName}
          hasValues={valuesYaml.trim().length > 0}
        />
        <div className="flex flex-wrap items-center justify-between gap-x-3 gap-y-2 border-t border-border px-5 py-3">
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
              className={cn(
                "border px-3 py-1 font-mono text-[12px] disabled:cursor-not-allowed disabled:opacity-50",
                hasDenials
                  ? "border-red bg-red/10 text-red"
                  : "border-accent bg-accent text-white",
              )}
              title={hasDenials ? "rbac pre-flight blocked this action — see right panel" : undefined}
            >
              {actionPending
                ? props.mode === "install"
                  ? "installing…"
                  : "upgrading…"
                : hasDenials
                  ? `${props.mode} · blocked by rbac`
                  : props.mode}
            </button>
          </div>
        </div>
      </footer>
    </Modal>
  );
}

// ─── Left rail: ProposalRail ────────────────────────────────────────

interface ProposalRailProps {
  mode: "install" | "upgrade";
  chartRef: string;
  onChartRefChange: (s: string) => void;
  chartName: string;
  onChartNameChange: (s: string) => void;
  onFetchClick: () => void;
  versions: string[];
  versionsLoading: boolean;
  versionsError: unknown;
  version: string;
  onVersionChange: (s: string) => void;
  valuesLoading: boolean;
  valuesError: unknown;
  installNamespace: string;
  onInstallNamespaceChange: (s: string) => void;
  installReleaseName: string;
  onInstallReleaseNameChange: (s: string) => void;
  chart: ChartFetchResult | null;
  valuesYaml: string;
  onValuesYamlChange: (s: string) => void;
}

function ProposalRail(props: ProposalRailProps) {
  const isOCI = props.chartRef.startsWith("oci://");
  const fetchDisabled =
    props.chartRef.length === 0 || (!isOCI && props.chartName.length === 0);
  return (
    <section className="flex min-h-0 flex-col overflow-hidden">
      <Eyebrow text="proposal ·" />
      <div className="flex min-h-0 flex-1 flex-col overflow-y-auto px-5 pb-4">
        {/* Chart reference */}
        <Field label="chart reference">
          <div className="flex flex-wrap gap-2">
            <input
              type="text"
              value={props.chartRef}
              onChange={(e) => props.onChartRefChange(e.target.value)}
              placeholder="oci://… or https://…"
              className="min-w-0 flex-1 rounded-sm border border-border bg-bg px-2.5 py-1.5 font-mono text-[12.5px] text-ink placeholder:text-ink-faint focus:border-accent focus:outline-none"
            />
            <button
              type="button"
              onClick={props.onFetchClick}
              disabled={fetchDisabled}
              className="border border-accent bg-accent px-3 py-1 font-mono text-[12px] text-white disabled:opacity-50"
            >
              {props.versionsLoading ? "fetching…" : "fetch"}
            </button>
          </div>
          {!isOCI && props.chartRef.length > 0 ? (
            <input
              type="text"
              value={props.chartName}
              onChange={(e) => props.onChartNameChange(e.target.value)}
              placeholder="chart name (e.g. nginx)"
              className="mt-2 w-full rounded-sm border border-border bg-bg px-2.5 py-1.5 font-mono text-[12.5px] text-ink placeholder:text-ink-faint focus:border-accent focus:outline-none"
            />
          ) : null}
          {props.versionsError ? (
            <p className="mt-1 font-mono text-[11.5px] text-red">
              {friendlyError(props.versionsError)}
            </p>
          ) : null}
        </Field>

        {/* Version */}
        {props.versions.length > 0 ? (
          <Field label="version">
            <select
              value={props.version}
              onChange={(e) => props.onVersionChange(e.target.value)}
              disabled={props.valuesLoading}
              className="w-full rounded-sm border border-border bg-bg px-2.5 py-1.5 font-mono text-[12.5px] text-ink focus:border-accent focus:outline-none disabled:opacity-50"
            >
              <option value="">— pick a version ({props.versions.length} available) —</option>
              {props.versions.map((v) => (
                <option key={v} value={v}>
                  {v}
                </option>
              ))}
            </select>
            {props.valuesLoading ? (
              <p className="mt-1 font-mono text-[11.5px] text-ink-faint">loading values…</p>
            ) : null}
            {props.valuesError ? (
              <p className="mt-1 font-mono text-[11.5px] text-red">
                {friendlyError(props.valuesError)}
              </p>
            ) : null}
          </Field>
        ) : null}

        {/* Target identity */}
        {props.mode === "install" && props.chart ? (
          <Field label="target">
            <div className="grid grid-cols-2 gap-2">
              <input
                type="text"
                value={props.installNamespace}
                onChange={(e) => props.onInstallNamespaceChange(e.target.value)}
                placeholder="namespace"
                className="min-w-0 rounded-sm border border-border bg-bg px-2.5 py-1.5 font-mono text-[12.5px] text-ink placeholder:text-ink-faint focus:border-accent focus:outline-none"
              />
              <input
                type="text"
                value={props.installReleaseName}
                onChange={(e) => props.onInstallReleaseNameChange(e.target.value)}
                placeholder="release name"
                className="min-w-0 rounded-sm border border-border bg-bg px-2.5 py-1.5 font-mono text-[12.5px] text-ink placeholder:text-ink-faint focus:border-accent focus:outline-none"
              />
            </div>
          </Field>
        ) : null}

        {/* Values editor — sized to fill remaining space */}
        {props.chart ? (
          <Field label="values" grow>
            <HelmValuesEditor
              valuesYaml={props.valuesYaml}
              schema={props.chart.schema as JSONSchema | undefined}
              onValuesYamlChange={props.onValuesYamlChange}
            />
          </Field>
        ) : null}
      </div>
    </section>
  );
}

// ─── Right panel: RenderedPanel ─────────────────────────────────────

interface RenderedPanelProps {
  mode: "install" | "upgrade";
  state: "empty" | "chart" | "preview" | "denied";
  chart: ChartFetchResult | null;
  preview: PreviewResponse | null;
  previewPending: boolean;
  activeTab: RenderedTab;
  onTabChange: (t: RenderedTab) => void;
}

function RenderedPanel({ mode, state, chart, preview, previewPending, activeTab, onTabChange }: RenderedPanelProps) {
  return (
    <section className="flex min-h-0 flex-col overflow-hidden bg-surface">
      <Eyebrow text={state === "denied" ? "rbac →" : "rendered →"} arrow={state === "denied" ? false : true}>
        {state === "preview" && preview ? (
          <RenderedTabStrip
            mode={mode}
            active={activeTab}
            onChange={onTabChange}
            counts={{
              manifests: preview.manifests.length,
              changes: preview.diff?.changes.length ?? 0,
            }}
          />
        ) : null}
      </Eyebrow>
      <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
        {state === "empty" ? (
          <EmptyHint mode={mode} />
        ) : state === "chart" && chart ? (
          <ChartMetadataPanel chart={chart} previewPending={previewPending} />
        ) : state === "preview" && preview ? (
          <PreviewContent mode={mode} preview={preview} activeTab={activeTab} />
        ) : state === "denied" && preview ? (
          <DenialPane denied={preview.denied ?? []} />
        ) : null}
      </div>
    </section>
  );
}

// ─── EmptyHint: no chart fetched yet ───────────────────────────────

function EmptyHint({ mode }: { mode: "install" | "upgrade" }) {
  return (
    <div className="flex flex-1 items-center justify-center px-8 py-10">
      <p className="max-w-xs font-display text-[18px] italic leading-tight text-ink-faint">
        {mode === "install"
          ? "paste a chart reference to begin →"
          : "edit the chart reference above to start an upgrade preview →"}
      </p>
    </div>
  );
}

// ─── ChartMetadataPanel: chart fetched, no preview yet ─────────────

function ChartMetadataPanel({ chart, previewPending }: { chart: ChartFetchResult; previewPending: boolean }) {
  return (
    <div className="flex flex-1 flex-col overflow-y-auto px-5 py-5 space-y-4">
      <div>
        <h3 className="font-display text-[24px] italic leading-tight text-ink">
          {chart.meta.name}{" "}
          <span className="font-mono text-[14px] not-italic text-ink-muted">
            {chart.meta.version}
          </span>
        </h3>
        {chart.meta.description ? (
          <p className="mt-2 max-w-prose text-[13px] leading-relaxed text-ink-muted">
            {chart.meta.description}
          </p>
        ) : null}
      </div>

      <dl className="grid grid-cols-[120px_1fr] gap-x-4 gap-y-1.5 font-mono text-[11.5px]">
        {chart.meta.appVersion ? (
          <MetaRow label="app version" value={chart.meta.appVersion} />
        ) : null}
        {chart.meta.kubeVersion ? (
          <MetaRow label="k8s constraint" value={chart.meta.kubeVersion} />
        ) : null}
        {chart.meta.type ? <MetaRow label="type" value={chart.meta.type} /> : null}
        <MetaRow
          label="schema"
          value={chart.schema ? "yes (form mode)" : "none (yaml mode)"}
          tone={chart.schema ? "data" : "muted"}
        />
        {chart.meta.maintainers && chart.meta.maintainers.length > 0 ? (
          <MetaRow
            label="maintainers"
            value={chart.meta.maintainers
              .map((m) => m.name ?? m.email ?? "")
              .filter(Boolean)
              .join(", ")}
          />
        ) : null}
      </dl>

      <div className="mt-2 rounded-sm border border-dashed border-border-strong px-4 py-3">
        <p className="font-mono text-[11.5px] text-ink-muted">
          {previewPending ? (
            <>rendering preview…</>
          ) : (
            <>
              edit values on the left, then click{" "}
              <span className="text-ink">preview</span> below to render
              the manifests + rbac pre-flight
              <span className="text-ink-faint"> + diff vs current state</span>.
            </>
          )}
        </p>
      </div>
    </div>
  );
}

function MetaRow({
  label,
  value,
  tone = "default",
}: {
  label: string;
  value: string;
  tone?: "default" | "muted" | "data";
}) {
  return (
    <>
      <dt className="text-ink-faint uppercase tracking-[0.16em] text-[10.5px]">{label}</dt>
      <dd
        className={cn(
          tone === "data"
            ? "text-data"
            : tone === "muted"
              ? "text-ink-faint"
              : "text-ink",
        )}
      >
        {value}
      </dd>
    </>
  );
}

// ─── PreviewContent: tab body for preview state ────────────────────

function PreviewContent({
  mode,
  preview,
  activeTab,
}: {
  mode: "install" | "upgrade";
  preview: PreviewResponse;
  activeTab: RenderedTab;
}) {
  // Upgrade mode without a diff payload (e.g. from-release-fetch
  // failed) collapses to manifest view.
  const effectiveTab: RenderedTab =
    activeTab === "diff" && (mode === "install" || !preview.diff)
      ? "manifest"
      : activeTab;
  if (effectiveTab === "diff" && preview.diff) {
    return <DiffTab diff={preview.diff} />;
  }
  if (effectiveTab === "manifest") {
    return <ManifestTab yaml={preview.manifestYaml} />;
  }
  return <ResourcesTab manifests={preview.manifests} />;
}

function DiffTab({ diff }: { diff: HelmDiffResponse }) {
  const counts = useMemo(() => {
    return diff.changes.reduce(
      (acc, c) => {
        acc[c.kind] = (acc[c.kind] ?? 0) + 1;
        return acc;
      },
      {} as Record<string, number>,
    );
  }, [diff.changes]);
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="border-b border-border px-5 py-2 font-mono text-[11.5px]">
        <span className="text-ink">
          {diff.changes.length} change{diff.changes.length === 1 ? "" : "s"}
        </span>
        {Object.entries(counts).map(([k, v]) => (
          <span key={k} className="ml-3 text-ink-muted">
            <span
              className={cn(
                k === "add"
                  ? "text-data"
                  : k === "remove"
                    ? "text-red"
                    : "text-ink-muted",
              )}
            >
              {k}
            </span>
            : {v}
          </span>
        ))}
      </div>
      <div className="min-h-0 flex-1">
        <InlineDiff original={diff.from.yaml} proposed={diff.to.yaml} />
      </div>
    </div>
  );
}

function ManifestTab({ yaml }: { yaml: string }) {
  if (!yaml) {
    return (
      <div className="flex flex-1 items-center justify-center px-8 py-10">
        <p className="font-mono text-[12px] italic text-ink-faint">
          chart rendered no manifests
        </p>
      </div>
    );
  }
  return (
    <div className="min-h-0 flex-1">
      <MonacoYAML value={yaml} emptyLabel="no manifests rendered" />
    </div>
  );
}

function ResourcesTab({ manifests }: { manifests: HelmManifestObject[] }) {
  if (manifests.length === 0) {
    return (
      <div className="flex flex-1 items-center justify-center px-8 py-10">
        <p className="font-mono text-[12px] italic text-ink-faint">
          no resources rendered
        </p>
      </div>
    );
  }
  // Group by Kind for an at-a-glance grouping.
  const byKind = manifests.reduce(
    (acc, m) => {
      const k = m.kind || "Unknown";
      acc[k] = acc[k] ?? [];
      acc[k].push(m);
      return acc;
    },
    {} as Record<string, HelmManifestObject[]>,
  );
  const kindsSorted = Object.keys(byKind).sort();
  return (
    <div className="flex-1 overflow-y-auto px-5 py-4 font-mono text-[12px]">
      {kindsSorted.map((kind) => (
        <div key={kind} className="mb-4">
          <h4 className="mb-1 text-ink-faint uppercase tracking-[0.16em] text-[10.5px]">
            {kind} <span className="ml-1 text-ink-muted">({byKind[kind].length})</span>
          </h4>
          <ul className="space-y-0.5 text-ink-muted">
            {byKind[kind].map((m, i) => (
              <li key={i}>
                <span className="text-ink-faint">{m.apiVersion}</span>
                {m.namespace ? (
                  <span className="text-ink-faint"> · {m.namespace}/</span>
                ) : (
                  <span className="text-ink-faint"> · </span>
                )}
                <span className="text-ink">{m.name}</span>
              </li>
            ))}
          </ul>
        </div>
      ))}
    </div>
  );
}

// ─── DenialPane: full-bleed RBAC denial state ───────────────────────

function DenialPane({ denied }: { denied: PreviewDenial[] }) {
  return (
    <div className="flex-1 overflow-y-auto px-5 py-5">
      <h3 className="font-display text-[22px] italic leading-tight text-ink">
        <span className="text-red">✕</span> apiserver would reject this operation.
      </h3>
      <p className="mt-2 max-w-prose text-[13px] text-ink-muted">
        Your role lacks the following verbs. Periscope ran a per-manifest
        SelfSubjectAccessReview before opening this dialog's diff to spare
        you a half-applied install.
      </p>

      <ul className="mt-4 space-y-2">
        {denied.map((d, i) => (
          <li
            key={i}
            className="border-l-[3px] border-red bg-red/5 px-3 py-2 font-mono text-[11.5px]"
          >
            <div className="text-ink">
              <span className="text-red">{d.verb}</span>{" "}
              <span className="text-ink-muted">
                {d.group ? `${d.group}/` : ""}
              </span>
              <span className="text-ink">{d.resource}</span>
              {d.namespace ? (
                <span className="text-ink-muted"> in namespace {d.namespace}</span>
              ) : null}
              {d.name ? (
                <span className="text-ink-muted"> ({d.name})</span>
              ) : null}
            </div>
            <div className="text-ink-faint">→ server: {d.reason}</div>
          </li>
        ))}
      </ul>

      <p className="mt-5 font-mono text-[11.5px] text-ink-muted">
        See{" "}
        <a
          href="https://github.com/gnana997/periscope/blob/main/docs/setup/cluster-rbac.md"
          target="_blank"
          rel="noreferrer"
          className="text-ink underline decoration-dotted decoration-accent underline-offset-[3px] hover:text-accent"
        >
          docs/setup/cluster-rbac.md
        </a>{" "}
        for the periscope-tier:write role definition.
      </p>
    </div>
  );
}

// ─── EquivalentCLI: copyable shell command ──────────────────────────

interface EquivalentCLIProps {
  mode: "install" | "upgrade";
  chartRef: string;
  chartName: string;
  version: string;
  namespace: string;
  releaseName: string;
  hasValues: boolean;
}

function EquivalentCLI({
  mode,
  chartRef,
  chartName,
  version,
  namespace,
  releaseName,
  hasValues,
}: EquivalentCLIProps) {
  const command = useMemo(
    () => buildHelmCommand({ mode, chartRef, chartName, version, namespace, releaseName, hasValues }),
    [mode, chartRef, chartName, version, namespace, releaseName, hasValues],
  );
  const [copied, setCopied] = useState(false);
  const onCopy = async () => {
    try {
      await navigator.clipboard.writeText(command);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      showToast("clipboard copy failed", "error");
    }
  };
  return (
    <div className="flex items-start gap-3 px-5 py-2">
      <pre className="flex-1 overflow-x-auto whitespace-pre font-mono text-[11.5px] leading-[1.55] text-ink-muted">
        <span className="text-ink-faint select-none">$ </span>
        {command}
      </pre>
      <button
        type="button"
        onClick={onCopy}
        className="shrink-0 border border-border px-2 py-0.5 font-mono text-[10.5px] text-ink-faint hover:border-border-strong hover:text-ink"
        title="copy equivalent helm command"
      >
        {copied ? "copied" : "⎘ copy"}
      </button>
    </div>
  );
}

function buildHelmCommand({
  mode,
  chartRef,
  chartName,
  version,
  namespace,
  releaseName,
  hasValues,
}: EquivalentCLIProps): string {
  // Empty-state placeholder so the footer is never blank — operators
  // see the shape of the eventual command from the moment the dialog
  // opens.
  if (!chartRef && !releaseName) {
    return mode === "install"
      ? "helm install <release> <chart-ref> --version <v> --namespace <ns> --atomic --wait"
      : "helm upgrade <release> <chart-ref> --version <v> --namespace <ns> --atomic --wait";
  }
  const release = releaseName || "<release>";
  const ns = namespace || "<namespace>";
  const ver = version || "<version>";
  const isOCI = chartRef.startsWith("oci://");
  // For HTTP repos, helm v3 supports `--repo <url>` to reference a
  // chart by name; for OCI, the chart name is part of the ref.
  const chartArg = isOCI ? chartRef : chartName || "<chart>";
  const parts = [
    `helm ${mode}`,
    release,
    chartArg,
    `--version ${ver}`,
    `--namespace ${ns}`,
    "--atomic",
    "--wait",
  ];
  if (!isOCI && chartRef) {
    parts.push(`--repo ${chartRef}`);
  }
  if (hasValues) {
    parts.push("-f values.yaml");
  }
  // Soft-wrap with backslash continuations after the chart args so
  // the command stays readable when it overflows.
  return parts.slice(0, 3).join(" ") + " \\\n    " + parts.slice(3).join(" ");
}

// ─── Small layout primitives ────────────────────────────────────────

function Eyebrow({
  text,
  arrow,
  children,
}: {
  text: string;
  arrow?: boolean;
  children?: React.ReactNode;
}) {
  return (
    <div className="flex shrink-0 items-center justify-between gap-3 border-b border-border bg-bg/40 px-5 py-2">
      <span
        className={cn(
          "font-mono text-[10.5px] uppercase tracking-[0.18em]",
          arrow ? "text-accent" : "text-ink-faint",
        )}
      >
        {text}
      </span>
      {children}
    </div>
  );
}

function Field({
  label,
  children,
  grow,
}: {
  label: string;
  children: React.ReactNode;
  grow?: boolean;
}) {
  return (
    <div className={cn("mt-4", grow && "flex min-h-0 flex-1 flex-col")}>
      <label className="font-mono text-[10.5px] uppercase tracking-[0.18em] text-ink-faint">
        {label}
      </label>
      <div className={cn("mt-1.5", grow && "flex min-h-0 flex-1 flex-col")}>
        {children}
      </div>
    </div>
  );
}

function RenderedTabStrip({
  mode,
  active,
  onChange,
  counts,
}: {
  mode: "install" | "upgrade";
  active: RenderedTab;
  onChange: (t: RenderedTab) => void;
  counts: { manifests: number; changes: number };
}) {
  const tabs: { id: RenderedTab; label: string; count?: number }[] = [
    ...(mode === "upgrade"
      ? [{ id: "diff" as const, label: "diff", count: counts.changes }]
      : []),
    { id: "manifest" as const, label: "manifest" },
    { id: "resources" as const, label: "resources", count: counts.manifests },
  ];
  return (
    <div className="flex items-center gap-1">
      {tabs.map((t) => (
        <button
          key={t.id}
          type="button"
          onClick={() => onChange(t.id)}
          className={cn(
            "border-b-2 px-2 py-0.5 font-mono text-[11px] transition-colors",
            active === t.id
              ? "border-accent text-accent"
              : "border-transparent text-ink-muted hover:text-ink",
          )}
        >
          {t.label}
          {t.count != null ? (
            <span className="ml-1 text-ink-faint">({t.count})</span>
          ) : null}
        </button>
      ))}
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
