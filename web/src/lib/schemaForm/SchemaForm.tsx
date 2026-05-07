// schemaForm/SchemaForm.tsx — generic JSON-Schema-driven form
// renderer. Originally HelmValuesForm (#74); generalized for #116
// so the same engine drives Helm chart values AND K8s OpenAPI v3
// schemas for ConfigMap / Secret / Service / Ingress.
//
// State model (unchanged from the Helm path):
//   - The CALLER owns the values object as a plain JS record.
//   - Each field's onChange invokes setAtPath() to produce a new
//     immutable values object, and bubbles through onChange.
//   - Validation runs on every change; issues are passed down by
//     path so each field can render its inline error.

import { useEffect, useId, useMemo, useRef, useState, type ReactNode } from "react";
import { buildFieldDescriptors, type WalkOptions } from "./walker";
import { validateValues } from "./validate";
import { cn } from "../cn";
import { getAtPath, setAtPath } from "./pathOps";
import type { FieldDescriptor, JSONSchema, ValidationIssue } from "./types";
import { Tooltip } from "../../components/Tooltip";

// InfoTip — tiny "(i)" affordance next to a label that opens a
// tooltip with the field's description on hover/focus. Used in
// place of inline description paragraphs because K8s OpenAPI v3
// descriptions are documentation-grade (the `host` field on
// Ingress.spec.rules carries a ~700-char paragraph) and inline
// rendering drowned out the actual form fields. Returns null when
// `text` is empty so call sites can drop the icon entirely.
function InfoTip({ text }: { text: string | undefined }) {
  if (!text) return null;
  return (
    <Tooltip content={text} side="top" sideOffset={4}>
      <button
        type="button"
        onClick={(e) => e.preventDefault()}
        aria-label="Field description"
        className="ml-1 inline-flex size-3.5 shrink-0 cursor-help items-center justify-center rounded-full border border-border text-[9.5px] font-mono text-ink-faint transition-colors hover:border-ink-muted hover:text-ink-muted"
      >
        i
      </button>
    </Tooltip>
  );
}

export type SchemaFormMode = "create" | "edit";

export interface SchemaFormProps {
  schema: JSONSchema;
  /** Current values. The form treats this as immutable; onChange
   *  returns a new tree. */
  values: Record<string, unknown>;
  onChange: (next: Record<string, unknown>) => void;
  /** Walker options (e.g. resolveRef, allowKvMap, allowArrayOfObjects).
   *  Default options reproduce Helm v1.1 behavior. */
  walkOptions?: WalkOptions;
  /** "edit" greys out descriptors marked editable="create-only"
   *  (e.g. metadata.name on K8s objects). Default "edit". */
  mode?: SchemaFormMode;
  /** Rendered when the schema produces zero descriptors. */
  emptyMessage?: ReactNode;
}

export function SchemaForm({
  schema,
  values,
  onChange,
  walkOptions,
  mode = "edit",
  emptyMessage,
}: SchemaFormProps) {
  const descriptors = useMemo(
    () => buildFieldDescriptors(schema, walkOptions),
    [schema, walkOptions],
  );
  const issues = useMemo(() => validateValues(schema, values), [schema, values]);

  if (descriptors.length === 0) {
    return (
      <div className="px-3 py-4 text-[13px] text-ink-muted">
        {emptyMessage ?? "schema produced no editable fields."}
      </div>
    );
  }

  return (
    <form className="space-y-3" onSubmit={(e) => e.preventDefault()}>
      {descriptors.map((d) => (
        <FieldRow
          key={d.path.join(".")}
          descriptor={d}
          values={values}
          issues={issues}
          mode={mode}
          onChange={onChange}
        />
      ))}
    </form>
  );
}

// ─── one field row ──────────────────────────────────────────────

interface FieldRowProps {
  descriptor: FieldDescriptor;
  values: Record<string, unknown>;
  issues: ValidationIssue[];
  mode: SchemaFormMode;
  onChange: (next: Record<string, unknown>) => void;
}

