import { useState } from "react";
import { TerminalSquare } from "lucide-react";
import { useClusters } from "../../hooks/useClusters";
import { useExecSessions } from "../../exec/useExecSessions";
import { CapReachedDialog } from "../../exec/CapReachedDialog";

/**
 * OpenNodeShellButton — node-detail action that opens (or focuses) an
 * SSM node-shell session in the global drawer (issue #105).
 *
 * Hides entirely when the backend reports nodeShellEnabled=false. The
 * server enforces the real gates (tier membership, per-user STS trust
 * policy, that the node is an EC2 instance); those surface as a failed
 * connection / error frame in the session rather than a pre-flight here,
 * matching the cluster-shell button.
 */

interface OpenNodeShellButtonProps {
  cluster: string;
  node: string;
}

export function OpenNodeShellButton({
  cluster,
  node,
}: OpenNodeShellButtonProps) {
  const { data: clustersData } = useClusters();
  const { openSession } = useExecSessions();

  const clusterMeta = clustersData?.clusters.find((c) => c.name === cluster);
  // "field absent" (older backend) → disabled; the feature is opt-in and
  // we don't want a button that always 404s.
  const enabled = clusterMeta?.nodeShellEnabled === true;

  const [capReached, setCapReached] = useState(false);

  function open() {
    const result = openSession({ kind: "node-shell", cluster, node });
    if (!result.ok && result.reason === "cap_reached") {
      setCapReached(true);
    }
  }

  if (!enabled || !node) return null;

  return (
    <>
      <button
        type="button"
        onClick={open}
        title="Open node shell (SSM)"
        className="group inline-flex items-center gap-1.5 rounded-md border border-border px-2.5 py-1 font-mono text-[11.5px] font-medium text-ink-muted transition-colors hover:border-accent hover:text-accent"
      >
        <TerminalSquare
          aria-hidden
          className="size-3.5 text-ink-faint transition-colors group-hover:text-accent"
        />
        <span>node shell</span>
      </button>
      <CapReachedDialog open={capReached} onClose={() => setCapReached(false)} />
    </>
  );
}
