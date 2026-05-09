// Round-trip property tests per #116 acceptance:
//   "form → YAML → form for representative manifests of each kind
//    produces identical state."
//
// We pull representative manifests for ConfigMap / Secret /
// Service / Ingress, parse them, run them through the walker
// against a synthetic OpenAPI v3 doc that mirrors the real
// apiserver shape (the four root types tagged with
// x-kubernetes-group-version-kind), and verify that:
//
//   1. Parse → stringify → parse is idempotent (the SchemaFormBridge
//      round-trip preserves the values object structurally).
//   2. The walker emits descriptors that cover the manifest's
//      operator-facing fields (so the form actually renders them).
//   3. Editing a leaf via setAtPath updates the YAML on the round
//      trip without dropping sibling keys.

import { describe, expect, it } from "vitest";
import { parse as parseYaml, stringify as stringifyYaml } from "yaml";
import { buildFieldDescriptors } from "./walker";
import { buildRefResolver, findSchemaByGVK } from "./refResolver";
import {
  filterSchemaForKind,
  getCreateOnlyPaths,
  getKindGVK,
  type SupportedKind,
} from "./k8sAllowlist";
import { getAtPath, setAtPath } from "./pathOps";
import type { JSONSchema, FieldDescriptor } from "./types";
import type { OpenAPIDoc } from "../api";

// Synthetic doc covering all four kinds with the shapes the real
// K8s OpenAPI v3 exposes (just enough for the round-trip walker
// to render the operator-facing fields).
const synthDoc: OpenAPIDoc = {
  components: {
    schemas: {
      "io.k8s.apimachinery.pkg.apis.meta.v1.ObjectMeta": {
        type: "object",
        properties: {
          name: { type: "string" },
          namespace: { type: "string" },
          labels: { type: "object", additionalProperties: { type: "string" } },
          annotations: { type: "object", additionalProperties: { type: "string" } },
          uid: { type: "string" },
          creationTimestamp: { type: "string" },
          resourceVersion: { type: "string" },
          managedFields: { type: "array", items: { type: "object" } },
        },
      },
      "io.k8s.api.core.v1.ConfigMap": {
        type: "object",
        "x-kubernetes-group-version-kind": [
          { group: "", version: "v1", kind: "ConfigMap" },
        ],
        properties: {
          apiVersion: { type: "string" },
          kind: { type: "string" },
          metadata: { $ref: "#/components/schemas/io.k8s.apimachinery.pkg.apis.meta.v1.ObjectMeta" },
          data: { type: "object", additionalProperties: { type: "string" } },
          binaryData: { type: "object", additionalProperties: { type: "string" } },
          immutable: { type: "boolean" },
        },
      },
      "io.k8s.api.core.v1.Secret": {
        type: "object",
        "x-kubernetes-group-version-kind": [
          { group: "", version: "v1", kind: "Secret" },
        ],
        properties: {
          apiVersion: { type: "string" },
          kind: { type: "string" },
          metadata: { $ref: "#/components/schemas/io.k8s.apimachinery.pkg.apis.meta.v1.ObjectMeta" },
          type: { type: "string" },
          data: { type: "object", additionalProperties: { type: "string" } },
          stringData: { type: "object", additionalProperties: { type: "string" } },
          immutable: { type: "boolean" },
        },
      },
      "io.k8s.api.core.v1.ServicePort": {
        type: "object",
        properties: {
          name: { type: "string" },
          port: { type: "integer" },
          targetPort: { type: "integer" },
          protocol: { type: "string", enum: ["TCP", "UDP", "SCTP"] },
          nodePort: { type: "integer" },
        },
        required: ["port"],
      },
      "io.k8s.api.core.v1.ServiceSpec": {
        type: "object",
        properties: {
          type: { type: "string", enum: ["ClusterIP", "NodePort", "LoadBalancer", "ExternalName"] },
          selector: { type: "object", additionalProperties: { type: "string" } },
          ports: {
            type: "array",
            items: { $ref: "#/components/schemas/io.k8s.api.core.v1.ServicePort" },
          },
          clusterIP: { type: "string" },
          externalTrafficPolicy: { type: "string" },
          sessionAffinity: { type: "string" },
        },
      },
      "io.k8s.api.core.v1.Service": {
        type: "object",
        "x-kubernetes-group-version-kind": [
          { group: "", version: "v1", kind: "Service" },
        ],
        properties: {
          apiVersion: { type: "string" },
          kind: { type: "string" },
          metadata: { $ref: "#/components/schemas/io.k8s.apimachinery.pkg.apis.meta.v1.ObjectMeta" },
          spec: { $ref: "#/components/schemas/io.k8s.api.core.v1.ServiceSpec" },
          status: { type: "object" },
        },
      },
      "io.k8s.api.networking.v1.HTTPIngressPath": {
        type: "object",
        properties: {
          path: { type: "string" },
          pathType: { type: "string" },
        },
        required: ["path", "pathType"],
      },
      "io.k8s.api.networking.v1.IngressRule": {
        type: "object",
        properties: {
          host: { type: "string" },
          // Real schema nests http.paths; flattened here for the test.
          paths: {
            type: "array",
            items: { $ref: "#/components/schemas/io.k8s.api.networking.v1.HTTPIngressPath" },
          },
        },
      },
      "io.k8s.api.networking.v1.IngressTLS": {
        type: "object",
        properties: {
          hosts: { type: "array", items: { type: "string" } },
          secretName: { type: "string" },
        },
      },
      "io.k8s.api.networking.v1.IngressSpec": {
        type: "object",
        properties: {
          ingressClassName: { type: "string" },
          rules: {
            type: "array",
            items: { $ref: "#/components/schemas/io.k8s.api.networking.v1.IngressRule" },
          },
          tls: {
            type: "array",
            items: { $ref: "#/components/schemas/io.k8s.api.networking.v1.IngressTLS" },
          },
        },
      },
      "io.k8s.api.networking.v1.Ingress": {
        type: "object",
        "x-kubernetes-group-version-kind": [
          { group: "networking.k8s.io", version: "v1", kind: "Ingress" },
        ],
        properties: {
          apiVersion: { type: "string" },
          kind: { type: "string" },
          metadata: { $ref: "#/components/schemas/io.k8s.apimachinery.pkg.apis.meta.v1.ObjectMeta" },
          spec: { $ref: "#/components/schemas/io.k8s.api.networking.v1.IngressSpec" },
          status: { type: "object" },
        },
      },
    },
  },
} as unknown as OpenAPIDoc;

