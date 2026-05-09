// helmSchema.ts — historical entry point for the JSON Schema →
// form engine introduced in PR #109. The engine itself moved to
// `lib/schemaForm/` for #116 (so the same code drives K8s OpenAPI
// v3 schemas for ConfigMap / Secret / Service / Ingress); this
// file is now a thin re-export so existing Helm-side imports
// (and the 18 helmSchema vitest cases plus addonInstall.ts's
// generateAddonValuesYamlStub) keep working.
//
// Soft-deprecated: new code should import from `lib/schemaForm`
// directly. Drop this shim in v1.2.

export type {
  JSONSchema,
  FieldDescriptor,
  FieldType,
  ValidationIssue,
} from "./schemaForm/types";
export {
  buildFieldDescriptors,
  hasRequiredUnsupportedField,
} from "./schemaForm/walker";
export { validateValues } from "./schemaForm/validate";