export function FieldRow({ descriptor, values, issues, mode, onChange }: FieldRowProps) {
  const id = useId();
  const fieldIssues = useMemo(
    () =>
      issues.filter(
        (i) => pathStartsWith(i.path, descriptor.path) && i.path.length === descriptor.path.length,
      ),
    [issues, descriptor.path],
  );
  const value = getAtPath(values, descriptor.path);
  const update = (next: unknown) => onChange(setAtPath(values, descriptor.path, next));

  if (descriptor.type === "object") {
    return (
      <fieldset className="rounded-sm border border-border px-3 pb-3 pt-2">
        <legend className="flex items-center px-1 font-mono text-[11px] uppercase tracking-[0.14em] text-ink-faint">
          {descriptor.label}
          {descriptor.required ? " *" : ""}
          <InfoTip text={descriptor.description} />
        </legend>
        <div className="space-y-3">
          {(descriptor.children ?? []).map((child) => (
            <FieldRow
              key={child.path.join(".")}
              descriptor={child}
              values={values}
              issues={issues}
              mode={mode}
              onChange={onChange}
            />
          ))}
        </div>
      </fieldset>
    );
  }

  if (descriptor.type === "unsupported") {
    return (
      <div className="rounded-sm border border-yellow/40 bg-yellow/5 px-3 py-2">
        <div className="flex items-baseline justify-between gap-3">
          <span className="font-mono text-[12px] text-ink">
            {descriptor.path.join(".")}
            {descriptor.required ? " *" : ""}
          </span>
          <span className="font-mono text-[10.5px] uppercase tracking-[0.12em] text-yellow">
            yaml only
          </span>
        </div>
        <p className="mt-1 text-[12px] text-ink-muted">{descriptor.unsupportedReason}</p>
      </div>
    );
  }

  const readOnly = mode === "edit" && descriptor.editable === "create-only";

  return (
    <div>
      <div className="flex items-baseline gap-2">
        <label htmlFor={id} className="inline-flex items-baseline font-mono text-[12px] text-ink">
          {descriptor.label}
          {descriptor.required ? <span className="text-red"> *</span> : null}
          <InfoTip text={descriptor.description} />
        </label>
        <span className="font-mono text-[10.5px] text-ink-faint">
          {pathBreadcrumb(descriptor.path)}
        </span>
        {readOnly ? (
          <span className="font-mono text-[10.5px] uppercase tracking-[0.12em] text-ink-faint">
            read-only after create
          </span>
        ) : null}
      </div>
      <div className="mt-1.5">
        <FieldInput
          id={id}
          descriptor={descriptor}
          value={value}
          onChange={update}
          readOnly={readOnly}
        />
      </div>
      {fieldIssues.map((iss, i) => (
        <p key={i} className="mt-1 font-mono text-[11.5px] text-red">
          {iss.message}
        </p>
      ))}
    </div>
  );
}

// ─── input components by type ───────────────────────────────────

interface InputProps {
  id: string;
  descriptor: FieldDescriptor;
  value: unknown;
  onChange: (next: unknown) => void;
  readOnly?: boolean;
}

