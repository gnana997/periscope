import { useMemo } from "react";
import { Link, useParams, useSearchParams } from "react-router-dom";
import { PageHeader } from "../components/page/PageHeader";
import { ErrorState } from "../components/table/states";
import { DeploymentLogsView } from "../components/logs/DeploymentLogsView";
import {
  paramsToState,
  stateToParams,
  type LogsViewState,
} from "../components/logs/logsState";

// Full-page deployment logs view, mounted at
// /clusters/:c/deployments/:ns/:name/logs. State round-trips through query
// params so this URL is shareable. The same view renders in-tab on the
// Deployments page (DeploymentLogsTab); both mount DeploymentLogsView.
export function DeploymentLogsPage({ cluster }: { cluster: string }) {
  const { ns, name } = useParams<{ ns: string; name: string }>();
  const [params, setParams] = useSearchParams();

  const state = useMemo(() => paramsToState(params), [params]);

  if (!ns || !name) {
    return (
      <div className="flex h-full items-center justify-center bg-bg">
        <ErrorState
          title="missing deployment"
          message="namespace or name not in URL"
        />
      </div>
    );
  }

  const onStateChange = (next: Partial<LogsViewState>) => {
    const merged = { ...state, ...next };
    setParams(stateToParams(merged), { replace: true });
  };

  const listPath = `/clusters/${encodeURIComponent(cluster)}/deployments`;

  return (
    <div className="flex h-full min-h-0 flex-col">
      <PageHeader
        title="Logs"
        subtitle={`${name} · ${ns} · deployment`}
        trailing={
          <Link
            to={listPath}
            className="rounded-md border border-border bg-surface-2/40 px-2 py-1 font-mono text-[11px] text-ink-muted transition-colors hover:border-border-strong hover:text-ink"
          >
            ← back to deployments
          </Link>
        }
      />
      <DeploymentLogsView
        cluster={cluster}
        namespace={ns}
        name={name}
        state={state}
        onStateChange={onStateChange}
      />
    </div>
  );
}
