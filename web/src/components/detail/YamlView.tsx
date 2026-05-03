// YamlView — dispatcher for the YAML tab body.
//
// Two rendering paths, derived from URL params + RBAC:
//
//   ?edit=1  + canEdit  + identifiable source → <YamlEditor>     (inline editor)
//   default                                   → <YamlReadView>   (Monaco read)
//
// Both children consume the same useEditorYaml cache key (segregated
// per source — built-ins keep `["yaml", ...]`, CRs use `["yaml-cr",
// ...]`), so toggling between modes never re-fetches. `resource` is
// derived from `gvrkFromSource(source)`. `canEdit` comes from
// useCanI (impersonated SSAR).

import { lazy, Suspense } from "react";
import { useSearchParams } from "react-router-dom";
import { useCanI } from "../../hooks/useCanI";
import {
  gvrkFromSource,
  sourceToResourceRef,
  type EditorSource,
} from "../../lib/customResources";
import { DetailLoading } from "./states";

const YamlReadView = lazy(() =>
  import("./YamlReadView").then((m) => ({ default: m.YamlReadView })),
);

const YamlEditor = lazy(() =>
  import("./yaml").then((m) => ({ default: m.YamlEditor })),
);

interface YamlViewProps {
  cluster: string;
  source: EditorSource;
  ns: string;
  name: string;
}

export function YamlView({ cluster, source, ns, name }: YamlViewProps) {
  const [params] = useSearchParams();
  const wantEdit = params.get("edit") === "1";

  const meta = gvrkFromSource(source);
  const resource = sourceToResourceRef(source, cluster, ns || null, name);

  // SAR via the existing hook — same gate ResourceActions uses.
  // Always called for stable hook order; only the editor branch
  // reads it. While the can-i query is in flight, useCanI reports
  // allowed=true (defer to the backend's gate on click) — this keeps
  // first-paint smooth and matches ResourceActions.
  const canEdit = useCanI(cluster, {
    verb: "patch",
    resource: meta.resource,
    namespace: ns || undefined,
  });

  if (wantEdit && canEdit.allowed) {
    return (
      <Suspense fallback={<DetailLoading label="loading editor…" />}>
        <YamlEditor cluster={cluster} source={source} resource={resource} />
      </Suspense>
    );
  }

  return (
    <Suspense fallback={<DetailLoading label="loading editor…" />}>
      <YamlReadView cluster={cluster} source={source} ns={ns} name={name} />
    </Suspense>
  );
}