function FieldInput({ id, descriptor, value, onChange, readOnly }: InputProps) {
  const placeholder =
    descriptor.default !== undefined ? `default: ${stringify(descriptor.default)}` : "";

  // Enum → select for any primitive type.
  if (descriptor.enum && descriptor.enum.length > 0) {
    return (
      <select
        id={id}
        disabled={readOnly}
        value={value === undefined ? "" : String(value)}
        onChange={(e) => onChange(coerceForType(descriptor.type, e.target.value))}
        className="w-full rounded-sm border border-border bg-bg px-2.5 py-1 font-mono text-[12.5px] text-ink focus:border-accent focus:outline-none disabled:opacity-60"
      >
        <option value="">— select —</option>
        {descriptor.enum.map((opt) => (
          <option key={String(opt)} value={String(opt)}>
            {String(opt)}
          </option>
        ))}
      </select>
    );
  }

  switch (descriptor.type) {
    case "boolean":
      return (
        <label className="inline-flex items-center gap-2 font-mono text-[12.5px] text-ink">
          <input
            id={id}
            type="checkbox"
            disabled={readOnly}
            checked={value === true}
            onChange={(e) => onChange(e.target.checked)}
            className="size-4 accent-accent"
          />
          <span className="text-ink-muted">{value === true ? "true" : "false"}</span>
        </label>
      );
    case "number":
    case "integer":
      return (
        <input
          id={id}
          type="number"
          readOnly={readOnly}
          step={descriptor.type === "integer" ? 1 : "any"}
          value={value === undefined || value === null ? "" : String(value)}
          placeholder={placeholder}
          min={descriptor.minimum}
          max={descriptor.maximum}
          onChange={(e) => onChange(coerceForType(descriptor.type, e.target.value))}
          className="w-full rounded-sm border border-border bg-bg px-2.5 py-1 font-mono text-[12.5px] text-ink focus:border-accent focus:outline-none read-only:opacity-60"
        />
      );
    case "array-of-primitives":
      return (
        <ArrayPrimitiveInput
          id={id}
          descriptor={descriptor}
          value={value}
          onChange={onChange}
          readOnly={readOnly}
        />
      );
    case "kv-map":
      return (
        <KvMapInput
          id={id}
          descriptor={descriptor}
          value={value}
          onChange={onChange}
          readOnly={readOnly}
        />
      );
    case "array-of-objects":
      return (
        <ArrayOfObjectsInput
          descriptor={descriptor}
          value={value}
          onChange={onChange}
          readOnly={readOnly}
        />
      );
    case "array-of-discriminators":
      return (
        <ArrayOfDiscriminatorsInput
          descriptor={descriptor}
          value={value}
          onChange={onChange}
          readOnly={readOnly}
        />
      );
    case "discriminator":
      return (
        <DiscriminatorInput
          descriptor={descriptor}
          value={value}
          onChange={onChange}
          readOnly={readOnly}
        />
      );
    case "string":
    default:
      return (
        <input
          id={id}
          type={descriptor.format === "password" ? "password" : "text"}
          readOnly={readOnly}
          value={value === undefined || value === null ? "" : String(value)}
          placeholder={placeholder}
          minLength={descriptor.minLength}
          maxLength={descriptor.maxLength}
          pattern={descriptor.pattern}
          onChange={(e) => onChange(e.target.value)}
          className="w-full rounded-sm border border-border bg-bg px-2.5 py-1 font-mono text-[12.5px] text-ink focus:border-accent focus:outline-none read-only:opacity-60"
        />
      );
  }
}

// Tag-style editor for array-of-primitives. Adds via Enter; removes
// via × on each chip. Coerces input string per descriptor.itemType.
function ArrayPrimitiveInput({ id, descriptor, value, onChange, readOnly }: InputProps) {
  const arr = Array.isArray(value) ? value : [];
  const itemType = descriptor.itemType ?? "string";
  return (
    <div className="space-y-1.5">
      <div className="flex flex-wrap gap-1.5">
        {arr.map((entry, i) => (
          <span
            key={i}
            className="inline-flex items-center gap-1.5 rounded-sm border border-border bg-surface px-2 py-0.5 font-mono text-[11.5px] text-ink"
          >
            {String(entry)}
            {readOnly ? null : (
              <button
                type="button"
                onClick={() => onChange(arr.filter((_, idx) => idx !== i))}
                className="text-ink-faint hover:text-red"
                aria-label={`remove ${String(entry)}`}
              >
                ×
              </button>
            )}
          </span>
        ))}
      </div>
      {readOnly ? null : (
        <input
          id={id}
          type={itemType === "string" ? "text" : "number"}
          placeholder="add item, press Enter"
          onKeyDown={(e) => {
            if (e.key !== "Enter") return;
            e.preventDefault();
            const raw = (e.currentTarget as HTMLInputElement).value.trim();
            if (raw === "") return;
            const coerced = coerceForType(itemType, raw);
            onChange([...arr, coerced]);
            (e.currentTarget as HTMLInputElement).value = "";
          }}
          className="w-full rounded-sm border border-border bg-bg px-2.5 py-1 font-mono text-[12.5px] text-ink focus:border-accent focus:outline-none"
        />
      )}
    </div>
  );
}

