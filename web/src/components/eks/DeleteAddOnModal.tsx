// DeleteAddOnModal — confirm-and-delete for an EKS managed add-on
// (issue #119, PR-3).
//
// Wraps the shared ConfirmActionModal with a `preserve` checkbox.
// Preserve=true keeps the underlying K8s resources (deployments,
// configmaps, …) in place after the addon resource is gone — surfaced
// as a checkbox so an operator doesn't accidentally rip out coredns
// and break DNS resolution cluster-wide. Default is false (full
// teardown), matching AWS's API default.
//
// On success: toast, close. The list/detail/catalog queries are
// invalidated by useDeleteAddon (onSettled) so the row picks up
// status=DELETING; the status-aware refetchInterval polls the row
// until it disappears.

import { useEffect, useState } from "react";
import { ApiError } from "../../lib/api";
import { showToast } from "../../lib/toastBus";
import { ConfirmActionModal } from "../ui/ConfirmActionModal";
import { useDeleteAddon } from "../../hooks/useAddonInstall";

interface DeleteAddOnModalProps {
  open: boolean;
  onClose: () => void;
  cluster: string;
  /** Addon name (e.g. "vpc-cni"). Used in the dialog body and as the
   *  URL param on the DELETE request. */
  addonName: string | null;
}

export function DeleteAddOnModal({
  open,
  onClose,
  cluster,
  addonName,
}: DeleteAddOnModalProps) {
  const [preserve, setPreserve] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const deleteMutation = useDeleteAddon(cluster);

  // Reset state every time the modal opens. Closing + reopening on a
  // different addon must not leak the previous addon's preserve
  // toggle or stale error.
  useEffect(() => {
    if (!open) return;
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setPreserve(false);
    setError(null);
  }, [open]);

  if (!addonName) return null;

  const onConfirm = () => {
    setError(null);
    deleteMutation.mutate(
      { name: addonName, preserve },
      {
        onSuccess: () => {
          showToast(
            preserve
              ? `deleting addon ${addonName} (preserving k8s resources)`
              : `deleting addon ${addonName}`,
            "success",
          );
          onClose();
        },
        onError: (err) => {
          setError(friendlyError(err));
        },
      },
    );
  };

  return (
    <ConfirmActionModal
      open={open}
      title={`Delete add-on ${addonName}?`}
      variant="danger"
      confirmLabel="delete"
      pending={deleteMutation.isPending}
      error={error}
      onCancel={onClose}
      onConfirm={onConfirm}
      body={
        <div className="space-y-3">
          <p>
            EKS will mark the addon <code>{addonName}</code> as{" "}
            <code>DELETING</code> and tear it down server-side. This
            takes 1-5 minutes.
          </p>
          <label className="flex items-start gap-2 text-[12px]">
            <input
              type="checkbox"
              checked={preserve}
              onChange={(e) => setPreserve(e.target.checked)}
              className="mt-0.5"
              disabled={deleteMutation.isPending}
            />
            <span>
              <span className="font-mono">preserve</span> — keep the
              underlying K8s resources (deployments, configmaps, …)
              after the addon is gone. Use this for{" "}
              <code>coredns</code> / <code>kube-proxy</code> / other
              critical add-ons where deleting the K8s resources would
              break the cluster.
            </span>
          </label>
        </div>
      }
    />
  );
}

function friendlyError(err: unknown): string {
  if (err instanceof ApiError) {
    return `${err.status} ${err.message}${err.bodyText ? ` — ${err.bodyText.trim()}` : ""}`;
  }
  if (err instanceof Error) return err.message;
  return String(err);
}
