// applyYamlParser tests — parser correctness + GVR mapping. Both are
// pure functions with no React / DOM dependencies, so they fit the
// existing vitest node environment cleanly.

import { describe, expect, it } from "vitest";
import {
  parseMultiDocYaml,
  gvrFromApiVersionAndKind,
} from "./applyYamlParser";

describe("parseMultiDocYaml", () => {
  it("returns no docs for empty input", () => {
    expect(parseMultiDocYaml("")).toEqual([]);
    expect(parseMultiDocYaml("   \n\n  ")).toEqual([]);
  });

  it("parses a single valid doc", () => {
    const input = `apiVersion: v1
kind: Pod
metadata:
  name: nginx
  namespace: default
spec:
  containers:
    - name: nginx
      image: nginx:1.27
`;
    const docs = parseMultiDocYaml(input);
    expect(docs).toHaveLength(1);
    expect(docs[0].valid).toBe(true);
    expect(docs[0].apiVersion).toBe("v1");
    expect(docs[0].kind).toBe("Pod");
    expect(docs[0].name).toBe("nginx");
    expect(docs[0].namespace).toBe("default");
    expect(docs[0].parseError).toBeUndefined();
  });

  it("parses multiple docs separated by ---", () => {
    const input = `apiVersion: v1
kind: ConfigMap
metadata:
  name: a
  namespace: prod
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: b
  namespace: prod
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: c
  namespace: prod
spec:
  replicas: 1
  selector:
    matchLabels:
      app: c
  template:
    metadata:
      labels:
        app: c
    spec:
      containers:
        - name: c
          image: nginx:1.27
`;
    const docs = parseMultiDocYaml(input);
    expect(docs).toHaveLength(3);
    expect(docs.map((d) => d.kind)).toEqual([
      "ConfigMap",
      "ConfigMap",
      "Deployment",
    ]);
    expect(docs.every((d) => d.valid)).toBe(true);
  });

  it("treats cluster-scoped resources (no namespace) as valid", () => {
    const input = `apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: my-cr
rules: []
`;
    const docs = parseMultiDocYaml(input);
    expect(docs).toHaveLength(1);
    expect(docs[0].valid).toBe(true);
    expect(docs[0].namespace).toBeUndefined();
  });

  it("flags missing required fields", () => {
    const input = `metadata:
  name: orphan
`;
    const docs = parseMultiDocYaml(input);
    expect(docs).toHaveLength(1);
    expect(docs[0].valid).toBe(false);
    expect(docs[0].parseError).toMatch(/missing required field/);
    expect(docs[0].parseError).toContain("apiVersion");
    expect(docs[0].parseError).toContain("kind");
  });

  it("isolates parse errors per doc — bad doc doesn't kill the rest", () => {
    const input = `apiVersion: v1
kind: ConfigMap
metadata:
  name: good-1
---
this: is: not: valid: yaml::
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: good-2
`;
    const docs = parseMultiDocYaml(input);
    expect(docs).toHaveLength(3);
    expect(docs[0].valid).toBe(true);
    expect(docs[0].name).toBe("good-1");
    expect(docs[1].valid).toBe(false);
    expect(docs[1].parseError).toBeTruthy();
    expect(docs[2].valid).toBe(true);
    expect(docs[2].name).toBe("good-2");
  });

  it("skips empty docs from leading / trailing separators", () => {
    // Leading and trailing `---` shouldn't produce empty ParsedDoc entries.
    const input = `---
apiVersion: v1
kind: ConfigMap
metadata:
  name: only
---
`;
    const docs = parseMultiDocYaml(input);
    expect(docs).toHaveLength(1);
    expect(docs[0].name).toBe("only");
  });

  it("preserves verbatim raw text per doc (slicing from source)", () => {
    const input = `apiVersion: v1
kind: ConfigMap
metadata:
  name: a
  # inline comment that must survive
data:
  key: value
`;
    const docs = parseMultiDocYaml(input);
    expect(docs[0].raw).toContain("inline comment that must survive");
  });

  it("generates stable IDs per doc", () => {
    const input = `apiVersion: v1
kind: ConfigMap
metadata:
  name: a
`;
    const ids1 = parseMultiDocYaml(input).map((d) => d.id);
    const ids2 = parseMultiDocYaml(input).map((d) => d.id);
    expect(ids1).toEqual(ids2);
    // Different content → different IDs
    const ids3 = parseMultiDocYaml(input + "data:\n  k: v\n").map((d) => d.id);
    expect(ids3[0]).not.toBe(ids1[0]);
  });
});

describe("gvrFromApiVersionAndKind", () => {
  it("maps known core/v1 kinds via KIND_REGISTRY", () => {
    expect(gvrFromApiVersionAndKind("v1", "Pod")).toEqual({
      group: "",
      version: "v1",
      resource: "pods",
    });
    expect(gvrFromApiVersionAndKind("v1", "ConfigMap")).toEqual({
      group: "",
      version: "v1",
      resource: "configmaps",
    });
  });

  it("maps known grouped kinds (apps/v1, rbac/v1) via KIND_REGISTRY", () => {
    expect(gvrFromApiVersionAndKind("apps/v1", "Deployment")).toEqual({
      group: "apps",
      version: "v1",
      resource: "deployments",
    });
    expect(
      gvrFromApiVersionAndKind("rbac.authorization.k8s.io/v1", "ClusterRole"),
    ).toEqual({
      group: "rbac.authorization.k8s.io",
      version: "v1",
      resource: "clusterroles",
    });
  });

  it("falls back to lowercase + s for unknown kinds (CRDs)", () => {
    expect(gvrFromApiVersionAndKind("cert-manager.io/v1", "Certificate")).toEqual({
      group: "cert-manager.io",
      version: "v1",
      resource: "certificates",
    });
    expect(
      gvrFromApiVersionAndKind("custom.example.com/v1", "MyResource"),
    ).toEqual({
      group: "custom.example.com",
      version: "v1",
      resource: "myresources",
    });
  });

  it("does not double-pluralize kinds already ending in s", () => {
    // "Endpoints" is the canonical kind name — already plural.
    expect(gvrFromApiVersionAndKind("v1", "Endpoints")).toEqual({
      group: "",
      version: "v1",
      // KIND_REGISTRY doesn't include Endpoints; falls through to
      // fallback. Since "Endpoints" ends in 's', we keep it.
      resource: "endpoints",
    });
  });

  it("preserves empty group for core (apiVersion='v1')", () => {
    // api.applyResource handles empty-group → 'core' URL substitution
    // server-side; the helper should pass "" through verbatim.
    const gvr = gvrFromApiVersionAndKind("v1", "ConfigMap");
    expect(gvr.group).toBe("");
  });
});
