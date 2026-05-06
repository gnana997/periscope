import { describe, expect, it } from "vitest";
import {
  buildInstallRequest,
  filterCompatibleVersions,
  parseSchemaSafe,
  pickDefaultVersion,
} from "./addonInstall";
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
