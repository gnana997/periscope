// schemaForm/SchemaFormBridge.tsx — translates between the YAML
// string a parent owns and the JS-object world SchemaForm operates
// in. Lifted (and slightly generalized) from HelmValuesEditor.tsx.
//
// One parse on mount + one parse on external valuesYaml change;
// one stringify per form mutation. yaml-package preserves key order
// across the round-trip but DOES NOT preserve comments — known
// limitation operators see in the banner the wrapper renders.

import { useEffect, useState, type ReactNode } from "react";
import { parse as parseYaml, stringify as stringifyYaml } from "yaml";
import { SchemaForm, type SchemaFormMode } from "./SchemaForm";
import type { JSONSchema } from "./types";
import type { WalkOptions } from "./walker";

export interface SchemaFormBridgeProps {
  valuesYaml: string;
  schema: JSONSchema;
  onValuesYamlChange: (next: string) => void;
  walkOptions?: WalkOptions;
  mode?: SchemaFormMode;
  emptyMessage?: ReactNode;
  /** Banner content rendered above the form. Helm uses it to warn
   *  about comment-stripping; K8s flows use it for the Form/YAML
   *  toggle and the round-trip warning state. */
  banner?: ReactNode;
  /** Called when the incoming `valuesYaml` fails to parse as an
   *  object. The bridge keeps the previous object state regardless;
   *  consumers that want to switch the user back to YAML mode use
   *  this signal. */
  onParseError?: (raw: string) => void;
}

export function SchemaFormBridge({
  valuesYaml,
  schema,
  onValuesYamlChange,
  walkOptions,
  mode,
  emptyMessage,
  banner,
  onParseError,
}: SchemaFormBridgeProps) {
  const [obj, setObj] = useState<Record<string, unknown>>(() => parseSafe(valuesYaml, onParseError));

  // External reset (parent supplied new YAML) — re-parse. The
  // setState-in-effect is intentional: this is the "external state
  // changed, sync our mirror" pattern. The structural-equality
  // guard makes it a no-op when the parent re-renders without
  // changing valuesYaml.
  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setObj((prev) => {
      const reparsed = parseSafe(valuesYaml, onParseError);
      try {
        if (JSON.stringify(prev) === JSON.stringify(reparsed)) return prev;
      } catch {
        /* fall through */
      }
      return reparsed;
    });
  }, [valuesYaml, onParseError]);

  return (
    <div className="space-y-3">
      {banner}
      <SchemaForm
        schema={schema}
        values={obj}
        walkOptions={walkOptions}
        mode={mode}
        emptyMessage={emptyMessage}
        onChange={(next) => {
          setObj(next);
          // Re-serialize and bubble. Default key order — sortKeys
          // would break section-ordering conventions for both Helm
          // values and K8s manifests.
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

function parseSafe(
  yaml: string,
  onParseError?: (raw: string) => void,
): Record<string, unknown> {
  if (!yaml.trim()) return {};
  try {
    const parsed = parseYaml(yaml);
    if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) {
      return parsed as Record<string, unknown>;
    }
    onParseError?.(yaml);
    return {};
  } catch {
    onParseError?.(yaml);
    return {};
  }
}
