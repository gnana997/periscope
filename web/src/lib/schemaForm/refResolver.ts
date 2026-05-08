// schemaForm/refResolver.ts — resolves OpenAPI v3 `$ref` strings
// against the `components.schemas` bundle of a fetched apiserver
// OpenAPI doc, and locates the root schema for a (group, version,
// kind) tuple so the form can render the right type.
//
// K8s schemas are almost entirely $ref-based — Service →
// `#/components/schemas/io.k8s.api.core.v1.Service` → ServiceSpec →
// ServicePort → IntOrString. Without ref resolution the walker
// can't render anything but the empty top-level object.

import type { OpenAPIDoc, OpenAPISchema } from "../api";
import type { JSONSchema } from "./types";

/** Build a resolver function the walker uses to dereference `$ref`
 *  strings in-line. Cycle-safe; the walker itself maintains a
 *  per-branch `seen` set. */
export function buildRefResolver(doc: OpenAPIDoc): (ref: string) => JSONSchema | undefined {
  const schemas = doc.components?.schemas ?? {};
  return (ref: string) => {
    // Only honor in-document component refs. Anything else (full
    // URLs, file refs) is rejected — the walker surfaces them as
    // "edit in YAML mode".
    const prefix = "#/components/schemas/";
    if (!ref.startsWith(prefix)) return undefined;
    const name = ref.slice(prefix.length);
    const s = schemas[name];
    return s ? (s as JSONSchema) : undefined;
  };
}

/** Locate the schema that describes the given (group, version,
 *  kind) inside the doc's components.schemas. K8s schemas tag the
 *  root types with `x-kubernetes-group-version-kind`. */
export function findSchemaByGVK(
  doc: OpenAPIDoc,
  group: string,
  version: string,
  kind: string,
): JSONSchema | undefined {
  const schemas = doc.components?.schemas ?? {};
  for (const name of Object.keys(schemas)) {
    const s = schemas[name] as OpenAPISchema | undefined;
    const gvks = s?.["x-kubernetes-group-version-kind"];
    if (!gvks) continue;
    for (const g of gvks) {
      if (g.group === group && g.version === version && g.kind === kind) {
        return s as JSONSchema;
      }
    }
  }
  return undefined;
}
