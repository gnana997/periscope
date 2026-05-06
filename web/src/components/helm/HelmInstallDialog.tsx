// HelmInstallDialog — the install-chart workflow modal (#74, scope B).
//
// Single-pane top-to-bottom flow:
//
//   1. Operator pastes a chart ref (HTTPS chart-repo URL or oci://...)
//   2. For HTTP repos, also enters a chart name (the index entry key)
//   3. Clicks Fetch → useChartVersions fires, version dropdown appears
//   4. Picks a version → useChartValuesMutation fires, values + schema land
//   5. Edits values via HelmValuesEditor
//   6. Footer: Cancel / Dry-run preview (disabled stub) / Install (disabled stub)
//
// Why disabled stubs at the bottom: the dry-run + install action
// backends are sibling issues under epic #72 and ship in their own
// PRs. The stubs keep the dialog "complete" visually so reviewers
// see the end-state without dead UI surprises later.

import { useState } from "react";
import {
  useChartValuesMutation,
  useChartVersions,
} from "../../hooks/useChartFetch";
import type { ChartFetchResult } from "../../lib/types";
import type { JSONSchema } from "../../lib/helmSchema";
import { Modal } from "../ui/Modal";
import { ApiError } from "../../lib/api";
import { showToast } from "../../lib/toastBus";
import { HelmValuesEditor } from "./HelmValuesEditor";

interface HelmInstallDialogProps {
  open: boolean;
  onClose: () => void;
  cluster: string;
}

