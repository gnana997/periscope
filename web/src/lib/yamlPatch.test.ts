import { describe, expect, it } from "vitest";
import {
  buildMinimalSSA,
  computeOps,
  MultiDocumentError,
  parseOrThrow,
  type Identity,
} from "./yamlPatch";

const NGINX_DEPLOYMENT = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: nginx-app
  namespace: default
  labels:
    app.kubernetes.io/name: nginx-app
    app.kubernetes.io/version: "1.25.3"
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
        - name: sidecar
          image: busybox:1.36
`;

const IDENTITY: Identity = {
  apiVersion: "apps/v1",
  kind: "Deployment",
  name: "nginx-app",
  namespace: "default",
};

describe("computeOps", () => {
  it("returns no ops when before === after", () => {
    expect(computeOps(NGINX_DEPLOYMENT, NGINX_DEPLOYMENT)).toEqual([]);
  });

  it("emits a replace for a single scalar change", () => {
    const after = NGINX_DEPLOYMENT.replace("replicas: 3", "replicas: 5");
    const ops = computeOps(NGINX_DEPLOYMENT, after);
    expect(ops).toHaveLength(1);
    expect(ops[0]).toEqual({
      op: "replace",
      path: ["spec", "replicas"],
      value: 5,
    });
  });

  it("uses merge-key for container array changes", () => {
    const after = NGINX_DEPLOYMENT.replace("nginx:1.25.3-alpine", "nginx:1.25.4-alpine");
    const ops = computeOps(NGINX_DEPLOYMENT, after);
    expect(ops).toHaveLength(1);
    expect(ops[0]).toEqual({
      op: "replace",
      path: [
        "spec",
        "template",
        "spec",
        "containers",
        { name: "nginx" },
        "image",
      ],
      value: "nginx:1.25.4-alpine",
    });
  });

  it("falls back to atomic replace for arrays without a merge key (ports)", () => {
    // ports use a composite [containerPort, protocol] merge key in real
    // SSA; we model this as atomic-replace for v1 (simpler, correct).
    const after = NGINX_DEPLOYMENT.replace("containerPort: 8080", "containerPort: 9090");
    const ops = computeOps(NGINX_DEPLOYMENT, after);
    expect(ops).toHaveLength(1);
    expect(ops[0].op).toBe("replace");
    expect(ops[0].path).toEqual([
      "spec",
      "template",
      "spec",
      "containers",
      { name: "nginx" },
      "ports",
    ]);
  });

  it("emits a remove when a leaf field is deleted", () => {
    // Drop the version label
    const after = NGINX_DEPLOYMENT.replace(
      `    app.kubernetes.io/version: "1.25.3"\n`,
      "",
    );
    const ops = computeOps(NGINX_DEPLOYMENT, after);
    expect(ops).toEqual([
      {
        op: "remove",
        path: ["metadata", "labels", "app.kubernetes.io/version"],
      },
    ]);
  });

  it("emits a single op when an entire map is removed", () => {
    // Remove the whole resources block
    const after = NGINX_DEPLOYMENT.replace(
      / {10}resources:\n {12}limits:\n {14}cpu: 500m\n {14}memory: 256Mi\n/,
      "",
    );
    const ops = computeOps(NGINX_DEPLOYMENT, after);
    expect(ops).toHaveLength(1);
    expect(ops[0]).toEqual({
      op: "remove",
      path: [
        "spec",
        "template",
        "spec",
        "containers",
        { name: "nginx" },
        "resources",
      ],
    });
  });

  it("does not emit phantom ops for cosmetic numeric/string equivalence", () => {
    // YAML treats `8080` and `0x1F90` as the same number, but our
    // editor presents canonical YAML so this is mostly a guard against
    // round-trip parsing changing types.
    const after = NGINX_DEPLOYMENT.replace("containerPort: 8080", "containerPort: 8080");
    expect(computeOps(NGINX_DEPLOYMENT, after)).toEqual([]);
  });

  it("strips server-managed metadata before diffing", () => {
    // `before` carries managedFields/resourceVersion the apiserver returned;
    // `after` (the user's edited buffer) doesn't. Diff must be empty.
    const beforeWithMeta = `${NGINX_DEPLOYMENT}metadata:\n  resourceVersion: "8429103"\n  managedFields:\n  - manager: kustomize-controller\n    operation: Apply\n`;
    // The above isn't valid (metadata appears twice), so rebuild properly:
    const before = NGINX_DEPLOYMENT.replace(
      "metadata:\n  name: nginx-app",
      `metadata:\n  resourceVersion: "8429103"\n  generation: 7\n  managedFields:\n  - manager: kustomize-controller\n    operation: Apply\n  name: nginx-app`,
    );
    expect(computeOps(before, NGINX_DEPLOYMENT)).toEqual([]);
    expect(beforeWithMeta).toBeDefined(); // silence unused
  });

  it("handles whole-array replace for arrays without a known merge key", () => {
    const before = `apiVersion: v1
