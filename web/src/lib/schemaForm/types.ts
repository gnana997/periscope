// schemaForm/types.ts — shared types for the JSON Schema → form
// engine. Originally lived in `helmSchema.ts` for the Helm install
// dialog (#74); lifted unchanged into a reusable lib so the same
// engine can render K8s OpenAPI v3 schemas for #116.

// JSON Schema fragment — narrow type that captures what we read.
// Loose typing on most fields because schema validators tolerate
// authoring quirks that strict types would reject.
export interface JSONSchema {
  $schema?: string;
  $ref?: string;
  type?: string | string[];
  title?: string;
  description?: string;
  default?: unknown;
  enum?: unknown[];
  required?: string[];
  properties?: Record<string, JSONSchema>;
  additionalProperties?: boolean | JSONSchema;
  items?: JSONSchema;
  pattern?: string;
  format?: string;
  minimum?: number;
  maximum?: number;
  minLength?: number;
  maxLength?: number;
  // Anything else (allOf, etc.) is tolerated but ignored by the
  // walker unless an explicit option opts in.
  [k: string]: unknown;
}

export type FieldType =
  | "string"
  | "number"
  | "integer"
  | "boolean"
  | "object"
  | "array-of-primitives"
  | "array-of-objects"
  | "kv-map"
  | "discriminator"
  | "unsupported";

/** Where a top-level descriptor lives in the sectioned form layout
 *  (introduced for the K8s ConfigMap/Secret/Service/Ingress curated
 *  forms — Helm consumers leave this unset and get flat rendering).
 *
 *  Section semantics:
 *    primary  — the kind-specific spec content the operator came to
 *               edit. Always visible at the top of the form.
 *    metadata — metadata.name / namespace / labels / annotations.
 *               Useful but rarely the reason someone opened the
 *               editor; collapsed by default.
 *    advanced — Spec fields that are valid but rarely touched
 *               (Service.spec.externalTrafficPolicy etc.) plus
 *               immutable-after-create flags. Hidden behind a
 *               "Show advanced" toggle.
 *
 *  Stamped by the walker when WalkOptions.sectionResolver is
 *  supplied. Children of object descriptors inherit visually from
 *  their parent — only top-level descriptors carry an explicit
 *  section. */
export type FieldSection = string;

/** Per-row sub-section config. The walker stamps an array-of-objects
 *  descriptor with `rowSubSections` so the renderer knows how to
 *  group the row's children into Primary / Probes / Mounts /
 *  Container advanced (or whatever the kind has declared). */
export interface RowSubSectionConfig {
  id: FieldSection;
  label: string;
  defaultOpen?: boolean;
  openWhenPopulated?: boolean;
  showCount?: boolean;
}

/** Sibling-property hint for K8s-style polymorphism — see WalkOptions
 *  `discriminatorHints`. K8s encodes Probe / Volume / EnvVarSource /
 *  LifecycleHandler this way (mutually exclusive sibling props with
 *  no `oneOf`); the hint table tells the walker to emit a Shape B
 *  discriminator instead of a flat object. */
export interface DiscriminatorHint {
  /** Property keys that are mutually exclusive — exactly one of them
   *  should be set on the value at a time. The walker emits one
   *  branch per key. */
  branches: string[];
  /** Optional human label per branch (defaults to the key). */
  labels?: Record<string, string>;
}

/** One option in a `discriminator`-typed field. The renderer shows a
 *  branch picker (segmented buttons) and recurses into the chosen
 *  branch's `schema` to render the sub-form. `discriminatorKey` is
 *  set when the parent shape was an object-level oneOf with each
 *  branch having `required: [singleKey]` (cert-manager Issuer style)
 *  — the renderer uses key-presence to detect the active branch
 *  without needing a full ajv-validate pass. */
export interface DiscriminatorBranch {
  label: string;
  description?: string;
  schema: JSONSchema;
  /** When set (object-level oneOf with required-key branches), the
   *  discriminator's value has shape `{[discriminatorKey]: subValue}`
   *  and the renderer detects the active branch via key-presence
   *  rather than ajv validation. Cert-manager Issuer style. */
  discriminatorKey?: string;
  /** Pre-walked child descriptors. Paths are relative to the
   *  discriminator's value (which is what `setAtPath` operates on),
   *  so the renderer iterates these and dispatches the standard
   *  FieldRow recursion without needing access to WalkOptions. */
  descriptors: FieldDescriptor[];
}

export interface FieldDescriptor {
  /** JSON-pointer-style path from root, e.g. ["resources", "limits", "cpu"]. */
  path: string[];
  /** Human label — falls back to the last path segment when title absent. */
  label: string;
  type: FieldType;
  description?: string;
  required: boolean;
  default?: unknown;
  enum?: unknown[];
  /** For type=array-of-primitives: the primitive type of the elements. */
  itemType?: "string" | "number" | "integer" | "boolean";
  /** For type=object or type=array-of-objects: the nested field
   *  descriptors (recursive). */
  children?: FieldDescriptor[];
  /** For type=kv-map: the schema of the values (always primitive). */
  kvValueType?: "string" | "number" | "integer" | "boolean";
  /** For type=discriminator: the N options the operator picks
   *  between. The renderer recurses into the chosen branch's
   *  schema to render the sub-form. */
  branches?: DiscriminatorBranch[];
  /** For type=discriminator (hinted shape): properties that are
   *  NOT branch picks but should still render alongside the picker
   *  (e.g. Probe's thresholds — initialDelaySeconds, periodSeconds —
   *  rendered above the branch-specific sub-form, preserved across
   *  branch switches). */
  sharedChildren?: FieldDescriptor[];
  /** Constraints surfaced for inline validation hints. */
  pattern?: string;
  format?: string;
  minimum?: number;
  maximum?: number;
  minLength?: number;
  maxLength?: number;
  /** When set to "create-only" the widget should render the field
   *  read-only on edit forms (e.g. metadata.name on K8s objects). */
  editable?: "create-only";
  /** For type=unsupported, why we couldn't render this field as a
   *  form. Surfaced as a "edit in YAML mode" hint. */
  unsupportedReason?: string;
  /** Curated layout grouping — set on top-level descriptors by the
   *  walker when WalkOptions.sectionResolver is supplied. The K8s
   *  forms use this to render primary fields above the fold and
   *  collapse metadata/advanced. Helm + other unsectioned consumers
   *  leave this undefined and get the flat-list render. */
  section?: FieldSection;
  /** Explicit ordering within a section. Lower number renders first.
   *  Set alongside `section` by the walker. When unset, descriptors
   *  fall back to schema-walk order within their section. */
  displayOrder?: number;
  /** Sub-section grouping for an array-of-objects descriptor's row
   *  children. Stamped by the walker when WalkOptions.subSectionResolver
   *  matches. Renderer reads this list to draw L2 sections (e.g.
   *  Primary / Probes / Mounts / Container advanced) inside each row. */
  rowSubSections?: RowSubSectionConfig[];
}

export interface ValidationIssue {
  /** JSON-pointer-style path to the offending value. */
  path: string[];
  /** Human-friendly violation message. */
  message: string;
  /** Original ajv keyword that flagged the issue (required / type /
   *  enum / pattern / etc.) — useful for inline UI affordances. */
  keyword: string;
}