// Key/value-map editor for `additionalProperties: { type: string }`
// shapes. ConfigMap.data, Secret.data (via base64 wrapper),
// Service.selector, metadata.labels / metadata.annotations all come
// through here.
//
// Local-state contract: the widget holds an array of `[key, value]`
// rows that allows EMPTY and DUPLICATE keys as transient UI state.
// Clicking "+ add key" appends an empty-key row the operator types
// into; until they fill it in the row exists only in local state.
// On every mutation we project to the parent's record shape (which
// can't represent empty/duplicate keys), filtering them out — so
// the parent's `obj` and the eventual YAML stay clean.
//
// Without this split the previous implementation filtered empties
// before even calling `onChange`, which meant clicking "+ add key"
// produced no visible row (the new empty entry was discarded
// before reaching state).
function KvMapInput({ descriptor, value, onChange, readOnly }: InputProps) {
  const valueType = descriptor.kvValueType ?? "string";
  const externalEntries = useMemo(() => objToEntries(value), [value]);

  const [entries, setEntries] = useState<[string, unknown][]>(externalEntries);

  // Track what we last projected to the parent so we can detect
  // "external update" (parent gave us new content from outside —
  // e.g. YAML→form roundtrip or schema change) vs "echo" (parent
  // re-rendered with the same record we just sent up).
  const lastProjectedRef = useRef<string>(JSON.stringify(toRecord(externalEntries)));

  // Sync from external prop changes only when the new value differs
  // from what we last projected — otherwise our own update would
  // wipe in-progress empty rows on every keystroke.
  useEffect(() => {
    const externalKey = JSON.stringify(value ?? {});
    if (externalKey !== lastProjectedRef.current) {
      setEntries(externalEntries);
      lastProjectedRef.current = externalKey;
    }
  }, [value, externalEntries]);

  const updateEntries = (next: [string, unknown][]) => {
    setEntries(next);
    const projected = toRecord(next);
    lastProjectedRef.current = JSON.stringify(projected);
    onChange(projected);
  };

  // Mark rows whose key is empty (in-progress) or whose key
  // duplicates an earlier row. The earlier row wins on projection
  // (Object key uniqueness — last write actually wins in JS, but
  // we filter duplicates rather than overwriting). Operator sees
  // a faint warning until they pick a unique key.
  const seen = new Set<string>();
  const rowState = entries.map(([k]) => {
    if (k === "") return "empty" as const;
    if (seen.has(k)) return "duplicate" as const;
    seen.add(k);
    return "ok" as const;
  });

  return (
    <div className="space-y-1.5">
      <div className="space-y-1">
        {entries.map(([k, v], i) => (
          <div key={i}>
            <div className="flex items-center gap-1.5">
              <input
                type="text"
                value={k}
                readOnly={readOnly}
                onChange={(e) => {
                  const next = [...entries];
                  next[i] = [e.target.value, v];
                  updateEntries(next);
                }}
                className={cn(
                  "w-1/3 rounded-sm border bg-bg px-2.5 py-1 font-mono text-[12.5px] text-ink focus:border-accent focus:outline-none read-only:opacity-60",
                  rowState[i] === "ok" ? "border-border" : "border-yellow/50",
                )}
                placeholder="key"
              />
              <input
                type={valueType === "string" ? "text" : "number"}
                value={v === undefined || v === null ? "" : String(v)}
                readOnly={readOnly}
                onChange={(e) => {
                  const next = [...entries];
                  next[i] = [k, coerceForType(valueType, e.target.value)];
                  updateEntries(next);
                }}
                className="flex-1 rounded-sm border border-border bg-bg px-2.5 py-1 font-mono text-[12.5px] text-ink focus:border-accent focus:outline-none read-only:opacity-60"
                placeholder="value"
              />
              {readOnly ? null : (
                <button
                  type="button"
                  onClick={() => updateEntries(entries.filter((_, idx) => idx !== i))}
                  className="font-mono text-[12px] text-ink-faint hover:text-red"
                  aria-label={`remove ${k}`}
                >
                  ×
                </button>
              )}
            </div>
            {rowState[i] === "empty" && (
              <p className="ml-1 mt-0.5 font-mono text-[10.5px] text-yellow">
                fill in a key — empty rows aren't saved
              </p>
            )}
            {rowState[i] === "duplicate" && (
              <p className="ml-1 mt-0.5 font-mono text-[10.5px] text-yellow">
                duplicate key — only the first occurrence is saved
              </p>
            )}
          </div>
        ))}
      </div>
      {readOnly ? null : (
        <button
          type="button"
          onClick={() =>
            updateEntries([...entries, ["", valueType === "string" ? "" : 0]])
          }
          className="font-mono text-[11.5px] text-accent hover:underline"
        >
          + add key
        </button>
      )}
    </div>
  );
}

