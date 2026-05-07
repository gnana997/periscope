// HelmValuesForm — historical entry point for the form renderer
// introduced in PR #109. The renderer itself moved to
// `lib/schemaForm/SchemaForm.tsx` for #116 reuse; this file
// re-exports it under the original name so existing Helm call
// sites compile unchanged.

export { SchemaForm as HelmValuesForm } from "../../lib/schemaForm/SchemaForm";
