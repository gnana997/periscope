import { describe, expect, it } from "vitest";
import {
  parseManagedFields,
  walkFieldsV1,
  type FieldOwner,
} from "./managedFields";
import type { ManagedFieldsEntry } from "./api";

// walkFieldsV1 — sub-tree → dotted path walker.
//
// Inputs are shaped like Kubernetes managedFields.fieldsV1: each key
// carries a one-character prefix that tells the walker how to project
// it onto the resource graph.
describe("walkFieldsV1", () => {
  function walk(node: Record<string, unknown>, prefix = ""): string[] {
    const out: string[] = [];
    walkFieldsV1(node, prefix, out);
    return out.sort();
  }

  it("emits an f: leaf as a dotted path", () => {
    expect(walk({ "f:spec": { "f:replicas": {} } })).toEqual(["spec.replicas"]);
  });

  it("emits '.': {} as 'this subtree is owned' at the current prefix", () => {
    // Nested ".": means periscope owns the whole metadata.labels map.
    const tree = {
      "f:metadata": {
        "f:labels": {
          ".": {},
        },
      },
    };
    expect(walk(tree)).toEqual(["metadata.labels"]);
  });

  it("ignores '.': {} at root prefix (no parent path to claim)", () => {
    // Defensive: walkFieldsV1 should not emit an empty-string path.
    expect(walk({ ".": {} })).toEqual([]);
  });

  it("emits k: merge-key segments with bracket notation, no dot", () => {
    const tree = {
      "f:spec": {
        "f:containers": {
          'k:{"name":"nginx"}': {
            "f:image": {},
          },
        },
      },
    };
    expect(walk(tree)).toEqual(["spec.containers[name=nginx].image"]);
  });

  it("emits i: integer-index segments with bracket notation", () => {
    const tree = {
      "f:spec": {
        "f:rules": {
          "i:0": { "f:host": {} },
        },
      },
    };
    expect(walk(tree)).toEqual(["spec.rules[0].host"]);
  });

  it("mixes f:, k:, i: at the same level cleanly", () => {
    const tree = {
      "f:spec": {
        "f:replicas": {},
        "f:containers": {
          'k:{"name":"nginx"}': { "f:image": {} },
        },
        "f:rules": {
          "i:0": { "f:host": {} },
        },
      },
    };
    expect(walk(tree)).toEqual([
      "spec.containers[name=nginx].image",
      "spec.replicas",
      "spec.rules[0].host",
    ]);
  });

  it("descends 4+ levels without dropping prefixes", () => {
    const tree = {
      "f:spec": {
        "f:template": {
          "f:spec": {
            "f:containers": {
              'k:{"name":"nginx"}': {
                "f:resources": {
                  "f:limits": {
                    "f:memory": {},
                  },
                },
              },
            },
          },
        },
      },
    };
    expect(walk(tree)).toEqual([
      "spec.template.spec.containers[name=nginx].resources.limits.memory",
    ]);
  });

  it("skips malformed k: values without throwing (entry + children dropped)", () => {
    // The "{...}" payload isn't valid JSON.
    const tree = {
      "f:spec": {
        "f:containers": {
          "k:{not-json}": {
            "f:image": {},
          },
          'k:{"name":"valid"}': {
            "f:image": {},
          },
        },
      },
    };
    // The malformed k: produces no segment → walker skips this key
    // entirely (children not visited). The valid sibling still emits.
    expect(() => walk(tree)).not.toThrow();
    expect(walk(tree)).toEqual(["spec.containers[name=valid].image"]);
  });

  it("skips unknown prefixes silently (forward-compat with new fieldsV1 markers)", () => {
    const tree = {
      "f:spec": {
        "f:replicas": {},
        // v: is a real K8s marker (set-of-atomic-values). We don't
        // emit anything for it — it's covered by the parent path.
        "f:finalizers": {
          'v:"foo.example.com/cleanup"': {},
        },
      },
    };
    // Only spec.replicas; "v:" not surfaced as a path of its own.
    expect(walk(tree)).toEqual(["spec.replicas"]);
  });

  it("tolerates non-object nodes (returns whatever could be parsed)", () => {
    // node: null, string, number → no-op.
    expect(walk(null as unknown as Record<string, unknown>)).toEqual([]);
    expect(walk("not an object" as unknown as Record<string, unknown>)).toEqual([]);
  });
});

// parseManagedFields — turns the array of managedFields entries on a
// K8s object into a flat list of FieldOwner records.
describe("parseManagedFields", () => {
  const NGINX_ENTRY: ManagedFieldsEntry = {
    manager: "kubectl-client-side-apply",
    operation: "Update",
    apiVersion: "apps/v1",
    fieldsType: "FieldsV1",
    fieldsV1: {
      "f:spec": {
        "f:replicas": {},
      },
    },
  };

  it("returns [] for null / undefined / empty input", () => {
    expect(parseManagedFields(null)).toEqual([]);
    expect(parseManagedFields(undefined)).toEqual([]);
    expect(parseManagedFields([])).toEqual([]);
  });

  it("emits one FieldOwner per leaf path with manager + operation populated", () => {
    const owners = parseManagedFields([NGINX_ENTRY]);
    expect(owners).toEqual<FieldOwner[]>([
      {
        path: "spec.replicas",
        manager: "kubectl-client-side-apply",
        operation: "Update",
      },
    ]);
  });

  it("skips entries lacking fieldsV1 / wrong fieldsType", () => {
    const owners = parseManagedFields([
      {
        manager: "stale-manager",
        operation: "Apply",
        apiVersion: "apps/v1",
        // no fieldsV1 / fieldsType
      },
      NGINX_ENTRY,
    ]);
    expect(owners).toHaveLength(1);
    expect(owners[0].manager).toBe("kubectl-client-side-apply");
  });
});