export function HelmInstallDialog({ open, onClose, cluster }: HelmInstallDialogProps) {
  const labelId = "helm-install-dialog-title";

  const [chartRef, setChartRef] = useState("");
  const [chartName, setChartName] = useState("");
  const [version, setVersion] = useState("");
  const [valuesYaml, setValuesYaml] = useState("");
  const [chart, setChart] = useState<ChartFetchResult | null>(null);

  const isOCI = chartRef.startsWith("oci://");
  // Versions auto-fire when ref looks plausible — for HTTP refs we
  // also need the chart name; for OCI the name is in the ref.
  const versionsEnabled =
    chartRef.length > 0 && (isOCI || chartName.length > 0);

  const versions = useChartVersions({
    cluster,
    ref: chartRef,
    chartName,
    enabled: versionsEnabled,
  });

  const valuesMutation = useChartValuesMutation(cluster);

  const fetchVersionsClick = () => {
    // Force refetch on click — the auto-fire useChartVersions hook
    // will already have run if the ref looks valid, but this
    // covers the "operator typed something then waited for the
    // button" path.
    versions.refetch();
  };

  const fetchValuesClick = () => {
    if (!chartRef || !version) return;
    valuesMutation.mutate(
      { ref: chartRef, chart: chartName || undefined, version },
      {
        onSuccess: (result) => {
          setChart(result);
          setValuesYaml(result.values);
        },
        onError: (err) => {
          showToast(`fetch failed: ${friendlyError(err)}`, "error");
        },
      },
    );
  };

  const onCloseAndReset = () => {
    onClose();
    // Defer reset to next tick so the modal close animation runs
    // against the current state, not blank fields. (Modal.tsx
    // unmounts the panel; state lives on the closing edge briefly.)
    setTimeout(() => {
      setChartRef("");
      setChartName("");
      setVersion("");
      setValuesYaml("");
      setChart(null);
    }, 0);
  };

  return (
    <Modal
      open={open}
      onClose={onCloseAndReset}
      labelledBy={labelId}
      size="lg"
      // max-h + flex column so the long values form scrolls within
      // the modal instead of spilling past the viewport. Header and
      // footer stay pinned via shrink-0; body grows + scrolls.
      panelClassName="max-h-[90vh] flex flex-col"
    >
      <header className="flex shrink-0 items-baseline justify-between border-b border-border px-5 py-3">
        <h2 id={labelId} className="font-display text-[20px] italic text-ink">
          Install Helm chart{" "}
          <span className="text-ink-muted">·</span>{" "}
          <span className="font-mono text-[14px] text-ink-muted not-italic">
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

      {/* min-h-0 is the load-bearing class here — without it, flex-1
          children of overflowing flex columns refuse to shrink (the
          default min-height: auto holds the child at content size and
          overflow-y-auto silently no-ops). */}
      <div className="min-h-0 flex-1 overflow-y-auto space-y-4 px-5 py-4">
        {/* 1. Ref input */}
        <RefInputBlock
          chartRef={chartRef}
          chartName={chartName}
          onRefChange={(next) => {
            setChartRef(next);
            // Reset downstream picks when ref changes meaningfully.
            setVersion("");
            setChart(null);
            setValuesYaml("");
          }}
          onChartNameChange={(next) => {
            setChartName(next);
            setVersion("");
            setChart(null);
            setValuesYaml("");
          }}
          onFetchClick={fetchVersionsClick}
          versionsLoading={versions.isFetching}
          versionsError={versions.error}
        />

        {/* 2. Versions dropdown — only renders once versions resolve */}
        {versions.data && versions.data.versions.length > 0 ? (
          <VersionPickerBlock
            versions={versions.data.versions}
            value={version}
            onChange={setVersion}
            onFetchValues={fetchValuesClick}
            valuesLoading={valuesMutation.isPending}
            valuesError={valuesMutation.error}
          />
        ) : null}

        {/* 3. Values editor — only after a successful values fetch */}
        {chart ? (
          <ChartHeaderBlock chart={chart} />
        ) : null}
        {chart ? (
          <HelmValuesEditor
            valuesYaml={valuesYaml}
            schema={chart.schema as JSONSchema | undefined}
            onValuesYamlChange={setValuesYaml}
          />
        ) : null}
      </div>

      <footer className="flex shrink-0 flex-wrap items-center justify-between gap-x-3 gap-y-2 border-t border-border bg-surface-2 px-5 py-3">
        <span className="font-mono text-[11px] text-ink-faint">
          v1.1: chart fetch + values editor only · install / dry-run land in follow-up PRs
        </span>
        <div className="flex flex-wrap items-center gap-2">
          <button
            type="button"
            onClick={onCloseAndReset}
            className="border border-border px-3 py-1 font-mono text-[12px] text-ink-muted hover:text-ink"
          >
            cancel
          </button>
          <button
            type="button"
            disabled
            title="dry-run preview lands in a follow-up PR (#72 epic)"
            className="border border-border px-3 py-1 font-mono text-[12px] text-ink-faint cursor-not-allowed"
          >
            dry-run preview
          </button>
          <button
            type="button"
            disabled
            title="install action lands in a follow-up PR (#72 epic)"
            className="border border-accent bg-accent/40 px-3 py-1 font-mono text-[12px] text-white cursor-not-allowed"
          >
            install
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
  onFetchValues,
  valuesLoading,
  valuesError,
}: {
  versions: string[];
  value: string;
  onChange: (v: string) => void;
  onFetchValues: () => void;
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
          className="min-w-0 flex-1 rounded-sm border border-border bg-bg px-2.5 py-1.5 font-mono text-[12.5px] text-ink focus:border-accent focus:outline-none"
        >
          <option value="">— pick a version ({versions.length} available) —</option>
          {versions.map((v) => (
            <option key={v} value={v}>
              {v}
            </option>
          ))}
        </select>
        <button
          type="button"
          disabled={!value || valuesLoading}
          onClick={onFetchValues}
          className="border border-accent bg-accent px-3 py-1 font-mono text-[12px] text-white disabled:opacity-50"
        >
          {valuesLoading ? "loading…" : "load values"}
        </button>
      </div>
      {valuesError ? (
        <p className="font-mono text-[12px] text-red">
          {friendlyError(valuesError)}
        </p>
      ) : null}
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

function friendlyError(err: unknown): string {
  if (err instanceof ApiError) {
    return `${err.status} ${err.message}${err.bodyText ? ` — ${err.bodyText.trim()}` : ""}`;
  }
  if (err instanceof Error) return err.message;
  return String(err);
}
