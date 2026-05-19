import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  buildRetainedOwnershipBody,
  buildRetainedOwnershipBodyFromOps,
  selectSelfOwnedPaths,
  ManagedFieldsUnavailableError,
} from "./applyBodyBuilder";
import {
  buildMinimalSSA,
  computeOps,
  parseOrThrow,
  type Identity,
  type Op,
} from "./yamlPatch";
import type { ManagedFieldsEntry } from "./api";

// ---------- shared fixtures ----------

const IDENTITY: Identity = {
  apiVersion: "networking.k8s.io/v1",
  kind: "Ingress",
  name: "demo",
  namespace: "default",
};

const PRISTINE_INGRESS = `apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: demo
  namespace: default
  annotations:
    alb.ingress.kubernetes.io/scheme: internet-facing
    alb.ingress.kubernetes.io/listen-ports: '[{"HTTP":80}]'
spec:
  ingressClassName: alb
  rules:
    - host: demo.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: api
                port:
                  number: 80
`;

const PORT_3000 = PRISTINE_INGRESS.replace("number: 80", "number: 3000");

/** managedFields entry where periscope-spa owns the ALB annotations. */
function periscopeOwnsAlbAnnotations(): ManagedFieldsEntry {
  return {
    manager: "periscope-spa",
    operation: "Apply",
    apiVersion: "networking.k8s.io/v1",
    fieldsType: "FieldsV1",
    fieldsV1: {
      "f:metadata": {
        "f:annotations": {
          "f:alb.ingress.kubernetes.io/scheme": {},
          "f:alb.ingress.kubernetes.io/listen-ports": {},
        },
      },
    },
  };
}

// ---------- selectSelfOwnedPaths — apiVersion-drift union ----------

describe("selectSelfOwnedPaths", () => {
  it("unions paths across multiple periscope-spa entries (apiVersion drift)", () => {
    const entries: ManagedFieldsEntry[] = [
      {
        manager: "periscope-spa",
        operation: "Apply",
        apiVersion: "apps/v1",
        fieldsType: "FieldsV1",
        fieldsV1: { "f:spec": { "f:replicas": {} } },
      },
      {
        manager: "periscope-spa",
        operation: "Apply",
        apiVersion: "apps/v1beta1",
        fieldsType: "FieldsV1",
        fieldsV1: {
          "f:spec": {
            "f:template": {
              "f:spec": {
                "f:containers": {
                  'k:{"name":"nginx"}': { "f:image": {} },
                },
              },
            },
          },
        },
      },
    ];
    const paths = selectSelfOwnedPaths(entries, "periscope-spa");
    expect(paths.sort()).toEqual(
      [
        "spec.replicas",
        "spec.template.spec.containers[name=nginx].image",
      ].sort(),
    );
  });

  it("ignores Update operation entries (CSA) — only Apply ownership is retained", () => {
    const entries: ManagedFieldsEntry[] = [
      {
        manager: "periscope-spa",
        operation: "Update",
        apiVersion: "apps/v1",
        fieldsType: "FieldsV1",
        fieldsV1: { "f:spec": { "f:replicas": {} } },
      },
    ];
    expect(selectSelfOwnedPaths(entries, "periscope-spa")).toEqual([]);
  });

  it("ignores entries from other managers", () => {
    const entries: ManagedFieldsEntry[] = [
      {
        manager: "kubectl-client-side-apply",
        operation: "Update",
        apiVersion: "v1",
        fieldsType: "FieldsV1",
        fieldsV1: { "f:metadata": { "f:annotations": { ".": {} } } },
      },
    ];
    expect(selectSelfOwnedPaths(entries, "periscope-spa")).toEqual([]);
  });
});

// ---------- buildRetainedOwnershipBody — core behaviour ----------

