import { useEffect, useState } from "react";
import { Terminal } from "lucide-react";
import { useClusters } from "../../hooks/useClusters";
import { useExecSessions } from "../../exec/useExecSessions";
import { CapReachedDialog } from "../../exec/CapReachedDialog";
import type { ClusterShellMode } from "../../exec/types";

/**
 * OpenClusterShellButton — cluster-header action that opens (or focuses)
 * a cluster-shell session in the global drawer (issue #104).
 *
 * Hides entirely when the backend reports clusterShellEnabled=false.
 * Server-side gating (tier membership, cap) surfaces as an error frame
 * in the session — the SPA does not pre-flight via SAR for the shell
 * because the relevant authz is on the impersonator SA, not the caller.
 *
 * Hotkey: Cmd-Shift-E. Cmd-E is owned by pod-exec on pod-detail pages;
 * Shift modifier disambiguates "cluster scope."
 */

interface OpenClusterShellButtonProps {
  cluster: string;
}

export function OpenClusterShellButton({
  cluster,
}: OpenClusterShellButtonProps) {
  const { data: clustersData } = useClusters();
  const { openSession } = useExecSessions();

  const clusterMeta = clustersData?.clusters.find((c) => c.name === cluster);
  // Treat "field absent" (older backend) as disabled — feature is opt-in,
  // and we don't want to render a button that always 404s.
  const enabled = clusterMeta?.clusterShellEnabled === true;
  const mode: ClusterShellMode =
    (clusterMeta?.clusterShellMode as ClusterShellMode | undefined) ?? "bash";

  const [capReached, setCapReached] = useState(false);

  function open() {
    const result = openSession({ kind: "cluster-shell", cluster, mode });
    if (!result.ok && result.reason === "cap_reached") {
      setCapReached(true);
    }
  }

  // Cmd-Shift-E shortcut while this button is mounted (i.e. while
  // anywhere inside a cluster). Capture phase + stopPropagation so it
  // wins against xterm.js and other in-page listeners — same pattern
  // as pod-exec's Cmd-E shortcut.
  useEffect(() => {
    if (!enabled) return;
    function onKey(e: KeyboardEvent) {
      const meta = e.metaKey || e.ctrlKey;
      if (!meta) return;
      if (!e.shiftKey) return;
      if (e.altKey) return;
      if (e.key !== "e" && e.key !== "E" && e.code !== "KeyE") return;
      const target = e.target as HTMLElement | null;
      if (
        target &&
        (target.tagName === "INPUT" ||
          target.tagName === "TEXTAREA" ||
          target.isContentEditable)
      ) {
        return;
      }
      e.preventDefault();
      e.stopPropagation();
      open();
    }
    window.addEventListener("keydown", onKey, true);
    return () => window.removeEventListener("keydown", onKey, true);
    // open() captures cluster + mode from the latest render closure.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [enabled, cluster, mode]);

  if (!enabled) return null;

  return (
    <>
      <button
        type="button"
        onClick={open}
        title={`Open cluster shell · Cmd-Shift-E`}
        className="group inline-flex items-center gap-1.5 rounded-md border border-border px-2.5 py-1 font-mono text-[11.5px] font-medium text-ink-muted transition-colors hover:border-accent hover:text-accent"
      >
        <Terminal
          aria-hidden
          className="size-3.5 text-ink-faint transition-colors group-hover:text-accent"
        />
        <span>shell</span>
      </button>
      <CapReachedDialog open={capReached} onClose={() => setCapReached(false)} />
    </>
  );
}
