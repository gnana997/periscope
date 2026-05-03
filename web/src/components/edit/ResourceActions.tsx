// ResourceActions — the "edit yaml" + "delete" pair shown in detail
// panel tab strips. Centralises styling + RBAC gating so the
// Pods/Deployments/Secrets detail components stay declarative.
//
// As of PR5 the edit flow is the inline YamlEditor (not a modal):
// `<EditButton />` navigates the URL to `?tab=yaml&edit=1`, and
// YamlView dispatches to the editor. ResourceActions retains the
// delete modal — that's still a confirm-and-go interaction that
// doesn't benefit from being inline.
//
// Phase 4: takes a single `source: EditorSource` so built-ins and
// CRs share one prop shape. Internally uses gvrkFromSource() to
// resolve the GVRK for the apiserver call without each caller
// having to thread group/version/resource.
//
// Actions are shown unconditionally in v1 — the backend is the
// authoritative gate (impersonated K8s RBAC). useCanI() is called
// anyway so when SSAR plumbing lands in v1.x every site upgrades for
// free. See useCanI for the rationale.

import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useCanI } from "../../hooks/useCanI";
import type { ResourceRef } from "../../lib/api";
import {
  gvrkFromSource,
  type EditorSource,
} from "../../lib/customResources";
import { DeleteResourceModal } from "./DeleteResourceModal";
import { EditButton } from "../detail/yaml/EditButton";

interface ResourceActionsProps {
  cluster: string;
  source: EditorSource;
  // null/undefined → cluster-scoped resource.
  namespace: string | null | undefined;
  name: string;
  // Optional: actions to render after edit/delete (e.g. "open shell").
  trailing?: React.ReactNode;
  // Called after a successful delete so the page can navigate away
  // from the now-gone selection. Edit invalidates from inside YamlEditor.
  onDeleted?: () => void;
}

export function ResourceActions({
  cluster,
  source,
  namespace,
  name,
  trailing,
  onDeleted,
}: ResourceActionsProps) {
  const [showDelete, setShowDelete] = useState(false);

  const meta = gvrkFromSource(source);
  const ns = namespace ?? undefined;
  const resource: ResourceRef = {
    cluster,
    group: meta.group,
    version: meta.version,
    resource: meta.resource,
    namespace: ns,
    name,
    kind: meta.kind,
  };
  const canEdit = useCanI({ verb: "patch", resource: meta.resource, namespace: ns });
  const canDelete = useCanI({ verb: "delete", resource: meta.resource, namespace: ns });

  const qc = useQueryClient();

  return (
    <div className="flex items-center gap-1.5">
      {canEdit && <EditButton />}
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

      {showDelete && (
        <DeleteResourceModal
          resourceRef={resource}
          onClose={() => setShowDelete(false)}
          onDeleted={() => {
            // List-cache invalidation key: built-ins use bare yamlKind
            // (existing convention), CRs use the namespaced source key.
            const listKey =
              source.kind === "builtin"
                ? [source.yamlKind]
                : [
                    "customresources",
                    cluster,
                    source.cr.group,
                    source.cr.version,
                    source.cr.resource,
                    ns ?? "",
                  ];
            qc.invalidateQueries({ queryKey: listKey });
            onDeleted?.();
          }}
        />
      )}
    </div>
  );
}