const walkOptionsFor = (kind: SupportedKind) => ({
  resolveRef: buildRefResolver(synthDoc),
  allowKvMap: true,
  allowArrayOfObjects: true,
  createOnlyPaths: getCreateOnlyPaths(kind),
});

const schemaFor = (kind: SupportedKind): JSONSchema => {
  const gvk = getKindGVK(kind);
  const root = findSchemaByGVK(synthDoc, gvk.group, gvk.version, gvk.kind);
  if (!root) throw new Error(`no schema for ${kind}`);
  return filterSchemaForKind(root, kind);
};

const flatten = (descriptors: FieldDescriptor[]): FieldDescriptor[] => {
  const out: FieldDescriptor[] = [];
  const visit = (ds: FieldDescriptor[]) => {
    for (const d of ds) {
      out.push(d);
      if (d.children) visit(d.children);
    }
  };
  visit(descriptors);
  return out;
};

const yamlRoundTrip = (yaml: string): unknown => {
  // Simulates SchemaFormBridge: parse, then re-stringify, then
  // re-parse. The intermediate stringify is the form-mode write.
  // schema: "yaml-1.1" matches what SchemaFormBridge passes — see
  // that file for the rationale (off/on/yes/no must round-trip as
  // strings, not get re-emitted unquoted and then parsed by the
  // K8s apiserver as booleans).
  const obj = parseYaml(yaml);
  const re = stringifyYaml(obj, { lineWidth: 0, schema: "yaml-1.1" });
  return parseYaml(re);
};

