import { describe, expect, it } from "vitest";
import {
  buildInstallRequest,
  compareAddonVersions,
  filterCompatibleVersions,
  generateAddonValuesYamlStub,
  parseSchemaSafe,
  pickDefaultVersion,
} from "./addonInstall";
import type { JSONSchema } from "./helmSchema";
import type { CatalogAddon } from "./types";

const vpcCNI: CatalogAddon = {
  name: "vpc-cni",
  compatibleVersions: [
    { version: "v1.18.5", kubernetesVersions: ["1.27", "1.28", "1.29", "1.30"], default: true },
    { version: "v1.17.0", kubernetesVersions: ["1.27", "1.28", "1.29"] },
    { version: "v1.16.4", kubernetesVersions: ["1.26", "1.27", "1.28"] },
  ],
};

describe("filterCompatibleVersions", () => {
  it("narrows to versions whose k8s list contains the cluster's version", () => {
    const out = filterCompatibleVersions(vpcCNI, "1.30");
    expect(out.map((v) => v.version)).toEqual(["v1.18.5"]);
  });

  it("filters more permissively for an older cluster", () => {
    const out = filterCompatibleVersions(vpcCNI, "1.28");
    expect(out.map((v) => v.version)).toEqual(["v1.18.5", "v1.17.0", "v1.16.4"]);
  });

  it("returns the full list when k8sVersion is empty (defensive)", () => {
    expect(filterCompatibleVersions(vpcCNI, undefined)).toHaveLength(3);
    expect(filterCompatibleVersions(vpcCNI, "")).toHaveLength(3);
  });
});

describe("pickDefaultVersion", () => {
  it("prefers the AWS-marked default", () => {
    expect(pickDefaultVersion(vpcCNI.compatibleVersions)).toBe("v1.18.5");
  });

  it("falls back to the first version when none is marked default", () => {
    const noDefault = vpcCNI.compatibleVersions.map((v) => ({ ...v, default: false }));
    expect(pickDefaultVersion(noDefault)).toBe("v1.18.5");
  });

  it("returns empty string when the list is empty", () => {
    expect(pickDefaultVersion([])).toBe("");
  });
});

describe("parseSchemaSafe", () => {
  it("returns the parsed object for valid JSON", () => {
    expect(parseSchemaSafe('{"type":"object"}')).toEqual({ type: "object" });
  });

  it("returns undefined for empty / whitespace input", () => {
    expect(parseSchemaSafe("")).toBeUndefined();
    expect(parseSchemaSafe(undefined)).toBeUndefined();
  });

  it("returns undefined for malformed JSON (defensive — falls back to YAML)", () => {
    expect(parseSchemaSafe("{not json")).toBeUndefined();
  });
});

describe("buildInstallRequest", () => {
  it("includes only required fields when optionals are empty", () => {
    const req = buildInstallRequest({
      addonName: "vpc-cni",
      addonVersion: "v1.18.5",
      configurationValuesYaml: "",
      serviceAccountRoleArn: "",
      resolveConflicts: "",
    });
    expect(req).toEqual({
      addonName: "vpc-cni",
      addonVersion: "v1.18.5",
    });
  });

  it("trims-then-checks empty strings — whitespace-only is treated as empty", () => {
    const req = buildInstallRequest({
      addonName: "vpc-cni",
      addonVersion: "v1.18.5",
      configurationValuesYaml: "   \n  ",
      serviceAccountRoleArn: "  ",
      resolveConflicts: "",
    });
    expect(req.configurationValues).toBeUndefined();
    expect(req.serviceAccountRoleArn).toBeUndefined();
  });

  it("forwards configurationValues verbatim (preserves YAML formatting)", () => {
    const yaml = "enableNetworkPolicy: true\n# comment\n";
    const req = buildInstallRequest({
      addonName: "vpc-cni",
      addonVersion: "v1.18.5",
      configurationValuesYaml: yaml,
      serviceAccountRoleArn: "",
      resolveConflicts: "OVERWRITE",
    });
    expect(req.configurationValues).toBe(yaml);
    expect(req.resolveConflicts).toBe("OVERWRITE");
  });

  it("includes serviceAccountRoleArn when set", () => {
    const req = buildInstallRequest({
      addonName: "vpc-cni",
      addonVersion: "v1.18.5",
      configurationValuesYaml: "",
      serviceAccountRoleArn: "arn:aws:iam::111:role/x",
      resolveConflicts: "NONE",
    });
    expect(req.serviceAccountRoleArn).toBe("arn:aws:iam::111:role/x");
    expect(req.resolveConflicts).toBe("NONE");
  });
});

