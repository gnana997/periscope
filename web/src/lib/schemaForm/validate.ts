// schemaForm/validate.ts — ajv-backed validation. Lifted from
// helmSchema.ts:213-287 unchanged so the existing 18 vitest cases
// keep passing under the new import path.

import Ajv, { type ErrorObject } from "ajv";
import type { JSONSchema, ValidationIssue } from "./types";

const ajv = new Ajv({
  // Authors don't always set "type" everywhere; allow.
  strict: false,
  // Schemas can use "format: email" etc. without ajv-formats; we
  // tolerate by treating them as no-ops for validation.
  allErrors: true,
});

/** Compile + validate. Returns [] when the value is valid. */
export function validateValues(
  schema: JSONSchema,
  values: unknown,
): ValidationIssue[] {
  if (!schema || typeof schema !== "object") return [];
  let validate;
  try {
    validate = ajv.compile(schema);
  } catch {
    // Ajv refuses some schemas (rare in practice). Treat as
    // "no validation" rather than crashing the form.
    return [];
  }
  validate(values);
  const errs = (validate.errors ?? []) as ErrorObject[];
  return errs.map(errorToIssue);
}

function errorToIssue(err: ErrorObject): ValidationIssue {
  const instancePath = err.instancePath ?? "";
  // ajv's instancePath is "/foo/bar"; split into path segments.
  const path = instancePath
    .split("/")
    .filter(Boolean)
    // Ajv encodes ~ as ~0 and / as ~1 per RFC 6901.
    .map((s) => s.replace(/~1/g, "/").replace(/~0/g, "~"));
  return {
    path,
    message: humanize(err),
    keyword: err.keyword,
  };
}

function humanize(err: ErrorObject): string {
  switch (err.keyword) {
    case "required":
      return `missing required field "${(err.params as { missingProperty?: string }).missingProperty ?? ""}"`;
    case "type":
      return `must be ${(err.params as { type?: string }).type ?? "the right type"}`;
    case "enum":
      return `must be one of: ${(err.params as { allowedValues?: unknown[] }).allowedValues?.join(", ") ?? ""}`;
    case "pattern":
      return `does not match pattern ${(err.params as { pattern?: string }).pattern ?? ""}`;
    case "minimum":
      return `must be ≥ ${(err.params as { limit?: number }).limit ?? ""}`;
    case "maximum":
      return `must be ≤ ${(err.params as { limit?: number }).limit ?? ""}`;
    case "minLength":
      return `must have ≥ ${(err.params as { limit?: number }).limit ?? ""} characters`;
    case "maxLength":
      return `must have ≤ ${(err.params as { limit?: number }).limit ?? ""} characters`;
  }
  return err.message ?? "schema violation";
}
