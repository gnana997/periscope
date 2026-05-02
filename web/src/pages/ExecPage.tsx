import { useEffect } from "react";
import { useNavigate, useParams, useSearchParams } from "react-router-dom";
import { useExecSessions } from "../exec/useExecSessions";

/**
 * ExecPage is a redirect-style route. The drawer is the actual UI for
 * exec — when someone hits a deep link like
 *
 *   /clusters/{cluster}/pods/{ns}/{name}/exec?container=app
 *
 * we open a drawer session with those params and replace the URL with the
 * pods list so the back button doesn't fight the drawer state.
 */
export function ExecPage({ cluster }: { cluster: string }) {
  const { ns, name } = useParams<{ ns: string; name: string }>();
  const [search] = useSearchParams();
  const { openSession } = useExecSessions();
  const navigate = useNavigate();

  useEffect(() => {
    if (!ns || !name) return;
    const container = search.get("container") || undefined;
    openSession({ cluster, namespace: ns, pod: name, container });
    // Replace so back-button leaves the drawer in place rather than
    // re-opening another session.
    navigate(
      `/clusters/${cluster}/pods?selNs=${encodeURIComponent(ns)}&sel=${encodeURIComponent(
        name,
      )}&tab=describe`,
      { replace: true },
    );
    // We deliberately do not depend on openSession/navigate — those are
    // referentially stable enough for this one-shot effect.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [cluster, ns, name]);

  return (
    <div className="flex h-full items-center justify-center bg-bg">
      <div className="flex items-center gap-3 font-mono text-[12px] text-ink-muted">
        <span
          aria-hidden
          className="block size-3 animate-spin rounded-full border-[1.5px] border-border-strong border-t-accent"
        />
        opening shell…
      </div>
    </div>
  );
}