describe("ConfigMap — round-trip", () => {
  const yaml = [
    "apiVersion: v1",
    "kind: ConfigMap",
    "metadata:",
    "  name: app-config",
    "  namespace: default",
    "  labels:",
    "    team: platform",
    "data:",
    "  DATABASE_URL: postgres://db:5432/app",
    "  LOG_LEVEL: info",
    "immutable: false",
    "",
  ].join("\n");

  it("walker emits descriptors covering the operator-facing fields", () => {
    const all = flatten(buildFieldDescriptors(schemaFor("ConfigMap"), walkOptionsFor("ConfigMap")));
    const paths = all.map((d) => d.path.join("."));
    expect(paths).toEqual(expect.arrayContaining(["metadata", "metadata.name", "data", "immutable"]));
    const dataDesc = all.find((d) => d.path.join(".") === "data");
    expect(dataDesc?.type).toBe("kv-map");
    const nameDesc = all.find((d) => d.path.join(".") === "metadata.name");
    expect(nameDesc?.editable).toBe("create-only");
  });

  it("parse → stringify → parse is structurally idempotent", () => {
    expect(yamlRoundTrip(yaml)).toEqual(parseYaml(yaml));
  });

  it("editing data[k] via setAtPath preserves siblings", () => {
    const obj = parseYaml(yaml) as Record<string, unknown>;
    const next = setAtPath(obj, ["data", "DATABASE_URL"], "postgres://newhost/app");
    const data = getAtPath(next, ["data"]) as Record<string, string>;
    expect(data.DATABASE_URL).toBe("postgres://newhost/app");
    expect(data.LOG_LEVEL).toBe("info");
    expect(getAtPath(next, ["metadata", "name"])).toBe("app-config");
  });
});

describe("Secret — round-trip", () => {
  const yaml = [
    "apiVersion: v1",
    "kind: Secret",
    "metadata:",
    "  name: db-creds",
    "type: Opaque",
    "data:",
    "  password: aGVsbG8td29ybGQ=",
    "",
  ].join("\n");

  it("walker emits a kv-map descriptor for data", () => {
    const all = flatten(buildFieldDescriptors(schemaFor("Secret"), walkOptionsFor("Secret")));
    const dataDesc = all.find((d) => d.path.join(".") === "data");
    expect(dataDesc?.type).toBe("kv-map");
    expect(dataDesc?.kvValueType).toBe("string");
  });

  it("structure preserved through round-trip", () => {
    expect(yamlRoundTrip(yaml)).toEqual(parseYaml(yaml));
  });
});

describe("Service — round-trip", () => {
  const yaml = [
    "apiVersion: v1",
    "kind: Service",
    "metadata:",
    "  name: api",
    "  namespace: default",
    "spec:",
    "  type: ClusterIP",
    "  selector:",
    "    app: api",
    "  ports:",
    "    - name: http",
    "      port: 80",
    "      targetPort: 8080",
    "      protocol: TCP",
    "    - name: metrics",
    "      port: 9090",
    "      targetPort: 9090",
    "      protocol: TCP",
    "",
  ].join("\n");

  it("walker emits array-of-objects for spec.ports with relative-path children", () => {
    const all = flatten(buildFieldDescriptors(schemaFor("Service"), walkOptionsFor("Service")));
    const ports = all.find((d) => d.path.join(".") === "spec.ports");
    expect(ports?.type).toBe("array-of-objects");
    const portChildPaths = (ports?.children ?? []).map((c) => c.path.join("."));
    expect(portChildPaths).toEqual(expect.arrayContaining(["name", "port", "protocol"]));
  });

  it("walker emits select-style enum for spec.type", () => {
    const all = flatten(buildFieldDescriptors(schemaFor("Service"), walkOptionsFor("Service")));
    const typeDesc = all.find((d) => d.path.join(".") === "spec.type");
    expect(typeDesc?.type).toBe("string");
    expect(typeDesc?.enum).toEqual(
      expect.arrayContaining(["ClusterIP", "NodePort", "LoadBalancer"]),
    );
  });

  it("walker flags spec.clusterIP as create-only", () => {
    const all = flatten(buildFieldDescriptors(schemaFor("Service"), walkOptionsFor("Service")));
    const clusterIP = all.find((d) => d.path.join(".") === "spec.clusterIP");
    expect(clusterIP?.editable).toBe("create-only");
  });

  it("structure preserved through round-trip including nested ports[]", () => {
    const before = parseYaml(yaml) as Record<string, unknown>;
    const after = yamlRoundTrip(yaml) as Record<string, unknown>;
    expect(after).toEqual(before);
    const ports = getAtPath(after, ["spec", "ports"]) as unknown[];
    expect(ports).toHaveLength(2);
  });

  it("editing a port row via setAtPath preserves siblings", () => {
    const obj = parseYaml(yaml) as Record<string, unknown>;
    const ports = getAtPath(obj, ["spec", "ports"]) as Record<string, unknown>[];
    const updatedPorts = [...ports];
    updatedPorts[0] = { ...updatedPorts[0], port: 8081 };
    const next = setAtPath(obj, ["spec", "ports"], updatedPorts);
    expect((getAtPath(next, ["spec", "ports"]) as Record<string, unknown>[])[0].port).toBe(8081);
    expect((getAtPath(next, ["spec", "ports"]) as Record<string, unknown>[])[1].port).toBe(9090);
    expect(getAtPath(next, ["spec", "selector", "app"])).toBe("api");
  });
});

