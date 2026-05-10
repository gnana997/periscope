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
import {
  collectRowSubSections,
  collectSectioned,
  descendantHasSection,
  descriptorHasContent,
} from "./sections";
import { cn } from "../cn";
import { getAtPath, setAtPath } from "./pathOps";
import type {
  FieldDescriptor,
  FieldSection,
  JSONSchema,
  RowSubSectionConfig,
  ValidationIssue,
} from "./types";
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

/** One section in the form's L1 layout. The renderer draws sections
 *  in declared order; the first one with `defaultOpen=true` renders
 *  as a `<section>` with a heading; the rest render as `<details>`
 *  with a clickable summary. */
export interface SchemaFormSectionConfig {
  id: FieldSection;
  label: string;
  defaultOpen?: boolean;
  /** Override defaultOpen=false when the section's bucket has any
   *  populated descriptor — useful for Volumes where editing
   *  volumeMounts without seeing volumes is a footgun. */
  openWhenPopulated?: boolean;
  /** Append "(N)" field count in the summary. Used for Advanced. */
  showCount?: boolean;
}

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
  /** When set, descriptors are grouped into the declared sections in
   *  order. K8sSchemaForm passes `getSections(kind)`; Helm leaves it
   *  undefined to keep the legacy flat layout. */
  sections?: SchemaFormSectionConfig[];
}

