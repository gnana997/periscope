// K8sSchemaForm — shared form composer used by ConfigMapForm,
// SecretForm, ServiceForm, IngressForm. Looks up the right
// OpenAPI v3 root schema for the kind, narrows it through the
// per-kind allowlist (so noise like status / managedFields stays
// hidden), and renders SchemaFormBridge with the K8s walker
// options (resolveRef + allowKvMap + allowArrayOfObjects).
//
// The four per-kind files in this folder are thin wrappers that
// pass through to this component plus any kind-specific layer
// (SecretForm wraps `data` with base64 encode/decode).

import { useMemo } from "react";
import type { ReactNode } from "react";
import { useOpenAPISchema } from "../../hooks/useResource";
import { SchemaFormBridge } from "../../lib/schemaForm/SchemaFormBridge";
import { buildRefResolver, findSchemaByGVK } from "../../lib/schemaForm/refResolver";
import {
  filterSchemaForKind,
  getCreateOnlyPaths,
  getKindGVK,
  type SupportedKind,
} from "../../lib/schemaForm/k8sAllowlist";
import type { SchemaFormMode } from "../../lib/schemaForm/SchemaForm";
import type { WalkOptions } from "../../lib/schemaForm/walker";

export interface K8sSchemaFormProps {
  cluster: string;
  kind: SupportedKind;
  /** YAML serialization the parent owns. The bridge round-trips it
   *  to/from a JS object behind the scenes. */
  valuesYaml: string;
  onValuesYamlChange: (next: string) => void;
  mode: SchemaFormMode;
  /** Optional banner above the form (e.g. the Form/YAML toggle
   *  rendered by KindEditRouter). */
  banner?: ReactNode;
}

export function K8sSchemaForm({
  cluster,
  kind,
  valuesYaml,
  onValuesYamlChange,
  mode,
  banner,
}: K8sSchemaFormProps) {
  const gvk = getKindGVK(kind);
  const schemaQuery = useOpenAPISchema(cluster, gvk.group, gvk.version, true);

  const { rootSchema, walkOptions } = useMemo(() => {
    const doc = schemaQuery.data;
    if (!doc) return { rootSchema: undefined, walkOptions: undefined };
    const root = findSchemaByGVK(doc, gvk.group, gvk.version, gvk.kind);
    if (!root) return { rootSchema: undefined, walkOptions: undefined };
    const filtered = filterSchemaForKind(root, kind);
    const opts: WalkOptions = {
      resolveRef: buildRefResolver(doc),
      allowKvMap: true,
      allowArrayOfObjects: true,
      createOnlyPaths: getCreateOnlyPaths(kind),
    };
    return { rootSchema: filtered, walkOptions: opts };
  }, [schemaQuery.data, gvk.group, gvk.version, gvk.kind, kind]);

  if (schemaQuery.isPending) {
    return (
      <div className="px-3 py-4 text-[13px] text-ink-muted">
        loading {gvk.group || "core"}/{gvk.version} schema…
      </div>
    );
  }
  if (schemaQuery.isError || !rootSchema || !walkOptions) {
    return (
      <div className="rounded-sm border border-yellow/40 bg-yellow/5 px-3 py-2 text-[12px] text-ink-muted">
        <span className="font-mono text-[10.5px] uppercase tracking-[0.14em] text-yellow">
          schema unavailable
        </span>
        <span className="ml-2">
          could not resolve the OpenAPI v3 schema for {gvk.kind} — switch to YAML mode to edit.
        </span>
      </div>
    );
  }

  return (
    <SchemaFormBridge
      valuesYaml={valuesYaml}
      schema={rootSchema}
      walkOptions={walkOptions}
      mode={mode}
      onValuesYamlChange={onValuesYamlChange}
      banner={banner}
      emptyMessage={`schema for ${gvk.kind} produced no editable fields under the form-mode allowlist — edit in YAML mode.`}
    />
  );
}