function objToEntries(value: unknown): [string, unknown][] {
  if (!value || typeof value !== "object" || Array.isArray(value)) return [];
  const obj = value as Record<string, unknown>;
  return Object.keys(obj).map((k) => [k, obj[k]] as [string, unknown]);
}

function toRecord(entries: [string, unknown][]): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const [k, v] of entries) {
    if (k === "") continue;
    if (k in out) continue; // duplicate — first wins, see warning UI
    out[k] = v;
  }
  return out;
}

// Table-of-fieldsets editor for arrays of objects. Each row owns a
// scoped sub-form whose values root is the row item; child
// descriptors carry RELATIVE paths (walker emits them with []) so
// the same FieldRow recursion works inside the row.
function ArrayOfObjectsInput({
  descriptor,
  value,
  onChange,
  readOnly,
}: Omit<InputProps, "id">) {
  const arr = Array.isArray(value) ? (value as Record<string, unknown>[]) : [];
  const childDescriptors = descriptor.children ?? [];

  const updateRow = (idx: number, nextItem: Record<string, unknown>) => {
    const next = [...arr];
    next[idx] = nextItem;
    onChange(next);
  };
  const removeRow = (idx: number) => onChange(arr.filter((_, i) => i !== idx));
  const addRow = () => onChange([...arr, {}]);

  return (
    <div className="space-y-2">
      {arr.map((row, idx) => (
        <fieldset key={idx} className="rounded-sm border border-border px-3 pb-3 pt-2">
          <legend className="flex items-center gap-2 px-1">
            <span className="font-mono text-[11px] uppercase tracking-[0.14em] text-ink-faint">
              {descriptor.label} #{idx + 1}
            </span>
            {readOnly ? null : (
              <button
                type="button"
                onClick={() => removeRow(idx)}
                className="font-mono text-[10.5px] text-ink-faint hover:text-red"
                aria-label={`remove ${descriptor.label} ${idx + 1}`}
              >
                remove
              </button>
            )}
          </legend>
          <div className="space-y-3">
            {childDescriptors.map((child) => (
              <FieldRow
                key={child.path.join(".")}
                descriptor={child}
                values={row}
                issues={[]}
                mode={readOnly ? "edit" : "edit"}
                onChange={(nextRow) => updateRow(idx, nextRow)}
              />
            ))}
          </div>
        </fieldset>
      ))}
      {readOnly ? null : (
        <button
          type="button"
          onClick={addRow}
          className="font-mono text-[11.5px] text-accent hover:underline"
        >
          + add {descriptor.label || "item"}
        </button>
      )}
    </div>
  );
}