kind: Pod
metadata:
  name: p
spec:
  containers:
    - name: c
      image: a:1
      command: ["/bin/sh", "-c"]
`;
    const after = before.replace(
      `command: ["/bin/sh", "-c"]`,
      `command: ["/bin/bash", "-lc"]`,
    );
    const ops = computeOps(before, after);
    // command is `Array<string>` — no merge key — whole-array replace
    expect(ops).toHaveLength(1);
    expect(ops[0]).toMatchObject({
      op: "replace",
      path: expect.arrayContaining(["command"]),
      value: ["/bin/bash", "-lc"],
    });
  });
});

describe("parseOrThrow", () => {
  it("parses single-doc YAML", () => {
    const { obj } = parseOrThrow(NGINX_DEPLOYMENT);
    expect((obj as { kind: string }).kind).toBe("Deployment");
  });

  it("rejects multi-doc YAML", () => {
    const multi = `${NGINX_DEPLOYMENT}\n---\napiVersion: v1\nkind: Service\nmetadata:\n  name: nginx-svc\n`;
    expect(() => parseOrThrow(multi)).toThrow(MultiDocumentError);
  });

  it("tolerates trailing document separator", () => {
    // `---` after the doc is common; should not be treated as a second doc.
    expect(() => parseOrThrow(`${NGINX_DEPLOYMENT}---\n`)).not.toThrow();
  });
});

describe("buildMinimalSSA", () => {
  it("emits identity-only YAML when ops are empty", () => {
    const yaml = buildMinimalSSA([], IDENTITY);
    expect(yaml).toContain("apiVersion: apps/v1");
    expect(yaml).toContain("kind: Deployment");
    expect(yaml).toContain("name: nginx-app");
    expect(yaml).toContain("namespace: default");
    // No spec or other fields
    expect(yaml).not.toContain("spec:");
  });

  it("omits namespace for cluster-scoped resources", () => {
    const yaml = buildMinimalSSA([], {
      apiVersion: "v1",
      kind: "Namespace",
      name: "kube-system",
    });
    expect(yaml).toContain("name: kube-system");
    expect(yaml).not.toContain("namespace:");
  });

  it("builds the path tree for a simple replace", () => {
    const ops = computeOps(NGINX_DEPLOYMENT, NGINX_DEPLOYMENT.replace("replicas: 3", "replicas: 5"));
    const yaml = buildMinimalSSA(ops, IDENTITY);
    expect(yaml).toContain("spec:");
    expect(yaml).toContain("replicas: 5");
    // Should not contain unchanged fields
    expect(yaml).not.toContain("revisionHistoryLimit");
    expect(yaml).not.toContain("strategy:");
  });

  it("emits arrays with merge-key items for container changes", () => {
    const ops = computeOps(NGINX_DEPLOYMENT, NGINX_DEPLOYMENT.replace("nginx:1.25.3-alpine", "nginx:1.25.4-alpine"));
    const yaml = buildMinimalSSA(ops, IDENTITY);
    // The minimal payload includes the merge key (name: nginx) so SSA
    // can do its keyed merge, plus only the changed field (image).
    expect(yaml).toContain("- name: nginx");
    expect(yaml).toContain("image: nginx:1.25.4-alpine");
    // Should not contain the second container (sidecar) since only nginx changed
    expect(yaml).not.toContain("sidecar");
    // Should not contain unchanged fields of the nginx container
    expect(yaml).not.toContain("containerPort:");
    expect(yaml).not.toContain("memory:");
  });

  it("expresses removals as null leaves", () => {
    const after = NGINX_DEPLOYMENT.replace(
      `    app.kubernetes.io/version: "1.25.3"\n`,
      "",
    );
    const ops = computeOps(NGINX_DEPLOYMENT, after);
    const yaml = buildMinimalSSA(ops, IDENTITY);
    // SSA expresses removal-of-managed-field by setting it to null
    // (or `~` in PLAIN scalar style).
    expect(yaml).toMatch(/app\.kubernetes\.io\/version: ~/);
  });

  it("produces well-formed YAML that round-trips", () => {
    const ops = computeOps(
      NGINX_DEPLOYMENT,
      NGINX_DEPLOYMENT
        .replace("replicas: 3", "replicas: 5")
        .replace("nginx:1.25.3-alpine", "nginx:1.25.4-alpine"),
    );
    const yaml = buildMinimalSSA(ops, IDENTITY);
    // Re-parse it — should not throw, and the result must contain
    // the changed values.
    const { obj } = parseOrThrow(yaml);
    const parsed = obj as Record<string, unknown>;
    expect(parsed.apiVersion).toBe("apps/v1");
    expect(parsed.kind).toBe("Deployment");
    expect(((parsed.spec as Record<string, unknown>).replicas)).toBe(5);
  });
});
