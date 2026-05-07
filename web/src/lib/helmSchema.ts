// helmSchema.ts — historical entry point for the JSON Schema →
// form engine introduced in PR #109. The engine itself moved to
// `lib/schemaForm/` for #116 (so the same code drives K8s OpenAPI
// v3 schemas for ConfigMap / Secret / Service / Ingress); this
// file is now a thin re-export so existing Helm-side imports
// (and the 18 helmSchema vitest cases) keep working.

export type {
  JSONSchema,
  FieldDescriptor,
  FieldType,
  ValidationIssue,
} from "./schemaForm/types";
export { buildFieldDescriptors } from "./schemaForm/walker";
export { validateValues } from "./schemaForm/validate";
