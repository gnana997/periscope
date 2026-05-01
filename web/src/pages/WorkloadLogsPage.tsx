import { useMemo } from "react";
import { Link, useParams, useSearchParams } from "react-router-dom";
import { PageHeader } from "../components/page/PageHeader";
import { ErrorState } from "../components/table/states";
import { WorkloadLogsView } from "../components/logs/WorkloadLogsView";
import type { WorkloadKind } from "../components/logs/PodFilterStrip";
import {
  paramsToState,
  stateToParams,
  type LogsViewState,
} from "../components/logs/logsState";

const LIST_PATHS: Record<WorkloadKind, string> = {
  deployment: "deployments",
  statefulset: "statefulsets",
  daemonset: "daemonsets",
  job: "jobs",
};

const KIND_LABELS: Record<WorkloadKind, string> = {
  deployment: "deployment",
  statefulset: "statefulset",
  daemonset: "daemonset",
  job: "job",
};

// WorkloadLogsPage is the full-page logs view for any controller kind
// (deployment / sts / ds / job). State round-trips through query params
// so the URL is shareable. The same view renders in-tab on each
// resource's list page (WorkloadLogsTab); both mount WorkloadLogsView.
export function WorkloadLogsPage({
  kind,
  cluster,
}: {
  kind: WorkloadKind;
  cluster: string;
}) {
  const { ns, name } = useParams<{ ns: string; name: string }>();
  const [params, setParams] = useSearchParams();

  const state = useMemo(() => paramsToState(params), [params]);

  if (!ns || !name) {
    return (
      <div className="flex h-full items-center justify-center bg-bg">
        <ErrorState
          title={`missing ${KIND_LABELS[kind]}`}
          message="namespace or name not in URL"
        />
      </div>
    );
  }

  const onStateChange = (next: Partial<LogsViewState>) => {
    const merged = { ...state, ...next };
    setParams(stateToParams(merged), { replace: true });
  };

  const listSegment = LIST_PATHS[kind];
  const listPath = `/clusters/${encodeURIComponent(cluster)}/${listSegment}`;

  return (
    <div className="flex h-full min-h-0 flex-col">
      <PageHeader
        title="Logs"
        subtitle={`${name} · ${ns} · ${KIND_LABELS[kind]}`}
        trailing={
          <Link
            to={listPath}
            className="rounded-md border border-border bg-surface-2/40 px-2 py-1 font-mono text-[11px] text-ink-muted transition-colors hover:border-border-strong hover:text-ink"
          >
            ← back to {listSegment}
          </Link>
        }
      />
      <WorkloadLogsView
        kind={kind}
        cluster={cluster}
        namespace={ns}
        name={name}
        state={state}
        onStateChange={onStateChange}
      />
    </div>
  );
}
