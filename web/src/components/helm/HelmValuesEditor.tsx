// HelmValuesEditor — top-level switch between form (when chart
// ships values.schema.json) and Monaco YAML.
//
// State contract:
//   - The CALLER owns `valuesYaml` (raw YAML string).
//   - When schema is present and the operator chose form mode, this
//     component parses the YAML once into a JS object, hands it to
//     HelmValuesForm, and on every form mutation re-serializes to
//     YAML and bubbles back through `onValuesYamlChange`.
//   - When schema is absent OR the operator toggled to YAML mode,
//     it bypasses parse/serialize and hands the string straight to
//     HelmValuesYaml.
//
// Form↔YAML toggle:
//   - Visible only when a schema is present (no toggle when there's
//     no schema — YAML is the only option).
//   - Default mode is `form` unless the schema has a required field
//     the form can't render ($ref / allOf / arrays of objects / etc.),
//     in which case we default to `yaml` so the operator isn't stuck.
//   - Switching either way is free: the parent owns valuesYaml and
//     SchemaFormBridge re-parses on remount, so the toggle is just
//     "which child to render."
//   - YAML→Form may lose comments (yaml.parse → yaml.stringify drops
//     them); the warning above the form fires once after such a flip.

import { useEffect, useMemo, useRef, useState } from "react";
import { parse as parseYaml, stringify as stringifyYaml } from "yaml";
import { HelmValuesForm } from "./HelmValuesForm";
import { HelmValuesYaml } from "./HelmValuesYaml";
import {
  hasRequiredUnsupportedField,
  type JSONSchema,
} from "../../lib/helmSchema";
import { cn } from "../../lib/cn";

type Mode = "form" | "yaml";

interface HelmValuesEditorProps {
  /** values.yaml as a raw string — what the chart shipped, what
   *  eventually goes back to helm. */
  valuesYaml: string;
  /** values.schema.json from the chart (decoded). When absent,
   *  the editor renders the Monaco YAML fallback with no toggle. */
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
    <SchemaEditor
      valuesYaml={valuesYaml}
      schema={schema}
      onValuesYamlChange={onValuesYamlChange}
    />
  );
}

function SchemaEditor({
  valuesYaml,
  schema,
  onValuesYamlChange,
}: {
  valuesYaml: string;
  schema: JSONSchema;
  onValuesYamlChange: (next: string) => void;
}) {
  // Auto-default to YAML if any required field is unrenderable in
  // form mode — otherwise the operator hits a wall.
  const [mode, setMode] = useState<Mode>(() =>
    hasRequiredUnsupportedField(schema) ? "yaml" : "form",
  );
  // Recompute the suggested default when the chart version (and so
  // the schema) changes. Only override the current mode when the new
  // schema would force a different default; preserve an explicit user
  // toggle within the same schema.
  const lastSchemaRef = useRef<JSONSchema>(schema);
  useEffect(() => {
    if (lastSchemaRef.current === schema) return;
    lastSchemaRef.current = schema;
    setMode(hasRequiredUnsupportedField(schema) ? "yaml" : "form");
  }, [schema]);

  // Track whether we've ever round-tripped through the form. If so,
  // comments in the original YAML have already been dropped on the
  // first stringify; flag it so the operator knows.
  const [commentsLost, setCommentsLost] = useState(false);

  return (
    <div className="flex min-h-[460px] flex-1 flex-col gap-2">
      <ModeToggle mode={mode} onChange={setMode} />
      {mode === "yaml" ? (
        <div className="flex min-h-[420px] flex-1 flex-col overflow-hidden rounded-sm border border-border bg-bg">
          <HelmValuesYaml value={valuesYaml} onChange={onValuesYamlChange} />
        </div>
      ) : (
        <SchemaFormBridge
          valuesYaml={valuesYaml}
          schema={schema}
          onValuesYamlChange={(next) => {
            setCommentsLost(true);
            onValuesYamlChange(next);
          }}
          commentsLost={commentsLost}
        />
      )}
    </div>
  );
}

function ModeToggle({
  mode,
  onChange,
}: {
  mode: Mode;
  onChange: (next: Mode) => void;
}) {
  return (
    <div className="flex items-center justify-end gap-1">
      <span className="mr-1 font-mono text-[10px] uppercase tracking-[0.08em] text-ink-faint">
        edit as
      </span>
      <ModeButton active={mode === "form"} onClick={() => onChange("form")}>
        form
      </ModeButton>
      <ModeButton active={mode === "yaml"} onClick={() => onChange("yaml")}>
        yaml
      </ModeButton>
    </div>
  );
}

function ModeButton({
  active,
  onClick,
  children,
}: {
  active: boolean;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "rounded-sm border px-2 py-0.5 font-mono text-[10.5px] uppercase tracking-[0.08em] transition-colors",
        active
          ? "border-accent bg-accent-soft text-accent"
          : "border-border text-ink-muted hover:bg-surface-2",
      )}
    >
      {children}
    </button>
  );
}

// SchemaFormBridge translates between the YAML string the parent
// owns and the JS-object world HelmValuesForm operates in. One
// parse on mount + one parse on external valuesYaml change; one
// stringify per form mutation. yaml-package preserves key order
// across the round-trip but DOES NOT preserve comments.
function SchemaFormBridge({
  valuesYaml,
  schema,
  onValuesYamlChange,
  commentsLost,
}: {
  valuesYaml: string;
  schema: JSONSchema;
  onValuesYamlChange: (next: string) => void;
  commentsLost: boolean;
}) {
  const [obj, setObj] = useState<Record<string, unknown>>(() => parseSafe(valuesYaml));

  // External reset (parent supplied new YAML — e.g. operator picked
  // a different chart version, or flipped from YAML mode back to
  // Form mode after editing) — re-parse. The setState-in-effect is
  // the "external state changed, sync our mirror" pattern; the
  // structural-equality guard makes it a no-op when the parent
  // re-renders without changing valuesYaml.
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

  const sourceHasComments = useMemo(() => yamlHasComments(valuesYaml), [valuesYaml]);

  return (
    <div className="space-y-3">
      {(commentsLost || sourceHasComments) && (
        <div className="rounded-sm border border-yellow/40 bg-yellow/5 px-3 py-2 text-[12px] text-ink-muted">
          <span className="font-mono text-[10.5px] uppercase tracking-[0.14em] text-yellow">
            form mode
          </span>
          <span className="ml-2">
            comments in values.yaml are stripped on save — switch to
            YAML mode to preserve them.
          </span>
        </div>
      )}
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

function yamlHasComments(yaml: string): boolean {
  // Cheap line scan — we don't need perfect YAML lexing here, just
  // "is there a # somewhere that isn't inside a string." Good enough
  // to decide whether to show the comment-loss banner.
  for (const line of yaml.split("\n")) {
    const t = line.trimStart();
    if (t.startsWith("#")) return true;
    // crude inline check: " # " outside of quotes
    const idx = t.indexOf(" #");
    if (idx >= 0) return true;
  }
  return false;
}
