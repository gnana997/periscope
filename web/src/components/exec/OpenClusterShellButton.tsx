import { useEffect, useState } from "react";
import { cn } from "../../lib/cn";
import { useClusters } from "../../hooks/useClusters";
import { useExecSessions } from "../../exec/useExecSessions";
import { CapReachedDialog } from "../../exec/CapReachedDialog";
import { Tooltip } from "../Tooltip";
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
      <Tooltip content={null}>
        <button
          type="button"
          onClick={open}
          className={cn(
            "inline-flex h-7 items-center gap-1.5 rounded-md border border-border bg-surface px-2 font-mono text-[11px] text-ink-muted transition-colors",
            "hover:border-accent/60 hover:bg-accent-soft hover:text-accent",
          )}
          title={`Open cluster shell · Cmd-Shift-E`}
        >
          <ShellGlyph />
          <span>shell</span>
        </button>
      </Tooltip>
      <CapReachedDialog open={capReached} onClose={() => setCapReached(false)} />
    </>
  );
}

function ShellGlyph() {
  return (
    <svg
      width="11"
      height="11"
      viewBox="0 0 11 11"
      aria-hidden
      className="text-ink-faint"
    >
      <path
        d="M2 3l2.5 2.5L2 8M5.5 8H9"
        stroke="currentColor"
        strokeWidth="1.3"
        fill="none"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}
