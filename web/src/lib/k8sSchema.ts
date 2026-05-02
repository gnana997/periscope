// k8sSchema — pure helpers for navigating K8s OpenAPI v3 documents
// and producing monaco-yaml schema configurations.
//
// Public surface:
//   parseIdentityFromYaml(yaml)     — extract apiVersion/kind/name/ns
//   gvkFromIdentity(identity)       — split apiVersion into {group, version}
//   findSchemaForGVK(doc, gvk)      — locate the schema entry by
//                                     x-kubernetes-group-version-kind
//   buildMonacoSchemaConfig(doc, gvk, modelURI)
//                                   — wrap the matched schema for monaco-yaml
//
// All functions are pure. No React, no Monaco — these can be tested in
// isolation. Used by YamlEditor (PR4) to wire the OpenAPI proxy
// (PR1.5) into monaco-yaml's schema-aware features.

import { parseDocument } from "yaml";
import type { OpenAPIDoc, OpenAPISchema } from "./api";
import type { Identity } from "./yamlPatch";

export interface GVK {
  group: string; // "" for core
  version: string;
  kind: string;
}

/**
 * parseIdentityFromYaml extracts the minimum identity info needed to
 * route schema lookups + apply requests. Tolerant: returns null for
 * malformed YAML or missing required fields. Doesn't validate against
 * a schema (that's monaco-yaml's job once the schema is loaded).
 */
export function parseIdentityFromYaml(yaml: string): Identity | null {
  try {
    const doc = parseDocument(yaml);
    if (doc.errors.length > 0) return null;
    const obj = doc.toJS({ mapAsMap: false }) as Record<string, unknown> | null;
    if (!obj || typeof obj !== "object") return null;
    const apiVersion = obj.apiVersion;
    const kind = obj.kind;
    const meta = obj.metadata as Record<string, unknown> | undefined;
    if (typeof apiVersion !== "string" || typeof kind !== "string" || !meta) return null;
    const name = meta.name;
    if (typeof name !== "string") return null;
    const namespace = typeof meta.namespace === "string" ? meta.namespace : undefined;
    return { apiVersion, kind, name, namespace };
  } catch {
    return null;
  }
}

/**
 * gvkFromIdentity splits "apps/v1" → {group:"apps", version:"v1"} and
 * "v1" → {group:"", version:"v1"} (core API). Matches the URL routing
 * convention: backend treats group="" as the core API.
 */
export function gvkFromIdentity(identity: Identity): GVK {
  const parts = identity.apiVersion.split("/");
  if (parts.length === 1) {
    return { group: "", version: parts[0], kind: identity.kind };
  }
  return { group: parts[0], version: parts[1], kind: identity.kind };
}

/**
 * findSchemaForGVK scans the OpenAPI document's components.schemas
 * for an entry whose `x-kubernetes-group-version-kind` extension
 * matches the target GVK. Returns the schema name (e.g.
 * "io.k8s.api.apps.v1.Deployment") and the schema object — null if no
 * match (CRDs not in the bundled GV, malformed extensions, etc.).
 *
 * The match is on the first GVK in the extension array. K8s objects
 * sometimes carry multiple GVK aliases (deprecated versions); we
 * match any of them.
 */
export function findSchemaForGVK(
  doc: OpenAPIDoc,
  gvk: GVK,
): { schemaName: string; schema: OpenAPISchema } | null {
  const schemas = doc.components?.schemas;
  if (!schemas) return null;
  for (const [schemaName, schema] of Object.entries(schemas)) {
    const ext = schema["x-kubernetes-group-version-kind"];
    if (!Array.isArray(ext)) continue;
    for (const candidate of ext) {
      if (
        candidate.group === gvk.group &&
        candidate.version === gvk.version &&
        candidate.kind === gvk.kind
      ) {
        return { schemaName, schema };
      }
    }
  }
  return null;
}

/**
 * buildMonacoSchemaConfig wraps the matched schema in monaco-yaml's
 * SchemaConfiguration shape. The wrapper uses a `$ref` into the
 * schema's location and includes the full `components` object so
 * monaco-yaml's ajv can resolve refs at validation time without us
 * pre-resolving the entire schema graph (which is expensive on big
 * CRDs).
 *
 * `uri` is the schema's identifier (used internally by monaco-yaml's
 * cache); `fileMatch` is the model URI pattern that triggers
 * validation against this schema. Pass the editor's exact model URI
 * for a 1:1 binding, or a glob like "kubernetes://*\/apps/v1/*.yaml"
 * to match any Deployment.
 */
export function buildMonacoSchemaConfig(
  doc: OpenAPIDoc,
  gvk: GVK,
  modelURI: string,
): SchemaConfiguration | null {
  const match = findSchemaForGVK(doc, gvk);
  if (!match) return null;
  return {
    uri: `kubernetes-schema://${gvk.group || "core"}/${gvk.version}/${gvk.kind}`,
    fileMatch: [modelURI],
    schema: {
      $ref: `#/components/schemas/${match.schemaName}`,
      components: doc.components,
    },
  };
}

// SchemaConfiguration shape mirrors monaco-yaml's SchemaConfiguration.
// Re-declared here to avoid making the whole module depend on
// monaco-yaml types — k8sSchema.ts is pure data manipulation.
export interface SchemaConfiguration {
  uri: string;
  fileMatch: string[];
  schema?: unknown;
}

/**
 * modelURIForResource builds a unique Monaco model URI for a given
 * resource. Used by YamlEditor + YamlReadView; also used as the
 * `fileMatch` value when registering a schema so monaco-yaml routes
 * validation to this exact model.
 */
export function modelURIForResource(args: {
  cluster: string;
  group: string;
  version: string;
  kind: string;
  namespace?: string;
  name: string;
}): string {
  const ns = args.namespace ?? "_cluster";
  const groupSeg = args.group || "core";
  return `kubernetes://${args.cluster}/${groupSeg}/${args.version}/${args.kind}/${ns}/${args.name}.yaml`;
}