export function SchemaForm({
  schema,
  values,
  onChange,
  walkOptions,
  mode = "edit",
  emptyMessage,
  sections,
}: SchemaFormProps) {
  const descriptors = useMemo(
    () => buildFieldDescriptors(schema, walkOptions),
    [schema, walkOptions],
  );
  const issues = useMemo(() => validateValues(schema, values), [schema, values]);
  const sectioned = useMemo(() => collectSectioned(descriptors), [descriptors]);

  if (descriptors.length === 0) {
    return (
      <div className="px-3 py-4 text-[13px] text-ink-muted">
        {emptyMessage ?? "schema produced no editable fields."}
      </div>
    );
  }

  const renderRow = (d: FieldDescriptor) => (
    <FieldRow
      key={d.path.join(".")}
      descriptor={d}
      values={values}
      issues={issues}
      mode={mode}
      onChange={onChange}
    />
  );

  // Back-compat fallback: when the caller hasn't asked for sections,
  // OR no descriptor has a section stamp (Helm path), render the flat
  // ordered list exactly like the pre-#142 layout.
  if (!sections || sections.length === 0 || sectioned.total === 0) {
    return (
      <form className="space-y-3" onSubmit={(e) => e.preventDefault()}>
        {descriptors.map(renderRow)}
      </form>
    );
  }

  // "Other" catches top-level descriptors that aren't sectioned and
  // whose children also aren't — typically forward-compatible cases
  // where a future K8s field for a supported kind hasn't been added
  // to the per-kind allowlist yet. Render at the bottom so they're
  // discoverable rather than silently dropped.
  const others = descriptors.filter(
    (d) => d.section === undefined && !descendantHasSection(d),
  );

  return (
    <form className="space-y-3" onSubmit={(e) => e.preventDefault()}>
      {sections.map((section) => {
        const bucket = sectioned.byId.get(section.id) ?? [];
        if (bucket.length === 0) return null;
        const populated = section.openWhenPopulated
          ? bucket.some((d) => descriptorHasContent(d, values))
          : false;
        const isOpen = section.defaultOpen ?? populated;
        const summaryText = section.showCount
          ? `${section.label} (${bucket.length})`
          : section.label;
        if (section.defaultOpen) {
          return (
            <section key={section.id} className="space-y-3">
              <h3 className="font-mono text-[11px] uppercase tracking-[0.14em] text-ink-faint">
                {section.label}
              </h3>
              <div className="space-y-3">{bucket.map(renderRow)}</div>
            </section>
          );
        }
        return (
          <details
            key={section.id}
            className="group rounded-sm border border-border"
            open={isOpen}
          >
            <summary className="cursor-pointer select-none px-3 py-2 font-mono text-[11px] uppercase tracking-[0.14em] text-ink-faint transition-colors hover:text-ink">
              {summaryText}
            </summary>
            <div className="space-y-3 px-3 pb-3 pt-1">{bucket.map(renderRow)}</div>
          </details>
        );
      })}

      {others.length > 0 && (
        <details className="group rounded-sm border border-border">
          <summary className="cursor-pointer select-none px-3 py-2 font-mono text-[11px] uppercase tracking-[0.14em] text-ink-faint transition-colors hover:text-ink">
            Other ({others.length})
          </summary>
          <div className="space-y-3 px-3 pb-3 pt-1">{others.map(renderRow)}</div>
        </details>
      )}
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
            <ArrayRowChildren
              row={row}
              rowIdx={idx}
              childDescriptors={childDescriptors}
              subSections={descriptor.rowSubSections}
              onChange={updateRow}
              readOnly={readOnly}
            />
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

// Discriminator picker — segmented branch buttons + a sub-form that
// renders the chosen branch's descriptors. Branch switching is
// destructive (operator's data under the previous branch gets wiped
// because schemas across branches don't share property shapes —
// trying to merge would produce ambiguous "is this branch fully
// filled?" states). Confirm-then-wipe mirrors the form↔yaml toggle
// pattern in KindEditRouter.
//
// Two shapes the descriptor can carry, set by the walker:
//
//   Shape A (whole-value picker): branch.descriptors are paths
//     relative to the discriminator's value. The branch's value
//     IS the discriminator's value. Active-branch detection runs
//     ajv-validate against each branch's schema (the first match
//     wins).
//
//   Shape B (required-key discriminator): branch.discriminatorKey
//     is set; the discriminator's value has shape
//     `{[discriminatorKey]: subValue}`. Active-branch detection
//     is fast-path: whichever branch's discriminatorKey is present
//     in the value object is the active one.
function DiscriminatorInput({
  descriptor,
  value,
  onChange,
  readOnly,
}: Omit<InputProps, "id">) {
  const branches = descriptor.branches ?? [];
  if (branches.length === 0) return null;

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
  const valueIsEmpty = !value || (isObjectValue && Object.keys(valueObj).length === 0);

  const switchBranch = (idx: number) => {
    if (idx === active) return;
    if (active >= 0 && !valueIsEmpty) {
      const ok = window.confirm(
        `Switching to "${branches[idx].label}" will discard the values you set under "${branches[active].label}". Continue?`,
      );
      if (!ok) return;
    }
    // Seed an empty value of the new branch's shape. Shape B needs
    // {[discriminatorKey]: {}}; Shape A needs whatever shape the
    // branch schema describes — empty object covers most cases
    // since branches with primitive value (Service.targetPort etc.)
    // expect the operator to type the primitive directly.
    const next: unknown = branches[idx].discriminatorKey
      ? { [branches[idx].discriminatorKey as string]: {} }
      : {};
    onChange(next);
  };

  return (
    <div className="space-y-2">
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
    </div>
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

// ArrayRowChildren — renders an array-of-objects ROW's children.
// When the descriptor carries a rowSubSections list (kind has
// declared per-row sub-section grouping for this array), the
// children get grouped by section and rendered as L2 blocks (a
// primary `<section>` + collapsed `<details>` for the rest).
// When no rowSubSections are set, falls back to the flat list
// render — preserves Helm + non-sectioned-K8s behavior.
function ArrayRowChildren({
  row,
  rowIdx,
  childDescriptors,
  subSections,
  onChange,
  readOnly,
}: {
  row: Record<string, unknown>;
  rowIdx: number;
  childDescriptors: FieldDescriptor[];
  subSections?: RowSubSectionConfig[];
  onChange: (idx: number, next: Record<string, unknown>) => void;
  readOnly?: boolean;
}) {
  const renderRow = (child: FieldDescriptor) => (
    <FieldRow
      key={child.path.join(".")}
      descriptor={child}
      values={row}
      issues={[]}
      mode={readOnly ? "edit" : "edit"}
      onChange={(nextRow) => onChange(rowIdx, nextRow)}
    />
  );

  if (!subSections || subSections.length === 0) {
    // No sub-section grouping declared — flat render as before.
    return <>{childDescriptors.map(renderRow)}</>;
  }

  const grouped = collectRowSubSections(childDescriptors);
  if (grouped.total === 0) {
    return <>{childDescriptors.map(renderRow)}</>;
  }

  // Children that didn't match any sub-section path stay visible at
  // the bottom in an "Other" fold so we don't silently drop new K8s
  // fields the allowlist hasn't been updated for.
  const others = childDescriptors.filter((c) => c.section === undefined);

  return (
    <>
      {subSections.map((sub) => {
        const bucket = grouped.byId.get(sub.id) ?? [];
        if (bucket.length === 0) return null;
        const populated = sub.openWhenPopulated
          ? bucket.some((d) => descriptorHasContent(d, row))
          : false;
        const isOpen = sub.defaultOpen ?? populated;
        const summaryText = sub.showCount
          ? `${sub.label} (${bucket.length})`
          : sub.label;
        if (sub.defaultOpen) {
          return (
            <div key={sub.id} className="space-y-2">
              {bucket.map(renderRow)}
            </div>
          );
        }
        return (
          <details
            key={sub.id}
            className="rounded-sm border border-border/60"
            open={isOpen}
          >
            <summary className="cursor-pointer select-none px-2.5 py-1.5 font-mono text-[10.5px] tracking-[0.08em] text-ink-faint transition-colors hover:text-ink">
              {summaryText}
            </summary>
            <div className="space-y-2 px-2.5 pb-2.5 pt-1">{bucket.map(renderRow)}</div>
          </details>
        );
      })}
      {others.length > 0 && (
        <details className="rounded-sm border border-border/60">
          <summary className="cursor-pointer select-none px-2.5 py-1.5 font-mono text-[10.5px] tracking-[0.08em] text-ink-faint transition-colors hover:text-ink">
            Other ({others.length})
          </summary>
          <div className="space-y-2 px-2.5 pb-2.5 pt-1">{others.map(renderRow)}</div>
        </details>
      )}
    </>
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
