// ResourceActions — the "edit yaml" + "delete" pair shown in detail
// panels. Centralises styling, modal lifecycle, and the YAML preload
// so the Pods/Deployments/Secrets detail components stay declarative.
//
// Actions are shown unconditionally in v1 — the backend is the
// authoritative gate (impersonated K8s RBAC). useCanI() is called
// anyway so when SSAR plumbing lands in v1.x every site upgrades for
// free. See useCanI for the rationale.

import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { ApiError } from "../../lib/api";
import { useYaml } from "../../hooks/useResource";
import { useCanI } from "../../hooks/useCanI";
import type { YamlKind } from "../../lib/api";
import { DeleteResourceModal } from "./DeleteResourceModal";
import { EditResourceModal, type ResourceRef } from "./EditResourceModal";

interface ResourceActionsProps {
  cluster: string;
  // The YAML kind / URL segment for the existing GET /yaml endpoint.
  // Used to fetch the current YAML when "edit yaml" is clicked.
  yamlKind: YamlKind;
  // Resource ref the modals act on. Pass `kind` for a friendlier
  // confirm-modal title ("delete pod foo" vs "delete pods foo").
  resource: ResourceRef;
  // Optional: actions to render after edit/delete (e.g. "open shell").
  trailing?: React.ReactNode;
  // Called after a successful delete so the page can navigate away
  // from the now-gone selection. Edit applies invalidate via react-query.
  onDeleted?: () => void;
}

export function ResourceActions({
  cluster,
  yamlKind,
  resource,
  trailing,
  onDeleted,
}: ResourceActionsProps) {
  const [showEdit, setShowEdit] = useState(false);
  const [showDelete, setShowDelete] = useState(false);
  const canEdit = useCanI({
    verb: "patch",
    resource: resource.resource,
    namespace: resource.namespace,
  });
  const canDelete = useCanI({
    verb: "delete",
    resource: resource.resource,
    namespace: resource.namespace,
  });
  const ns = resource.namespace ?? "";

  // Lazy-load YAML only when the edit modal opens. Reusing the existing
  // useYaml hook means the same react-query cache backs both the YAML
  // tab and the editor's initial state.
  const yamlQuery = useYaml(cluster, yamlKind, ns, resource.name, showEdit);

  const qc = useQueryClient();

  return (
    <div className="flex items-center gap-1.5">
      {canEdit && (
        <button
          type="button"
          onClick={() => setShowEdit(true)}
          className="rounded-sm border border-border-strong px-2.5 py-1 font-mono text-[12px] text-ink-muted transition-colors hover:border-ink-muted hover:text-ink"
        >
          edit yaml
        </button>
      )}
      {canDelete && (
        <button
          type="button"
          onClick={() => setShowDelete(true)}
          className="rounded-sm border border-border-strong px-2.5 py-1 font-mono text-[12px] text-red transition-colors hover:bg-red-soft"
        >
          delete
        </button>
      )}
      {trailing}

      {showEdit &&
        (yamlQuery.isLoading ? (
          <ModalShell label="loading yaml…" onClose={() => setShowEdit(false)} />
        ) : yamlQuery.isError ? (
          <ModalShell
            label={
              (yamlQuery.error as ApiError | undefined)?.status === 403
                ? "your role doesn't allow reading this resource"
                : "couldn't load yaml"
            }
            tone="error"
            onClose={() => setShowEdit(false)}
          />
        ) : yamlQuery.data ? (
          <EditResourceModal
            resourceRef={resource}
            initialYaml={yamlQuery.data}
            onClose={() => setShowEdit(false)}
            onApplied={(result) => {
              if (!result.dryRun) {
                // Refresh the detail + list so the new state shows.
                qc.invalidateQueries({ queryKey: [yamlKind] });
                qc.invalidateQueries({
                  queryKey: ["yaml", cluster, yamlKind, ns, resource.name],
                });
              }
            }}
          />
        ) : null)}

      {showDelete && (
        <DeleteResourceModal
          resourceRef={resource}
          onClose={() => setShowDelete(false)}
          onDeleted={() => {
            qc.invalidateQueries({ queryKey: [yamlKind] });
            onDeleted?.();
          }}
        />
      )}
    </div>
  );
}

// ModalShell — minimal placeholder used while we're loading the YAML
// or if the load failed. Same chrome as the real edit modal so the UI
// doesn't jump.
function ModalShell({
  label,
  tone = "info",
  onClose,
}: {
  label: string;
  tone?: "info" | "error";
  onClose: () => void;
}) {
  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 px-4"
      role="dialog"
      aria-modal="true"
      onClick={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div className="w-full max-w-md rounded-md border border-border-strong bg-surface px-5 py-6 text-center">
        <div
          className={
            tone === "error"
              ? "font-mono text-sm text-red"
              : "font-mono text-sm text-ink-faint italic"
          }
        >
          {label}
        </div>
        <div className="mt-4">
          <button
            type="button"
            onClick={onClose}
            className="rounded-sm border border-border-strong px-3 py-1 font-mono text-xs text-ink-muted transition-colors hover:border-ink-muted hover:text-ink"
          >
            close
          </button>
        </div>
      </div>
    </div>
  );
}
