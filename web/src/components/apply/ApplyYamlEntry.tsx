// ApplyYamlEntry — Sidebar button that opens the shared
// ApplyYamlDialog via useApplyDialog().
//
// Mounted inside the cluster Sidebar above the resource navigation.
// Hidden when the operator has no apply permission in the cluster
// (per useCanApply). The dialog itself lives once at App level via
// ApplyDialogProvider so multiple entry points (Sidebar + Cmd+K
// palette + future surfaces) share a single mount.

import { useParams } from "react-router-dom";
import { Plus } from "lucide-react";
import { useApplyDialog } from "../../contexts/applyDialog";
import { useCanApply } from "../../hooks/useCanApply";

export function ApplyYamlEntry() {
  const { cluster } = useParams<{ cluster: string }>();
  const can = useCanApply(cluster ?? "");
  const dialog = useApplyDialog();

  // Hide the entry point entirely (don't render a disabled stub) when
  // the operator can't apply anything. Loading shows the button so
  // first paint stays usable; the dialog would error per-doc anyway
  // if the operator turns out to lack permission.
  if (!cluster) return null;
  if (!can.loading && !can.allowed) return null;

  return (
    <button
      type="button"
      onClick={() => dialog.open(cluster)}
      title="Paste / upload YAML and apply to this cluster"
      className="group mx-2 mt-2 flex items-center gap-1.5 rounded-sm border border-border bg-surface-2 px-2.5 py-1.5 font-mono text-[12px] lowercase tracking-tight text-ink-muted transition-colors hover:border-accent hover:text-accent"
    >
      <Plus
        aria-hidden
        className="size-3.5 text-ink-faint transition-colors group-hover:text-accent"
      />
      <span>apply yaml</span>
    </button>
  );
}
