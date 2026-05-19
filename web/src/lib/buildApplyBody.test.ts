// buildApplyBody — the post-#224 entry point for SSA apply bodies.
// Architecturally critical surface: every call site (YAML editor,
// form mode, mutation hooks) routes through here. These tests lock
// the invariants that the refactor exists to enforce:
//
//   1. The payload is exactly the operator's diff — nothing more.
//   2. No managedFields data appears in the payload (the v1.1.x
//      retained-ownership behavior is gone).
//   3. Identity (apiVersion / kind / metadata.{name, namespace}) is
//      always present.
//   4. Malformed input throws a recognizable error the caller can
//      surface to the user.

import { describe, expect, it } from "vitest";
import { buildApplyBody } from "./applyBodyBuilder";
import { MultiDocumentError, parseOrThrow, type Identity } from "./yamlPatch";

const NGINX = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: nginx-app
  namespace: default
  labels:
    app.kubernetes.io/name: nginx-app
    app.kubernetes.io/version: "1.25.3"
  annotations:
    alb.ingress.kubernetes.io/scheme: internet-facing
    helm.sh/chart: nginx-1.0.0
spec:
  replicas: 3
  template:
    spec:
      containers:
        - name: nginx
          image: nginx:1.25.3-alpine
          ports:
            - name: http
              containerPort: 8080
              protocol: TCP
          resources:
            limits:
              cpu: 500m
              memory: 256Mi
`;

const IDENTITY: Identity = {
  apiVersion: "apps/v1",
  kind: "Deployment",
  name: "nginx-app",
  namespace: "default",
};

describe("buildApplyBody", () => {
  describe("no-op", () => {
    it("baseline === draft → ops is empty; yaml still carries identity", () => {
      const { yaml, ops } = buildApplyBody(NGINX, NGINX, IDENTITY);
      expect(ops).toEqual([]);
      // Empty ops still produce a valid identity-only payload — the
      // caller is responsible for short-circuiting on ops.length === 0
      // if they don't want to submit a no-op apply.
      const parsed = parseOrThrow(yaml).obj as Record<string, unknown>;
      expect(parsed.apiVersion).toBe("apps/v1");
      expect(parsed.kind).toBe("Deployment");
      expect((parsed.metadata as Record<string, unknown>).name).toBe("nginx-app");
      expect((parsed.metadata as Record<string, unknown>).namespace).toBe("default");
    });
  });

  describe("minimal-diff (architectural invariant)", () => {
    it("scalar change → payload contains only identity + the changed field", () => {
      const draft = NGINX.replace("replicas: 3", "replicas: 5");
      const { yaml, ops } = buildApplyBody(NGINX, draft, IDENTITY);

      expect(ops).toHaveLength(1);
      expect(ops[0]).toEqual({
        op: "replace",
        path: ["spec", "replicas"],
        value: 5,
      });

      // The payload must contain the new value...
      expect(yaml).toContain("replicas: 5");

      // ...but NOT contain any of the unchanged fields the baseline
      // had. This is the load-bearing assertion: retained-ownership
      // would have re-included these because periscope-spa might
      // have owned them. Minimal-diff sends only what changed.
      expect(yaml).not.toContain("app.kubernetes.io/version");
      expect(yaml).not.toContain("alb.ingress.kubernetes.io/scheme");
      expect(yaml).not.toContain("helm.sh/chart");
      expect(yaml).not.toContain("containerPort");
      expect(yaml).not.toContain("nginx:1.25.3-alpine");
      expect(yaml).not.toContain("memory:");
      expect(yaml).not.toContain("500m");
    });

    it("multiple non-overlapping changes → payload contains all of them, nothing else", () => {
      const draft = NGINX
        .replace("replicas: 3", "replicas: 5")
        .replace("nginx:1.25.3-alpine", "nginx:1.25.4-alpine");
      const { yaml, ops } = buildApplyBody(NGINX, draft, IDENTITY);

      expect(ops).toHaveLength(2);
      expect(yaml).toContain("replicas: 5");
      expect(yaml).toContain("nginx:1.25.4-alpine");

      // Unchanged sibling fields still absent.
      expect(yaml).not.toContain("alb.ingress.kubernetes.io/scheme");
      expect(yaml).not.toContain("memory:");
      expect(yaml).not.toContain("containerPort");
    });
  });

  describe("managedFields invariant", () => {
    it("payload never contains a managedFields key (v1.1.x retained-ownership is gone)", () => {
      // Even if the baseline somehow includes a managedFields block
      // (e.g. the operator pasted a kubectl get -oyaml output that
      // includes it), the apply pipeline must strip it. parseOrThrow
      // does not retain managedFields in the parsed shape used by
      // computeOps/buildMinimalSSA, so this falls out automatically;
      // we lock it as a regression test.
      const baselineWithMF = NGINX.replace(
        "metadata:",
        `metadata:
  managedFields:
    - manager: periscope-spa
      operation: Apply
      fieldsV1:
        f:spec:
          f:replicas: {}
      apiVersion: apps/v1`,
      );
      const draft = baselineWithMF.replace("replicas: 3", "replicas: 5");
      const { yaml } = buildApplyBody(baselineWithMF, draft, IDENTITY);

      expect(yaml).not.toContain("managedFields");
      expect(yaml).not.toContain("fieldsV1");
      expect(yaml).not.toContain("periscope-spa");
    });
  });

  describe("identity preservation", () => {
    it("namespaced identity round-trips apiVersion / kind / name / namespace", () => {
      const draft = NGINX.replace("replicas: 3", "replicas: 7");
      const { yaml } = buildApplyBody(NGINX, draft, IDENTITY);
      const parsed = parseOrThrow(yaml).obj as Record<string, unknown>;
      expect(parsed.apiVersion).toBe("apps/v1");
      expect(parsed.kind).toBe("Deployment");
      const meta = parsed.metadata as Record<string, unknown>;
      expect(meta.name).toBe("nginx-app");
      expect(meta.namespace).toBe("default");
    });

    it("cluster-scoped identity (no namespace) round-trips without producing a metadata.namespace key", () => {
      const baseline = `apiVersion: v1
kind: Namespace
metadata:
  name: tenant-a
  labels:
    team: platform
`;
      const draft = baseline.replace("team: platform", "team: payments");
      const id: Identity = {
        apiVersion: "v1",
        kind: "Namespace",
        name: "tenant-a",
      };
      const { yaml } = buildApplyBody(baseline, draft, id);
      const parsed = parseOrThrow(yaml).obj as Record<string, unknown>;
      const meta = parsed.metadata as Record<string, unknown>;
      expect(meta.name).toBe("tenant-a");
      // namespace key must not be invented for a cluster-scoped resource.
      expect(meta).not.toHaveProperty("namespace");
    });
  });

  describe("malformed input", () => {
    it("multi-document YAML in draft → throws MultiDocumentError", () => {
      const multiDocDraft = `${NGINX}---
apiVersion: v1
kind: ConfigMap
metadata:
  name: stowaway
`;
      expect(() => buildApplyBody(NGINX, multiDocDraft, IDENTITY)).toThrow(
        MultiDocumentError,
      );
    });

    it("unparseable YAML in draft → throws (YamlParseError surface)", () => {
      // Unterminated double-quoted string. The exact error class is
      // parseOrThrow's — we just assert *some* throw so the caller
      // has a chance to surface it to the user.
      const garbage = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: nginx-app
  annotations:
    broken: "unterminated string
spec:
  replicas: 5
`;
      expect(() => buildApplyBody(NGINX, garbage, IDENTITY)).toThrow();
    });
  });
});
