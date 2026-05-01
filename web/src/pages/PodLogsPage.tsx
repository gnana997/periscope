import { useMemo } from "react";
import { Link, useParams, useSearchParams } from "react-router-dom";
import { PageHeader } from "../components/page/PageHeader";
import { ErrorState } from "../components/table/states";
import { PodLogsView } from "../components/logs/PodLogsView";
import {
  paramsToState,
  stateToParams,
  type LogsViewState,
} from "../components/logs/logsState";

// Full-page logs view, mounted at /clusters/:c/pods/:ns/:name/logs.
// State is round-tripped through query params so this URL is shareable.
// The same view shows up in-tab on the Pods page (PodLogsTab); both mount
// the underlying PodLogsView component.
export function PodLogsPage({ cluster }: { cluster: string }) {
  const { ns, name } = useParams<{ ns: string; name: string }>();
  const [params, setParams] = useSearchParams();

  const state = useMemo(() => paramsToState(params), [params]);

  if (!ns || !name) {
    return (
      <div className="flex h-full items-center justify-center bg-bg">
        <ErrorState title="missing pod" message="namespace or name not in URL" />
      </div>
    );
  }

  const onStateChange = (next: Partial<LogsViewState>) => {
    const merged = { ...state, ...next };
    setParams(stateToParams(merged), { replace: true });
  };

  const podsListPath = `/clusters/${encodeURIComponent(cluster)}/pods`;

  return (
    <div className="flex h-full min-h-0 flex-col">
      <PageHeader
        title="Logs"
        subtitle={`${name} · ${ns}`}
        trailing={
          <Link
            to={podsListPath}
            className="rounded-md border border-border bg-surface-2/40 px-2 py-1 font-mono text-[11px] text-ink-muted transition-colors hover:border-border-strong hover:text-ink"
          >
            ← back to pods
          </Link>
        }
      />
      <PodLogsView
        cluster={cluster}
        namespace={ns}
        name={name}
        state={state}
        onStateChange={onStateChange}
      />
    </div>
  );
}