// Per-row discriminator picker. Used for arrays whose item type is
// a K8s sibling-encoded oneOf — each row IS a discriminator value
// (`volumes[]` rows are Volume objects, picker = volume type;
// `envFrom[]` rows are EnvFromSource objects, picker = configMap
// vs secret).
//
// Each row delegates to DiscriminatorInput with a synthesized
// "row descriptor" carrying the same `branches` + `sharedChildren`
// the walker emitted on the array descriptor — so branch-switching,
// shared-child rendering, and select-vs-buttons behaviour all
// inherit unchanged.
function ArrayOfDiscriminatorsInput({
  descriptor,
  value,
  onChange,
  readOnly,
}: Omit<InputProps, "id">) {
  const arr = Array.isArray(value) ? (value as unknown[]) : [];

  // Synthesise once per render — DiscriminatorInput only reads
  // branches / sharedChildren off the descriptor.
  const rowDescriptor: FieldDescriptor = {
    path: [],
    label: descriptor.label,
    type: "discriminator",
    required: false,
    branches: descriptor.branches,
    sharedChildren: descriptor.sharedChildren,
  };

  const updateRow = (idx: number, nextRow: unknown) => {
    const next = [...arr];
    next[idx] = nextRow;
    onChange(next);
  };
  const removeRow = (idx: number) => onChange(arr.filter((_, i) => i !== idx));
  // New rows start as `{}` — operator picks a branch first; the
  // discriminator widget seeds the chosen branch's empty value.
  const addRow = () => onChange([...arr, {}]);

  return (
    <div className="space-y-2">
      {arr.map((row, idx) => (
        <fieldset key={idx} className="rounded-sm border border-border px-3 pb-3 pt-2">
          <legend className="flex items-center gap-2 px-1">
            <span className="font-mono text-[11px] uppercase tracking-[0.14em] text-ink-faint">
              {descriptor.label} #{idx + 1}
            </span>
            {readOnly ? null : (
              <button
                type="button"
                onClick={() => removeRow(idx)}
                className="font-mono text-[10.5px] text-ink-faint hover:text-red"
                aria-label={`remove ${descriptor.label} ${idx + 1}`}
              >
                remove
              </button>
            )}
          </legend>
          <DiscriminatorInput
            descriptor={rowDescriptor}
            value={row}
            onChange={(nextRow) => updateRow(idx, nextRow)}
            readOnly={readOnly}
          />
        </fieldset>
      ))}
      {readOnly ? null : (
        <button
          type="button"
          onClick={addRow}
          className="font-mono text-[11.5px] text-accent hover:underline"
        >
          + add {descriptor.label || "item"}
        </button>
      )}
    </div>
  );
}

