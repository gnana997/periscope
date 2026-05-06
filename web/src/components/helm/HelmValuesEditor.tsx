// HelmValuesEditor — top-level switch between form (when chart
// ships values.schema.json) and Monaco YAML (when it doesn't).
//
// Why not a toggle in v1.1: a Form↔YAML toggle would need
// round-trip serialization that loses YAML comments and reformat
// hints. Operators editing schemaless charts edit YAML directly;
// operators editing schema'd charts use the form. Cleaner v1
// invariant; toggle is a follow-up polish item.
//
// State contract:
//   - The CALLER owns `valuesYaml` (raw YAML string).
//   - When schema is present, this component parses the YAML once
//     into a JS object, hands it to HelmValuesForm, and on every
//     form mutation re-serializes to YAML and bubbles back through
//     `onValuesYamlChange`.
//   - When schema is absent, it bypasses parse/serialize and hands
//     the string straight to HelmValuesYaml.

import { useEffect, useState } from "react";
import { parse as parseYaml, stringify as stringifyYaml } from "yaml";
import { HelmValuesForm } from "./HelmValuesForm";
import { HelmValuesYaml } from "./HelmValuesYaml";
import type { JSONSchema } from "../../lib/helmSchema";

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
    />
  );
}

// SchemaFormBridge translates between the YAML string the parent
// owns and the JS-object world HelmValuesForm operates in. One
// parse on mount + one parse on external valuesYaml change; one
// stringify per form mutation. Yaml-package preserves key order
// across the round-trip but DOES NOT preserve comments — known
// limitation in v1.1 (operators see the warning banner above).
function SchemaFormBridge({
  valuesYaml,
  schema,
  onValuesYamlChange,
}: {
  valuesYaml: string;
  schema: JSONSchema;
  onValuesYamlChange: (next: string) => void;
}) {
  const [obj, setObj] = useState<Record<string, unknown>>(() => parseSafe(valuesYaml));

  // External reset (parent supplied new YAML — e.g. operator picked
  // a different chart version) — re-parse. The setState-in-effect
  // is intentional: this is the "external state changed, sync our
  // mirror" pattern, which the rule legitimately allows but flags
  // anyway. The structural-equality guard makes it a no-op when the
  // parent re-renders without changing valuesYaml.
  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setObj((prev) => {
      const reparsed = parseSafe(valuesYaml);
      try {
        if (JSON.stringify(prev) === JSON.stringify(reparsed)) return prev;
      } catch {
        /* fall through */
      }
      return reparsed;
    });
  }, [valuesYaml]);

  return (
    <div className="space-y-3">
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
      <HelmValuesForm
        schema={schema}
        values={obj}
        onChange={(next) => {
          setObj(next);
          // Re-serialize and bubble. yaml.stringify with sortKeys
          // would be operator-friendly but breaks helm convention
          // (sections matter); leave default order.
          try {
            onValuesYamlChange(stringifyYaml(next, { lineWidth: 0 }));
          } catch {
            /* serialization shouldn't fail for objects we just
             * received via setState; if it does, we keep the local
             * state but skip propagation rather than crashing. */
          }
        }}
      />
    </div>
  );
}

function parseSafe(yaml: string): Record<string, unknown> {
  if (!yaml.trim()) return {};
  try {
    const parsed = parseYaml(yaml);
    if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) {
      return parsed as Record<string, unknown>;
    }
    return {};
  } catch {
    return {};
  }
}
