// applyYamlParser — splits a multi-doc YAML blob into per-doc records,
// extracts the four fields the apply pipeline needs (apiVersion, kind,
// metadata.name, metadata.namespace), and surfaces parse errors at the
// doc level so the dialog can show "doc 3 of 5: bad indent" without
// failing the entire paste.
//
// Built on the `yaml` package's parseAllDocuments — same library the
// existing inline editor uses (yamlPatch.ts, k8sSchema.ts).

import { parseAllDocuments } from "yaml";

export interface ParsedDoc {
  /** Stable ID for React keys. Hash of raw + index. */
  id: string;
  /** Original YAML text for this single document, normalised (no leading separator). */
  raw: string;
  /** True when YAML parsed clean AND the four required fields are present. */
  valid: boolean;
  apiVersion?: string;
  kind?: string;
  name?: string;
  namespace?: string;
  /** Human-readable error covering YAML parse OR missing-field issues. */
  parseError?: string;
}

/**
 * parseMultiDocYaml splits the input on YAML document boundaries
 * (`---`) and parses each segment.
 *
 * Empty leading / trailing whitespace-only documents are dropped; this
 * is consistent with `kubectl apply -f`'s behaviour on multi-doc files
 * that start or end with a `---` separator.
 *
 * Validation: each doc must have `apiVersion`, `kind`, and
 * `metadata.name`. `metadata.namespace` is optional (cluster-scoped
 * resources don't have one). Missing fields surface as `parseError`
 * rather than throwing.
 */
export function parseMultiDocYaml(yamlText: string): ParsedDoc[] {
  if (yamlText.trim().length === 0) return [];

  // parseAllDocuments handles `---` separators and tracks per-doc
  // ranges in the source via Document#range. We use the range to slice
  // the original text so each ParsedDoc.raw is the verbatim YAML the
  // operator wrote, including comments and formatting — important for
  // the apply call (server-side YAML normalisation has surprises).
  const docs = parseAllDocuments(yamlText);
  const out: ParsedDoc[] = [];
  let idx = 0;

  for (const doc of docs) {
    const range = doc.range; // [contentStart, contentEnd, eofOrNextStart]
    const sliceStart = range?.[0] ?? 0;
    const sliceEnd = range?.[1] ?? yamlText.length;
    const raw = yamlText.slice(sliceStart, sliceEnd).trim();

    // parseAllDocuments emits an empty doc for buffers like "---\n---\n"
    // where there's no content between separators. Skip those.
    if (raw.length === 0 || doc.contents == null) {
      continue;
    }

    const id = `doc-${idx}-${stableHash(raw)}`;

    // YAML-level errors (bad indent, unclosed quote, etc.) come back
    // on doc.errors. Surface the first one; the rest are usually
    // cascades of the same root cause.
    const yamlError = doc.errors?.[0]?.message;
    if (yamlError) {
      out.push({ id, raw, valid: false, parseError: yamlError });
      idx += 1;
      continue;
    }

    const obj = doc.toJS() as unknown;
    if (obj == null) {
      // Empty doc (e.g. content between adjacent --- separators,
      // or trailing --- with no body). Silently skip rather than
      // emit a parseError — this is a YAML quirk, not user fault.
      idx += 1;
      continue;
    }
    if (typeof obj !== "object") {
      out.push({
        id,
        raw,
        valid: false,
        parseError: "document is not a YAML mapping",
      });
      idx += 1;
      continue;
    }
    const meta = (obj as { metadata?: { name?: unknown; namespace?: unknown } })
      .metadata;
    const apiVersion = (obj as { apiVersion?: unknown }).apiVersion;
    const kind = (obj as { kind?: unknown }).kind;
    const name = meta?.name;
    const namespace = meta?.namespace;

    const missing: string[] = [];
    if (typeof apiVersion !== "string" || apiVersion.length === 0) {
      missing.push("apiVersion");
    }
    if (typeof kind !== "string" || kind.length === 0) {
      missing.push("kind");
    }
    if (typeof name !== "string" || name.length === 0) {
      missing.push("metadata.name");
    }

    if (missing.length > 0) {
      out.push({
        id,
        raw,
        valid: false,
        apiVersion: typeof apiVersion === "string" ? apiVersion : undefined,
        kind: typeof kind === "string" ? kind : undefined,
        name: typeof name === "string" ? name : undefined,
        namespace: typeof namespace === "string" ? namespace : undefined,
        parseError: `missing required field(s): ${missing.join(", ")}`,
      });
      idx += 1;
      continue;
    }

    out.push({
      id,
      raw,
      valid: true,
      apiVersion: apiVersion as string,
      kind: kind as string,
      name: name as string,
      namespace: typeof namespace === "string" ? namespace : undefined,
    });
    idx += 1;
  }

  return out;
}

// stableHash — non-cryptographic 32-bit FNV-1a, just enough to disambiguate
// React keys when two adjacent docs have similar but distinct content.
function stableHash(s: string): string {
  let h = 0x811c9dc5;
  for (let i = 0; i < s.length; i += 1) {
    h ^= s.charCodeAt(i);
    h = Math.imul(h, 0x01000193);
  }
  return (h >>> 0).toString(16).padStart(8, "0");
}

import { KIND_REGISTRY } from "./k8sKinds";

export interface GVR {
  group: string;
  version: string;
  resource: string;
}

/**
 * gvrFromApiVersionAndKind splits a Kubernetes apiVersion + kind into
 * the (group, version, resource) tuple Periscope's apply endpoint
 * expects.
 *
 * For known built-in kinds (Pod / Deployment / ConfigMap / …), the
 * resource plural comes from KIND_REGISTRY. For unknown kinds — most
 * commonly Custom Resource Definitions whose plural varies —
 * fall back to the naive "lowercase + 's'" pluralization. The
 * apiserver will return a clear 404 on the apply call if the plural
 * is wrong, which surfaces in the per-doc result panel.
 *
 * apiVersion conventions:
 *   - "v1"          → group="" (core), version="v1"
 *   - "apps/v1"     → group="apps", version="v1"
 *   - "rbac.authorization.k8s.io/v1" → group="rbac.…", version="v1"
 *
 * Periscope's URL convention rewrites empty group to "core" — that
 * substitution happens server-side in cmd/periscope/main.go, so we
 * pass "" through and the existing api.applyResource handles it.
 */
export function gvrFromApiVersionAndKind(
  apiVersion: string,
  kind: string,
): GVR {
  const slash = apiVersion.indexOf("/");
  const group = slash === -1 ? "" : apiVersion.slice(0, slash);
  const version = slash === -1 ? apiVersion : apiVersion.slice(slash + 1);

  // Reverse lookup: kind ("Pod") → KindMeta entry. KIND_REGISTRY is
  // indexed by YamlKind (the URL-segment plural).
  for (const meta of Object.values(KIND_REGISTRY)) {
    if (meta.kind === kind) {
      return { group: meta.group, version: meta.version, resource: meta.resource };
    }
  }

  // Fallback: naive pluralization. Works for ~80% of CRD names that
  // follow the noun + 's' convention. For irregulars (NetworkPolicy →
  // networkpolicies, Endpoints → endpoints), the server will 404.
  return {
    group,
    version,
    resource: kind.toLowerCase() + (kind.endsWith("s") ? "" : "s"),
  };
}
