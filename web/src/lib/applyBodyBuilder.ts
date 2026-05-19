// applyBodyBuilder — single source of truth for SSA Apply request bodies.
//
// As of issue #224 every call site (YAML editor, form-mode submit,
// every mutation hook in src/hooks/mutations) routes through
// `buildApplyBody(baseline, draft, identity)`. The body is a pure
// minimal diff: identity header + the fields the operator changed
// between baseline and draft. Nothing else.
//
// The retained-ownership composition pattern that lived here previously
// (re-asserting every field periscope-spa already owned, drawn from
// managedFields) was removed after the v1.1 hotfix series exposed
// three failure modes that all traced back to the composition logic.
// See docs/architecture/ssa-strategy.md for the architectural
// background and the closed v1.1.x history.
//
// The only error this layer can throw is whatever computeOps's
// parseOrThrow surfaces — YamlParseError, MultiDocumentError. Callers
// catch and route to the user-facing banner.

import { buildMinimalSSA, computeOps, type Identity, type Op } from "./yamlPatch";

export interface ApplyBodyResult {
  yaml: string;
  ops: Op[];
}

/**
 * Build the SSA apply body for a YAML editor or form-mode submit.
 * Returns both the YAML payload to PATCH and the Op[] the caller may
 * want to inspect (e.g. to suppress submit when ops.length === 0).
 *
 * The payload contains exactly the fields the user changed between
 * `baseline` and `draft`, plus the identity header (apiVersion / kind
 * / metadata.{name, namespace?}). Field ownership is left to SSA's
 * per-key resolution at the apiserver.
 *
 * Throws `YamlParseError` / `MultiDocumentError` from `parseOrThrow`
 * on malformed input — callers should surface those to the user.
 */
export function buildApplyBody(
  baseline: string,
  draft: string,
  identity: Identity,
): ApplyBodyResult {
  const ops = computeOps(baseline, draft);
  const yaml = buildMinimalSSA(ops, identity);
  return { yaml, ops };
}
