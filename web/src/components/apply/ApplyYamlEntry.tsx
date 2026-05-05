// ApplyYamlEntry — button that opens the shared ApplyYamlDialog via
// useApplyDialog().
//
// Mounted in PageHeader (right-side action row, between the page's
// trailing slot and ThemeToggle), and in OverviewPage's
// ClusterIdentityBanner. The dialog itself lives once at App level
// via ApplyDialogProvider so multiple entry points share a single
// mount.
//
// Hidden when the operator has no apply permission in the cluster
// (per useCanApply). Per-doc RBAC pre-flight inside the dialog is
// sub-task #55.

import { useParams } from "react-router-dom";
import { Plus } from "lucide-react";
import { useApplyDialog } from "../../contexts/applyDialog";
import { useCanApply } from "../../hooks/useCanApply";

export function ApplyYamlEntry() {
  const { cluster } = useParams<{ cluster: string }>();
  const can = useCanApply(cluster ?? "");
  const dialog = useApplyDialog();

  // Hide entirely (don't render a disabled stub) when the operator
  // can't apply anything. Loading shows the button so first paint
  // stays usable; the dialog would error per-doc anyway if the
  // operator turns out to lack permission.
  if (!cluster) return null;
  if (!can.loading && !can.allowed) return null;

  return (
    <button
      type="button"
      onClick={() => dialog.open(cluster)}
      title="Paste / upload YAML and apply to this cluster"
      className="group inline-flex items-center gap-1.5 rounded-md border border-border px-2.5 py-1 font-mono text-[11.5px] font-medium text-ink-muted transition-colors hover:border-accent hover:text-accent"
    >
      <Plus
        aria-hidden
        className="size-3.5 text-ink-faint transition-colors group-hover:text-accent"
      />
      <span>apply yaml</span>
    </button>
  );
}