describe("buildRetainedOwnershipBody", () => {
  let warnSpy: ReturnType<typeof vi.spyOn>;
  beforeEach(() => {
    warnSpy = vi.spyOn(console, "warn").mockImplementation(() => {});
  });
  afterEach(() => {
    warnSpy.mockRestore();
  });

  it("first-ever apply (empty managedFields) matches legacy buildMinimalSSA byte-for-byte", () => {
    const baseline = PRISTINE_INGRESS;
    const draft = PORT_3000;
    const result = buildRetainedOwnershipBody({
      baseline,
      draft,
      current: baseline,
      identity: IDENTITY,
      managedFields: [],
    });
    expect(result.firstApply).toBe(true);
    expect(result.priorOwnedPaths).toEqual([]);
    expect(result.yaml).toBe(buildMinimalSSA(computeOps(baseline, draft), IDENTITY));
  });

  it("subsequent apply retains prior-owned annotations from current cluster state", () => {
    const result = buildRetainedOwnershipBody({
      baseline: PRISTINE_INGRESS,
      draft: PORT_3000,
      current: PRISTINE_INGRESS,
      identity: IDENTITY,
      managedFields: [periscopeOwnsAlbAnnotations()],
    });
    expect(result.firstApply).toBe(false);
    expect(result.priorOwnedPaths.sort()).toEqual(
      [
        "metadata.annotations.alb.ingress.kubernetes.io/scheme",
        "metadata.annotations.alb.ingress.kubernetes.io/listen-ports",
      ].sort(),
    );
    const parsed = parseOrThrow(result.yaml).obj as Record<string, unknown>;
    const annotations = (
      (parsed.metadata as Record<string, unknown>).annotations as Record<string, unknown>
    );
    expect(annotations["alb.ingress.kubernetes.io/scheme"]).toBe("internet-facing");
    expect(annotations["alb.ingress.kubernetes.io/listen-ports"]).toBe(
      '[{"HTTP":80}]',
    );
  });

  it("user edits a prior-owned path → edit wins (edit ops applied last)", () => {
    // periscope-spa already owns spec.replicas with cluster value 3;
    // user is editing it to 7 in draft.
    const baseline = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: demo
spec:
  replicas: 3
`;
    const draft = baseline.replace("replicas: 3", "replicas: 7");
    const current = baseline; // current cluster still has 3
    const mf: ManagedFieldsEntry = {
      manager: "periscope-spa",
      operation: "Apply",
      apiVersion: "apps/v1",
      fieldsType: "FieldsV1",
      fieldsV1: { "f:spec": { "f:replicas": {} } },
    };
    const result = buildRetainedOwnershipBody({
      baseline,
      draft,
      current,
      identity: {
        apiVersion: "apps/v1",
        kind: "Deployment",
        name: "demo",
      },
      managedFields: [mf],
    });
    const parsed = parseOrThrow(result.yaml).obj as Record<string, unknown>;
    expect((parsed.spec as Record<string, unknown>).replicas).toBe(7);
  });

  it("user 'remove' op on a prior-owned path omits the key from the payload (SSA retained-ownership deletion, hotfix v1.1.1)", () => {
    const baseline = `apiVersion: v1
kind: ConfigMap
metadata:
  name: demo
  annotations:
    keep-me: yes
    drop-me: 'true'
data:
  k: v
`;
    const draft = baseline.replace("    drop-me: 'true'\n", "");
    const current = baseline;
    const mf: ManagedFieldsEntry = {
      manager: "periscope-spa",
      operation: "Apply",
      apiVersion: "v1",
      fieldsType: "FieldsV1",
      fieldsV1: {
        "f:metadata": {
          "f:annotations": {
            "f:keep-me": {},
            "f:drop-me": {},
          },
        },
      },
    };
    const result = buildRetainedOwnershipBody({
      baseline,
      draft,
      current,
      identity: { apiVersion: "v1", kind: "ConfigMap", name: "demo" },
      managedFields: [mf],
    });
    const parsed = parseOrThrow(result.yaml).obj as Record<string, unknown>;
    const annotations = (
      (parsed.metadata as Record<string, unknown>).annotations as Record<string, unknown>
    );
    // keep-me retained from current; drop-me OMITTED from the apply
    // payload (not written as null). Under SSA per-key ownership,
    // periscope-spa previously owned f:drop-me, so dropping it from the
    // apply body relinquishes ownership → the apiserver removes the
    // annotation. Writing null (the v1.1.0 behavior) was wrong for
    // map[string]string fields: the apiserver coerced null → "" and
    // the key persisted with an empty value. See yamlPatch.ts:setLeaf
    // comment for the full story.
    expect(annotations["keep-me"]).toBe("yes");
    expect(Object.keys(annotations)).not.toContain("drop-me");
  });

  it("managedFields === null → throws ManagedFieldsUnavailableError", () => {
    expect(() =>
      buildRetainedOwnershipBody({
        baseline: PRISTINE_INGRESS,
        draft: PORT_3000,
        current: PRISTINE_INGRESS,
        identity: IDENTITY,
        managedFields: null,
      }),
    ).toThrow(ManagedFieldsUnavailableError);
  });

  it("managedFields === undefined → throws ManagedFieldsUnavailableError", () => {
    expect(() =>
      buildRetainedOwnershipBody({
        baseline: PRISTINE_INGRESS,
        draft: PORT_3000,
        current: PRISTINE_INGRESS,
        identity: IDENTITY,
        managedFields: undefined,
      }),
    ).toThrow(ManagedFieldsUnavailableError);
  });

  it("prior-owned path missing from current cluster state is silently dropped", () => {
    const baseline = `apiVersion: v1
kind: ConfigMap
metadata:
  name: demo
  annotations:
    only-one: still-here
data:
  k: v
`;
    const draft = baseline.replace("k: v", "k: v2");
    // periscope claims an annotation that no longer exists in current.
    const mf: ManagedFieldsEntry = {
      manager: "periscope-spa",
      operation: "Apply",
      apiVersion: "v1",
      fieldsType: "FieldsV1",
      fieldsV1: {
        "f:metadata": {
          "f:annotations": {
            "f:only-one": {},
            "f:was-deleted-externally": {},
          },
        },
      },
    };
    const result = buildRetainedOwnershipBody({
      baseline,
      draft,
      current: baseline,
      identity: { apiVersion: "v1", kind: "ConfigMap", name: "demo" },
      managedFields: [mf],
    });
    const parsed = parseOrThrow(result.yaml).obj as Record<string, unknown>;
    const annotations = (
      (parsed.metadata as Record<string, unknown>).annotations as Record<string, unknown>
    );
    // only-one retained; was-deleted-externally not present.
    expect(annotations["only-one"]).toBe("still-here");
    expect("was-deleted-externally" in annotations).toBe(false);
  });

  it("excludePriorOwned callback drops paths the operator chose to revert", () => {
    const result = buildRetainedOwnershipBody({
      baseline: PRISTINE_INGRESS,
      draft: PORT_3000,
      current: PRISTINE_INGRESS,
      identity: IDENTITY,
      managedFields: [periscopeOwnsAlbAnnotations()],
      excludePriorOwned: (p) =>
        p === "metadata.annotations.alb.ingress.kubernetes.io/scheme",
    });
    expect(result.priorOwnedPaths).toEqual([
      "metadata.annotations.alb.ingress.kubernetes.io/listen-ports",
    ]);
    const parsed = parseOrThrow(result.yaml).obj as Record<string, unknown>;
    const annotations = ((parsed.metadata as Record<string, unknown>)
      .annotations ?? {}) as Record<string, unknown>;
    expect("alb.ingress.kubernetes.io/scheme" in annotations).toBe(false);
    expect(annotations["alb.ingress.kubernetes.io/listen-ports"]).toBe(
      '[{"HTTP":80}]',
    );
  });

  it("retained path with IndexKey segment is dropped with a console.warn", () => {
    // i: marker on an atomic list.
    const mf: ManagedFieldsEntry = {
      manager: "periscope-spa",
      operation: "Apply",
      apiVersion: "v1",
      fieldsType: "FieldsV1",
      fieldsV1: {
        "f:spec": {
          "f:finalizers": {
            "i:0": { ".": {} },
          },
        },
      },
    };
    const baseline = `apiVersion: v1
kind: Foo
metadata:
  name: demo
spec:
  finalizers:
    - keep.me/forever
  field: a
`;
    const draft = baseline.replace("field: a", "field: b");
    buildRetainedOwnershipBody({
      baseline,
      draft,
      current: baseline,
      identity: { apiVersion: "v1", kind: "Foo", name: "demo" },
      managedFields: [mf],
    });
    expect(warnSpy).toHaveBeenCalledWith(
      expect.stringMatching(/IndexKey/),
    );
  });

  it("ops returned to caller is the user-edit set only (not the synthetic retain ops)", () => {
    const result = buildRetainedOwnershipBody({
      baseline: PRISTINE_INGRESS,
      draft: PORT_3000,
      current: PRISTINE_INGRESS,
      identity: IDENTITY,
      managedFields: [periscopeOwnsAlbAnnotations()],
    });
    // The user only edited the port; the retained annotations should
    // NOT show up here — parseConflictCauses relies on this.
    expect(result.ops.length).toBe(1);
    expect(result.ops[0].op).toBe("replace");
  });
});

// ---------- buildRetainedOwnershipBodyFromOps — mutation-hook entry ----------

describe("buildRetainedOwnershipBodyFromOps", () => {
  it("composes a body from caller-supplied ops + retained ownership", () => {
    const scaleOp: Op = {
      op: "replace",
      path: ["spec", "replicas"],
      value: 5,
    };
    const current = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: demo
  annotations:
    flux.io/managed: 'true'
spec:
  replicas: 3
`;
    const mf: ManagedFieldsEntry = {
      manager: "periscope-spa",
      operation: "Apply",
      apiVersion: "apps/v1",
      fieldsType: "FieldsV1",
      fieldsV1: {
        "f:metadata": { "f:annotations": { "f:flux.io/managed": {} } },
      },
    };
    const result = buildRetainedOwnershipBodyFromOps({
      ops: [scaleOp],
      current,
      identity: { apiVersion: "apps/v1", kind: "Deployment", name: "demo" },
      managedFields: [mf],
    });
    expect(result.firstApply).toBe(false);
    const parsed = parseOrThrow(result.yaml).obj as Record<string, unknown>;
    expect((parsed.spec as Record<string, unknown>).replicas).toBe(5);
    const annotations = (
      (parsed.metadata as Record<string, unknown>).annotations as Record<string, unknown>
    );
    expect(annotations["flux.io/managed"]).toBe("true");
  });

  it("first-apply case (empty managedFields) returns minimal SSA with just the ops", () => {
    const op: Op = { op: "replace", path: ["spec", "replicas"], value: 2 };
    const result = buildRetainedOwnershipBodyFromOps({
      ops: [op],
      current: "",
      identity: { apiVersion: "apps/v1", kind: "Deployment", name: "demo" },
      managedFields: [],
    });
    expect(result.firstApply).toBe(true);
    expect(result.priorOwnedPaths).toEqual([]);
  });

  it("throws on null managedFields (defensive race guard)", () => {
    expect(() =>
      buildRetainedOwnershipBodyFromOps({
        ops: [],
        current: "",
        identity: { apiVersion: "v1", kind: "Foo", name: "x" },
        managedFields: null,
      }),
    ).toThrow(ManagedFieldsUnavailableError);
  });
});
