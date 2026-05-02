// YamlView — dispatcher for the YAML tab body.
//
// Two rendering paths, derived from URL params + RBAC:
//
//   ?edit=1  + canEdit  + kind in registry → <YamlEditor>     (inline editor)
//   default                                 → <YamlReadView>  (Monaco read)
//
// Both children consume the same useYaml cache key, so toggling
// between modes never re-fetches. `resource` is derived from the
// YamlKind registry — pages don't need to thread it. `canEdit` comes
// from useCanI (impersonated SSAR).

import { lazy, Suspense } from "react";
import { useSearchParams } from "react-router-dom";
import { useCanI } from "../../hooks/useCanI";
import type { ResourceRef, YamlKind } from "../../lib/api";
import { KIND_REGISTRY } from "../../lib/k8sKinds";
import { DetailLoading } from "./states";

const YamlReadView = lazy(() =>
  import("./YamlReadView").then((m) => ({ default: m.YamlReadView })),
);

const YamlEditor = lazy(() =>
  import("./yaml").then((m) => ({ default: m.YamlEditor })),
);

interface YamlViewProps {
  cluster: string;
  kind: YamlKind;
  ns: string;
  name: string;
}

export function YamlView(props: YamlViewProps) {
  const [params] = useSearchParams();
  const wantEdit = params.get("edit") === "1";

  const meta = KIND_REGISTRY[props.kind];
  const resource: ResourceRef | null = meta
    ? {
        cluster: props.cluster,
        group: meta.group,
        version: meta.version,
        resource: meta.resource,
        namespace: props.ns || undefined,
        name: props.name,
        kind: meta.kind,
      }
    : null;

  // SSAR via the existing hook — the same gate ResourceActions uses.
  // Always called for stable hook order; only the editor branch
  // reads it.
  const canEdit = useCanI({
    verb: "patch",
    resource: meta?.resource ?? "",
    namespace: props.ns || undefined,
  });

  if (wantEdit && resource && canEdit) {
    return (
      <Suspense fallback={<DetailLoading label="loading editor…" />}>
        <YamlEditor
          cluster={props.cluster}
          yamlKind={props.kind}
          resource={resource}
        />
      </Suspense>
    );
  }

  return (
    <Suspense fallback={<DetailLoading label="loading editor…" />}>
      <YamlReadView
        cluster={props.cluster}
        kind={props.kind}
        ns={props.ns}
        name={props.name}
      />
    </Suspense>
  );
}
