// HelmValuesForm — renders a JSON-Schema-driven form for editing
// Helm chart values (#74). Recursive: nested objects → fieldsets,
// scalars → typed inputs, arrays of primitives → tag-list editor.
//
// State model:
//   - The CALLER owns the values object as a plain JS record.
//   - Each field's onChange invokes setAtPath() to produce a new
//     immutable values object, and bubbles through onChange.
//   - Validation runs on every change; issues are passed down by
//     path so each field can render its inline error.
//
// Why we don't co-locate state inside the form: the parent
// HelmInstallDialog needs the same values when toggling between
// form and YAML mode (planned for v1.2 polish), and again when
// the eventual install action posts them. Single source of truth.

import { useId, useMemo } from "react";
import {
  buildFieldDescriptors,
  validateValues,
  type FieldDescriptor,
  type JSONSchema,
  type ValidationIssue,
} from "../../lib/helmSchema";

interface HelmValuesFormProps {
  schema: JSONSchema;
  /** Current values — typically the parsed values.yaml. The form
   *  treats this as immutable; onChange returns a new tree. */
  values: Record<string, unknown>;
  onChange: (next: Record<string, unknown>) => void;
}

export function HelmValuesForm({ schema, values, onChange }: HelmValuesFormProps) {
  const descriptors = useMemo(() => buildFieldDescriptors(schema), [schema]);
  const issues = useMemo(() => validateValues(schema, values), [schema, values]);

  if (descriptors.length === 0) {
    return (
      <div className="px-3 py-4 text-[13px] text-ink-muted">
        chart ships a schema but it doesn't describe an editable values
        object — nothing to render in form mode.
      </div>
    );
  }

  return (
    <form
      className="space-y-3"
      onSubmit={(e) => e.preventDefault()}
    >
      {descriptors.map((d) => (
        <FieldRow
          key={d.path.join(".")}
          descriptor={d}
          values={values}
          issues={issues}
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
  onChange: (next: Record<string, unknown>) => void;
}

function FieldRow({ descriptor, values, issues, onChange }: FieldRowProps) {
  const id = useId();
  const fieldIssues = useMemo(
    () => issues.filter((i) => pathStartsWith(i.path, descriptor.path) && i.path.length === descriptor.path.length),
    [issues, descriptor.path],
  );
  const value = getAtPath(values, descriptor.path);
  const update = (next: unknown) => onChange(setAtPath(values, descriptor.path, next));

  if (descriptor.type === "object") {
    const subIssues = issues; // pass full list down; nested rows filter themselves
    return (
      <fieldset className="rounded-sm border border-border px-3 pb-3 pt-2">
        <legend className="px-1 font-mono text-[11px] uppercase tracking-[0.14em] text-ink-faint">
          {descriptor.label}
          {descriptor.required ? " *" : ""}
        </legend>
        {descriptor.description ? (
          <p className="mt-1 mb-2 text-[12px] text-ink-muted">{descriptor.description}</p>
        ) : null}
        <div className="space-y-3">
          {(descriptor.children ?? []).map((child) => (
            <FieldRow
              key={child.path.join(".")}
              descriptor={child}
              values={values}
              issues={subIssues}
              onChange={onChange}
            />
          ))}
        </div>
      </fieldset>
    );
  }

  if (descriptor.type === "unsupported") {
    return (
      <div className="rounded-sm border border-border bg-surface px-3 py-2">
        <div className="flex items-baseline justify-between gap-3">
          <span className="font-mono text-[12px] text-ink">
            {descriptor.path.join(".")}
            {descriptor.required ? <span className="text-red"> *</span> : null}
          </span>
          <span className="font-mono text-[10.5px] uppercase tracking-[0.12em] text-ink-faint">
            edit in yaml mode
          </span>
        </div>
        <p className="mt-1 text-[12px] text-ink-muted">
          {descriptor.unsupportedReason} — toggle to YAML above to set this field.
        </p>
      </div>
    );
  }

  return (
    <div>
      <div className="flex items-baseline gap-2">
        <label
          htmlFor={id}
          className="font-mono text-[12px] text-ink"
        >
          {descriptor.label}
          {descriptor.required ? <span className="text-red"> *</span> : null}
        </label>
        <span className="font-mono text-[10.5px] text-ink-faint">
          {pathBreadcrumb(descriptor.path)}
        </span>
      </div>
      {descriptor.description ? (
        <p className="mt-0.5 text-[12px] text-ink-muted">{descriptor.description}</p>
      ) : null}
      <div className="mt-1.5">
        <FieldInput id={id} descriptor={descriptor} value={value} onChange={update} />
      </div>
      {fieldIssues.map((iss, i) => (
        <p key={i} className="mt-1 font-mono text-[11.5px] text-red">
          {iss.message}
        </p>
      ))}
    </div>
  );
}

// ─── input components by type ────────────────────────────────────

interface InputProps {
  id: string;
  descriptor: FieldDescriptor;
  value: unknown;
  onChange: (next: unknown) => void;
}

function FieldInput({ id, descriptor, value, onChange }: InputProps) {
  const placeholder =
    descriptor.default !== undefined ? `default: ${stringify(descriptor.default)}` : "";

  // Enum → select for any primitive type.
  if (descriptor.enum && descriptor.enum.length > 0) {
    return (
      <select
        id={id}
        value={value === undefined ? "" : String(value)}
        onChange={(e) => onChange(coerceForType(descriptor.type, e.target.value))}
        className="w-full rounded-sm border border-border bg-bg px-2.5 py-1 font-mono text-[12.5px] text-ink focus:border-accent focus:outline-none"
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
          step={descriptor.type === "integer" ? 1 : "any"}
          value={value === undefined || value === null ? "" : String(value)}
          placeholder={placeholder}
          min={descriptor.minimum}
          max={descriptor.maximum}
          onChange={(e) => onChange(coerceForType(descriptor.type, e.target.value))}
          className="w-full rounded-sm border border-border bg-bg px-2.5 py-1 font-mono text-[12.5px] text-ink focus:border-accent focus:outline-none"
        />
      );
    case "array-of-primitives":
      return (
        <ArrayPrimitiveInput id={id} descriptor={descriptor} value={value} onChange={onChange} />
      );
    case "string":
    default:
      return (
        <input
          id={id}
          type={descriptor.format === "password" ? "password" : "text"}
          value={value === undefined || value === null ? "" : String(value)}
          placeholder={placeholder}
          minLength={descriptor.minLength}
          maxLength={descriptor.maxLength}
          pattern={descriptor.pattern}
          onChange={(e) => onChange(e.target.value)}
          className="w-full rounded-sm border border-border bg-bg px-2.5 py-1 font-mono text-[12.5px] text-ink focus:border-accent focus:outline-none"
        />
      );
  }
}

// Tag-style editor for array-of-primitives. Adds via Enter; removes
// via × on each chip. Coerces input string per descriptor.itemType.
function ArrayPrimitiveInput({ id, descriptor, value, onChange }: InputProps) {
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
            <button
              type="button"
              onClick={() => onChange(arr.filter((_, idx) => idx !== i))}
              className="text-ink-faint hover:text-red"
              aria-label={`remove ${String(entry)}`}
            >
              ×
            </button>
          </span>
        ))}
      </div>
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
    </div>
  );
}

// ─── path / coercion helpers ────────────────────────────────────

function getAtPath(obj: unknown, path: string[]): unknown {
  let cur: unknown = obj;
  for (const seg of path) {
    if (cur === null || cur === undefined || typeof cur !== "object") return undefined;
    cur = (cur as Record<string, unknown>)[seg];
  }
  return cur;
}

function setAtPath(
  obj: Record<string, unknown>,
  path: string[],
  value: unknown,
): Record<string, unknown> {
  if (path.length === 0) return obj;
  const [head, ...rest] = path;
  const next = { ...obj };
  if (rest.length === 0) {
    next[head] = value;
  } else {
    const child = (obj[head] && typeof obj[head] === "object" && !Array.isArray(obj[head]))
      ? (obj[head] as Record<string, unknown>)
      : {};
    next[head] = setAtPath(child, rest, value);
  }
  return next;
}

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

function coerceForType(
  type: string | undefined,
  raw: string,
): unknown {
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