describe("compareAddonVersions", () => {
  it("returns positive when a is newer than b on patch", () => {
    expect(
      compareAddonVersions("v1.12.2-eksbuild.2", "v1.12.2-eksbuild.1"),
    ).toBeGreaterThan(0);
  });

  it("returns negative when a is older than b on minor", () => {
    expect(
      compareAddonVersions("v1.12.1-eksbuild.4", "v1.12.2-eksbuild.2"),
    ).toBeLessThan(0);
  });

  it("compares numerically — v1.10.0 is newer than v1.7.1", () => {
    expect(
      compareAddonVersions("v1.10.0-eksbuild.1", "v1.7.1-eksbuild.2"),
    ).toBeGreaterThan(0);
  });

  it("returns 0 for identical versions", () => {
    expect(
      compareAddonVersions("v1.12.2-eksbuild.2", "v1.12.2-eksbuild.2"),
    ).toBe(0);
  });

  it("treats absent eksbuild suffix as 0 (older than any eksbuild)", () => {
    expect(
      compareAddonVersions("v1.12.2", "v1.12.2-eksbuild.1"),
    ).toBeLessThan(0);
  });

  it("returns 0 for unparseable versions (defensive — caller hides affordance)", () => {
    expect(compareAddonVersions("garbage", "also-garbage")).toBe(0);
  });
});

describe("generateAddonValuesYamlStub", () => {
  const schema: JSONSchema = {
    type: "object",
    properties: {
      enableMetrics: {
        type: "boolean",
        description: "Enable metrics collection for the controller pod",
        default: false,
      },
      logLevel: {
        type: "integer",
        description: "Set the level of verbosity of the logs",
        default: 2,
      },
      loggingFormat: {
        type: "string",
        description: "Log format for the driver container",
        enum: ["text", "json"],
      },
      controller: {
        type: "object",
        properties: {
          replicas: {
            type: "integer",
            description: "Number of controller replicas",
            default: 2,
          },
          env: {
            type: "array",
            description: "Extra env vars",
          },
        },
      },
    },
  };

  it("emits a header explaining the commented-stub convention", () => {
    const stub = generateAddonValuesYamlStub(schema);
    expect(stub).toMatch(/^# All fields below are commented for reference/);
    expect(stub).toContain("Empty config = AWS uses all defaults");
  });

  it("emits each field as a description comment + key:default line", () => {
    const stub = generateAddonValuesYamlStub(schema);
    expect(stub).toContain("# Enable metrics collection for the controller pod");
    expect(stub).toContain("# enableMetrics: false");
    expect(stub).toContain("# Set the level of verbosity of the logs");
    expect(stub).toContain("# logLevel: 2");
  });

  it("emits enum values as a hint comment", () => {
    const stub = generateAddonValuesYamlStub(schema);
    expect(stub).toMatch(/# loggingFormat: "" {2}# one of: "text", "json"/);
  });

  it("emits nested objects with indented children", () => {
    const stub = generateAddonValuesYamlStub(schema);
    // Parent and child both at the # convention; child indented one level
    expect(stub).toContain("# controller:");
    expect(stub).toMatch(/ {2}# Number of controller replicas/);
    expect(stub).toMatch(/ {2}# replicas: 2/);
  });

  it("emits unsupported leaves with the reason inline", () => {
    const stub = generateAddonValuesYamlStub(schema);
    // env is an array without items — descriptor type "unsupported"
    expect(stub).toMatch(/# env:.*array without items schema/);
  });

  it("returns empty string for an empty schema", () => {
    expect(generateAddonValuesYamlStub({ type: "object" })).toBe("");
  });
});