// Discriminator picker — branch picker (segmented buttons or
// <select> when there are many branches) + a sub-form for the
// chosen branch + optional always-on shared children.
//
// Three descriptor shapes the walker can produce:
//
//   Shape A (whole-value picker): no discriminatorKey on branches.
//     The branch's value IS the discriminator's value. Active-branch
//     detection runs against each branch's required keys (first
//     match wins).
//
//   Shape B (required-key discriminator): branch.discriminatorKey
//     is set; the discriminator's value has shape
//     `{[discriminatorKey]: subValue}`. Active branch = whichever
//     discriminatorKey is present in the value object.
//
//   Shape B + sharedChildren (hinted hybrid): same as Shape B, but
//     `descriptor.sharedChildren` carries fields that live alongside
//     the branch keys and are preserved across branch switches.
//     K8s Probe (handler branches + threshold knobs) and Volume
//     (volume-type branches + `name`) use this shape.
//
// Branch switching deletes the previous branch's discriminatorKey
// from the value (so the apiserver gets a single-handler value) but
// preserves shared keys. Shape A switching wipes the whole value
// since branch shapes don't overlap.
//
// Picker style: ≤5 branches render as segmented buttons; more
// (Volume has ~30) collapse to a <select> for usability.
const SELECT_THRESHOLD = 5;
function DiscriminatorInput({
  descriptor,
  value,
  onChange,
  readOnly,
}: Omit<InputProps, "id">) {
  const branches = descriptor.branches ?? [];
  if (branches.length === 0) return null;
  const sharedChildren = descriptor.sharedChildren ?? [];

  const isObjectValue =
    value !== null && typeof value === "object" && !Array.isArray(value);
  const valueObj = isObjectValue ? (value as Record<string, unknown>) : {};

  // Detect active branch.
  // Shape B fast path: whichever discriminatorKey is set wins.
  // Shape A (no discriminatorKey on branches): no ajv hop here —
  // we infer by matching against each branch's required keys, falling
  // back to "first branch" when nothing decisive is found.
  let active = -1;
  for (let i = 0; i < branches.length; i++) {
    const k = branches[i].discriminatorKey;
    if (k && k in valueObj) {
      active = i;
      break;
    }
  }
  if (active < 0) {
    // Shape A heuristic: match by whichever branch's required-keys
    // are all present in the value. First match wins.
    for (let i = 0; i < branches.length; i++) {
      const req = branches[i].schema.required;
      if (
        Array.isArray(req) &&
        req.length > 0 &&
        req.every((r) => r in valueObj)
      ) {
        active = i;
        break;
      }
    }
  }

  const activeBranch = active >= 0 ? branches[active] : null;
  const isShapeB = branches[0]?.discriminatorKey !== undefined;
  // For confirmation: Shape A wipes the whole value; Shape B/hybrid
  // only wipes the previous branch's key, so check whether THAT key
  // has a non-empty sub-value (sharedChildren stay either way).
  const previousBranchKey = active >= 0 ? branches[active].discriminatorKey : undefined;
  const previousBranchHasValue =
    previousBranchKey !== undefined &&
    valueObj[previousBranchKey] !== undefined &&
    !isEmptyContainer(valueObj[previousBranchKey]);
  const valueIsEmpty = !value || (isObjectValue && Object.keys(valueObj).length === 0);

  const switchBranch = (idx: number) => {
    if (idx === active) return;
    if (active >= 0) {
      const dirty = isShapeB ? previousBranchHasValue : !valueIsEmpty;
      if (dirty) {
        const ok = window.confirm(
          `Switching to "${branches[idx].label}" will discard the values you set under "${branches[active].label}". Continue?`,
        );
        if (!ok) return;
      }
    }
    if (isShapeB) {
      // Hybrid Shape B: preserve sharedChildren keys; replace the
      // previous-branch key with the new branch's empty value.
      const next: Record<string, unknown> = { ...valueObj };
      if (previousBranchKey !== undefined) delete next[previousBranchKey];
      const newKey = branches[idx].discriminatorKey;
      if (newKey !== undefined) next[newKey] = {};
      onChange(next);
    } else {
      // Shape A: branch IS the value, no shared keys.
      onChange({});
    }
  };

  const useSelect = branches.length > SELECT_THRESHOLD;

  return (
    <div className="space-y-2">
      {useSelect ? (
        <select
          value={active >= 0 ? String(active) : ""}
          disabled={readOnly}
          onChange={(e) => switchBranch(parseInt(e.target.value, 10))}
          className="w-full rounded-sm border border-border bg-bg px-2 py-1 font-mono text-[12px] text-ink focus:border-accent focus:outline-none disabled:opacity-50"
        >
          {active < 0 ? (
            <option value="" disabled>
              — pick one —
            </option>
          ) : null}
          {branches.map((b, i) => (
            <option key={i} value={i}>
              {b.label}
            </option>
          ))}
        </select>
      ) : (
        <div className="flex flex-wrap gap-1.5">
          {branches.map((b, i) => (
            <Tooltip key={i} content={b.description} side="top" sideOffset={4}>
              <button
                type="button"
                onClick={() => switchBranch(i)}
                disabled={readOnly}
                className={cn(
                  "rounded-sm border px-2 py-0.5 font-mono text-[11.5px] transition-colors disabled:opacity-50",
                  active === i
                    ? "border-accent bg-accent-soft text-accent"
                    : "border-border text-ink-muted hover:bg-surface-2",
                )}
              >
                {b.label}
              </button>
            </Tooltip>
          ))}
        </div>
      )}
      {activeBranch ? (
        <BranchSubForm
          branch={activeBranch}
          value={valueObj}
          onChange={onChange}
          readOnly={readOnly ?? false}
        />
      ) : (
        <p className="text-[11.5px] italic text-ink-faint">
          Pick an option above to configure.
        </p>
      )}
      {sharedChildren.length > 0 ? (
        <SharedChildrenSection
          descriptors={sharedChildren}
          value={valueObj}
          onChange={onChange}
          readOnly={readOnly ?? false}
        />
      ) : null}
    </div>
  );
}

