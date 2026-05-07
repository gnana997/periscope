// schemaForm/index.ts — barrel for the JSON Schema → form engine.
// Helm and K8s call sites import from here.

export type {
  JSONSchema,
  FieldDescriptor,
  FieldType,
  ValidationIssue,
} from "./types";
export type { WalkOptions } from "./walker";
export type { SchemaFormMode, SchemaFormProps } from "./SchemaForm";
export type { SchemaFormBridgeProps } from "./SchemaFormBridge";

export { buildFieldDescriptors } from "./walker";
export { validateValues } from "./validate";
export { SchemaForm } from "./SchemaForm";
export { getAtPath, setAtPath } from "./pathOps";
export { SchemaFormBridge } from "./SchemaFormBridge";
