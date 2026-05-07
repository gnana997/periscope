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
  | "unsupported";

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