function isEmptyContainer(v: unknown): boolean {
  if (v === undefined || v === null) return true;
  if (Array.isArray(v)) return v.length === 0;
  if (typeof v === "object") return Object.keys(v as object).length === 0;
  return false;
}

// SharedChildrenSection — renders the always-on properties of a
// hinted hybrid discriminator (Probe thresholds, Volume `name`,
// EnvFromSource `prefix`) as siblings of the branch picker. Each
// child's path is relative to the discriminator's value (same
// convention as Shape B branch descriptors).
function SharedChildrenSection({
  descriptors,
  value,
  onChange,
  readOnly,
}: {
  descriptors: FieldDescriptor[];
  value: Record<string, unknown>;
  onChange: (next: unknown) => void;
  readOnly: boolean;
}) {
  return (
    <fieldset className="rounded-sm border border-border px-3 pb-3 pt-2">
      <legend className="px-1">
        <span className="font-mono text-[10.5px] uppercase tracking-[0.14em] text-ink-faint">
          shared
        </span>
      </legend>
      <div className="space-y-3">
        {descriptors.map((child) => (
          <FieldRow
            key={child.path.join(".")}
            descriptor={child}
            values={value}
            issues={[]}
            mode={readOnly ? "edit" : "edit"}
            onChange={onChange}
          />
        ))}
      </div>
    </fieldset>
  );
}

// Renders the active branch's descriptors. Each descriptor's path is
// already relative to the discriminator's value (the walker prefixed
// Shape B paths with the discriminatorKey), so we can iterate
// branch.descriptors and route each onChange through setAtPath
// against the discriminator's value root.
function BranchSubForm({
  branch,
  value,
  onChange,
  readOnly,
}: {
  branch: NonNullable<FieldDescriptor["branches"]>[number];
  value: Record<string, unknown>;
  onChange: (next: unknown) => void;
  readOnly: boolean;
}) {
  // Empty branch (Shape B "marker" branch where the schema is just
  // `{type: object, properties: {}}` — operator selecting it just
  // sets `{[key]: {}}`). Render a friendly status line.
  if (branch.descriptors.length === 0) {
    return (
      <p className="px-1 py-1 text-[11.5px] italic text-ink-faint">
        No additional configuration required.
      </p>
    );
  }
  return (
    <fieldset className="rounded-sm border border-border px-3 pb-3 pt-2">
      <legend className="px-1">
        <span className="font-mono text-[10.5px] uppercase tracking-[0.14em] text-ink-faint">
          {branch.label}
        </span>
      </legend>
      <div className="space-y-3">
        {branch.descriptors.map((child) => (
          <FieldRow
            key={child.path.join(".")}
            descriptor={child}
            values={value}
            issues={[]}
            mode={readOnly ? "edit" : "edit"}
            onChange={onChange}
          />
        ))}
      </div>
    </fieldset>
  );
}

// ─── coercion / path helpers ────────────────────────────────────

function pathStartsWith(p: string[], prefix: string[]): boolean {
  if (p.length < prefix.length) return false;
  for (let i = 0; i < prefix.length; i++) {
    if (p[i] !== prefix[i]) return false;
  }
  return true;
}

function pathBreadcrumb(path: string[]): string {
  if (path.length <= 1) return "";
  return path.join(".");
}

function coerceForType(type: string | undefined, raw: string): unknown {
  if (raw === "") return undefined;
  switch (type) {
    case "integer": {
      const n = parseInt(raw, 10);
      return Number.isNaN(n) ? raw : n;
    }
    case "number": {
      const n = parseFloat(raw);
      return Number.isNaN(n) ? raw : n;
    }
    case "boolean":
      return raw === "true";
  }
  return raw;
}

function stringify(v: unknown): string {
  if (v === null || v === undefined) return "";
  if (typeof v === "string") return `"${v}"`;
  if (Array.isArray(v)) return `[${v.length} items]`;
  if (typeof v === "object") return "{...}";
  return String(v);
}