describe("Ingress — round-trip", () => {
  const yaml = [
    "apiVersion: networking.k8s.io/v1",
    "kind: Ingress",
    "metadata:",
    "  name: api",
    "  annotations:",
    "    nginx.ingress.kubernetes.io/rewrite-target: /",
    "spec:",
    "  ingressClassName: nginx",
    "  rules:",
    "    - host: api.example.com",
    "      paths:",
    "        - path: /",
    "          pathType: Prefix",
    "  tls:",
    "    - hosts:",
    "        - api.example.com",
    "      secretName: api-tls",
    "",
  ].join("\n");

  it("walker emits array-of-objects for rules[] and tls[]", () => {
    const all = flatten(buildFieldDescriptors(schemaFor("Ingress"), walkOptionsFor("Ingress")));
    const rules = all.find((d) => d.path.join(".") === "spec.rules");
    const tls = all.find((d) => d.path.join(".") === "spec.tls");
    expect(rules?.type).toBe("array-of-objects");
    expect(tls?.type).toBe("array-of-objects");
  });

  it("walker passes controller-specific annotations through as kv-map (no validation)", () => {
    const all = flatten(buildFieldDescriptors(schemaFor("Ingress"), walkOptionsFor("Ingress")));
    const annotations = all.find((d) => d.path.join(".") === "metadata.annotations");
    expect(annotations?.type).toBe("kv-map");
  });

  it("structure preserved through round-trip", () => {
    expect(yamlRoundTrip(yaml)).toEqual(parseYaml(yaml));
  });
});

describe("YAML 1.1 boolean-keyword strings — regression", () => {
  // Real bug observed in production (rc-3 follow-up): operator added a
  // ConfigMap.data key with value "off". The default yaml-package
  // schema emitted it unquoted (`fuck: off`); the K8s apiserver
  // parsed that as the boolean `false` and rejected the apply with
  // "expected string, got valueUnstructured{Value:false}". The fix
  // is `schema: "yaml-1.1"` on stringify, which forces quoting of
  // these magic keywords on emit.
  //
  // YAML 1.1 boolean-like strings: y / Y / yes / Yes / YES / n / N
  // / no / No / NO / true / True / TRUE / false / False / FALSE /
  // on / On / ON / off / Off / OFF.
  const SAMPLES = [
    "off",
    "on",
    "yes",
    "no",
    "true",
    "false",
    "Off",
    "ON",
    "Yes",
    "y",
    "n",
  ];

  for (const sample of SAMPLES) {
    it(`preserves ${JSON.stringify(sample)} as a string through round-trip`, () => {
      const yaml = `data:\n  KEY: ${JSON.stringify(sample)}\n`;
      const result = yamlRoundTrip(yaml);
      expect(result).toEqual({ data: { KEY: sample } });
    });
  }
});
