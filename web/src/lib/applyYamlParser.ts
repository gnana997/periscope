// applyYamlParser — splits a multi-doc YAML blob into per-doc records,
// extracts the four fields the apply pipeline needs (apiVersion, kind,
// metadata.name, metadata.namespace), and surfaces parse errors at the
// doc level so the dialog can show "doc 3 of 5: bad indent" without
// failing the entire paste.
//
// Stub in this commit; real parsing arrives in a later commit of the
// same PR.

export interface ParsedDoc {
  /** Stable ID for React keys. Hash of (raw + index). */
  id: string;
  /** Original YAML text for this single document. */
  raw: string;
  /** True when the four required fields are present and YAML parsed clean. */
  valid: boolean;
  apiVersion?: string;
  kind?: string;
  name?: string;
  namespace?: string;
  /** Human-readable YAML / validation error. */
  parseError?: string;
}

/**
 * parseMultiDocYaml — placeholder; returns no docs. Real implementation
 * lands in the parser commit.
 */
export function parseMultiDocYaml(_yamlText: string): ParsedDoc[] {
  return [];
}
