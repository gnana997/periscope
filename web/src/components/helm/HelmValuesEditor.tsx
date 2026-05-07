// HelmValuesEditor — top-level switch between form (when chart
// ships values.schema.json) and Monaco YAML (when it doesn't).
//
// The renderer moved to `lib/schemaForm/` for #116 reuse; this
// file is now a thin Helm-flavored wrapper that picks the form
// path when a schema is present and the Monaco fallback otherwise.
//
// State contract:
//   - The CALLER owns `valuesYaml` (raw YAML string).
//   - When schema is present, SchemaFormBridge owns the parse /
//     stringify round-trip and feeds SchemaForm the JS object.
//   - When schema is absent, this component bypasses parse /
//     serialize and hands the string straight to HelmValuesYaml.

import { SchemaFormBridge } from "../../lib/schemaForm/SchemaFormBridge";
import { HelmValuesYaml } from "./HelmValuesYaml";
import type { JSONSchema } from "../../lib/schemaForm/types";

interface HelmValuesEditorProps {
  /** values.yaml as a raw string — what the chart shipped, what
   *  eventually goes back to helm. */
  valuesYaml: string;
  /** values.schema.json from the chart (decoded). When absent,
   *  the editor renders the Monaco YAML fallback. */
  schema?: JSONSchema;
  onValuesYamlChange: (next: string) => void;
}

export function HelmValuesEditor({
  valuesYaml,
  schema,
  onValuesYamlChange,
}: HelmValuesEditorProps) {
  if (!schema) {
    return (
      <div className="flex min-h-[460px] flex-1 flex-col overflow-hidden rounded-sm border border-border bg-bg">
        <HelmValuesYaml value={valuesYaml} onChange={onValuesYamlChange} />
      </div>
    );
  }
  return (
    <SchemaFormBridge
      valuesYaml={valuesYaml}
      schema={schema}
      onValuesYamlChange={onValuesYamlChange}
      banner={<FormModeBanner />}
      emptyMessage="chart ships a schema but it doesn't describe an editable values object — nothing to render in form mode."
    />
  );
}

function FormModeBanner() {
  return (
    <div className="rounded-sm border border-yellow/40 bg-yellow/5 px-3 py-2 text-[12px] text-ink-muted">
      <span className="font-mono text-[10.5px] uppercase tracking-[0.14em] text-yellow">
        form mode
      </span>
      <span className="ml-2">
        chart ships a values schema; rendering as a structured form.
        comments in values.yaml will be stripped on save — switch to
        YAML mode (planned in a follow-up) to preserve them.
      </span>
    </div>
  );
}
