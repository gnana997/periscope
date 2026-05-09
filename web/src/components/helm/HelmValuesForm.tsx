// HelmValuesForm — historical wrapper around the generic SchemaForm
// engine. The engine moved to `lib/schemaForm/` for #116; this file
// stays as a Helm-named entry point so existing helm-side imports
// don't churn and the prop shape (no walkOptions, no mode) stays
// documented.
//
// Helm consumers want default walker behavior (no $ref resolver,
// no kv-map, no array-of-objects — anything the form can't render
// shows the "edit in YAML mode" hint via SchemaForm's unsupported
// branch). K8s consumers go through `K8sSchemaForm` for the richer
// option set.

import { SchemaForm, type JSONSchema } from "../../lib/schemaForm";

interface HelmValuesFormProps {
  schema: JSONSchema;
  /** Current values — typically the parsed values.yaml. The form
   *  treats this as immutable; onChange returns a new tree. */
  values: Record<string, unknown>;
  onChange: (next: Record<string, unknown>) => void;
}

export function HelmValuesForm({ schema, values, onChange }: HelmValuesFormProps) {
  // walkOptions intentionally unset — Helm v1.1 default behavior:
  // no $ref resolution, no K8s-specific widget extensions, no
  // create-only paths. K8s editors that need the richer option set
  // import SchemaForm directly via K8sSchemaForm.
  return <SchemaForm schema={schema} values={values} onChange={onChange} />;
}
