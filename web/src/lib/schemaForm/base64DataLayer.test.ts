import { describe, expect, it } from "vitest";
import { decodeSecretYaml, encodeSecretYaml } from "./base64DataLayer";

describe("base64DataLayer — decode/encode round-trip", () => {
  it("decodes data[k] from base64 to plaintext", () => {
    const yaml = [
      "apiVersion: v1",
      "kind: Secret",
      "metadata:",
      "  name: app",
      "data:",
      "  password: aGVsbG8td29ybGQ=",
      "  token: cHJpdmF0ZQ==",
      "",
    ].join("\n");
    const decoded = decodeSecretYaml(yaml);
    expect(decoded).toMatch(/password: hello-world/);
    expect(decoded).toMatch(/token: private/);
  });

  it("re-encodes plaintext back to canonical base64", () => {
    const yaml = [
      "apiVersion: v1",
      "kind: Secret",
      "metadata:",
      "  name: app",
      "data:",
      "  password: hello-world",
      "",
    ].join("\n");
    const encoded = encodeSecretYaml(yaml);
    expect(encoded).toMatch(/password: aGVsbG8td29ybGQ=/);
  });

  it("survives a decode → encode round-trip", () => {
    const original = [
      "apiVersion: v1",
      "kind: Secret",
      "metadata:",
      "  name: app",
      "data:",
      "  password: aGVsbG8td29ybGQ=",
      "",
    ].join("\n");
    expect(encodeSecretYaml(decodeSecretYaml(original))).toContain(
      "password: aGVsbG8td29ybGQ=",
    );
  });

  it("preserves UTF-8 multi-byte plaintext through round-trip", () => {
    const plaintext = "naïve-tøken-π";
    const encoded = encodeSecretYaml(
      `apiVersion: v1\nkind: Secret\ndata:\n  k: ${plaintext}\n`,
    );
    const decoded = decodeSecretYaml(encoded);
    expect(decoded).toContain(`k: ${plaintext}`);
  });

  it("leaves stringData untouched (already plaintext)", () => {
    const yaml = [
      "apiVersion: v1",
      "kind: Secret",
      "metadata:",
      "  name: app",
      "stringData:",
      "  password: hello-world",
      "",
    ].join("\n");
    const decoded = decodeSecretYaml(yaml);
    expect(decoded).toMatch(/password: hello-world/);
  });

  it("passes through malformed YAML untouched", () => {
    const broken = ":\nnot: [valid";
    expect(decodeSecretYaml(broken)).toBe(broken);
    expect(encodeSecretYaml(broken)).toBe(broken);
  });

  it("is a no-op on a Secret with no data field", () => {
    const yaml = [
      "apiVersion: v1",
      "kind: Secret",
      "metadata:",
      "  name: app",
      "type: Opaque",
      "",
    ].join("\n");
    expect(decodeSecretYaml(yaml)).toContain("type: Opaque");
    expect(encodeSecretYaml(yaml)).toContain("type: Opaque");
  });
});
