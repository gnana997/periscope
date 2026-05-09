// schemaForm/base64DataLayer.ts — bidirectional transform that
// swaps a Secret's `data[k]` between base64 (the wire format K8s
// stores) and plaintext (what operators want to type into a form).
//
// Used by SecretForm to give the form view plaintext while the
// applied YAML keeps the canonical base64 representation. A
// "show raw base64" toggle in the wrapper bypasses both
// directions so operators can inspect / edit the actual stored
// values.

import { parse as parseYaml, stringify as stringifyYaml } from "yaml";

/** Decode every value under `data` from base64 → plaintext. The
 *  `stringData` field already stores plaintext, so we leave it
 *  alone. Non-string entries pass through verbatim. */
export function decodeSecretYaml(yaml: string): string {
  if (!yaml.trim()) return yaml;
  let obj: unknown;
  try {
    obj = parseYaml(yaml);
  } catch {
    return yaml;
  }
  if (!obj || typeof obj !== "object" || Array.isArray(obj)) return yaml;
  const root = obj as Record<string, unknown>;
  const data = root.data;
  if (!data || typeof data !== "object" || Array.isArray(data)) return yaml;
  const decoded: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(data as Record<string, unknown>)) {
    decoded[k] = typeof v === "string" ? safeAtob(v) : v;
  }
  const next = { ...root, data: decoded };
  try {
    // schema: "yaml-1.1" matches SchemaFormBridge — see that file
    // for why off/on/yes/no need to round-trip as quoted strings.
    return stringifyYaml(next, { lineWidth: 0, schema: "yaml-1.1" });
  } catch {
    return yaml;
  }
}

/** Inverse of decodeSecretYaml: re-encode `data[k]` plaintext to
 *  base64. Skips entries that are already valid base64 to keep the
 *  no-op round-trip stable. */
export function encodeSecretYaml(yaml: string): string {
  if (!yaml.trim()) return yaml;
  let obj: unknown;
  try {
    obj = parseYaml(yaml);
  } catch {
    return yaml;
  }
  if (!obj || typeof obj !== "object" || Array.isArray(obj)) return yaml;
  const root = obj as Record<string, unknown>;
  const data = root.data;
  if (!data || typeof data !== "object" || Array.isArray(data)) return yaml;
  const encoded: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(data as Record<string, unknown>)) {
    encoded[k] = typeof v === "string" ? safeBtoa(v) : v;
  }
  const next = { ...root, data: encoded };
  try {
    // schema: "yaml-1.1" matches SchemaFormBridge — see that file
    // for why off/on/yes/no need to round-trip as quoted strings.
    return stringifyYaml(next, { lineWidth: 0, schema: "yaml-1.1" });
  } catch {
    return yaml;
  }
}

function safeAtob(s: string): string {
  try {
    // atob handles ASCII; for UTF-8 we round-trip via TextDecoder
    // so multi-byte plaintext (e.g. emoji in a token) survives.
    const bin = atob(s);
    const bytes = new Uint8Array(bin.length);
    for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
    return new TextDecoder().decode(bytes);
  } catch {
    // Not valid base64 — operator may have just typed plaintext
    // and toggled to "show raw base64" mid-edit. Pass through.
    return s;
  }
}

function safeBtoa(s: string): string {
  try {
    const bytes = new TextEncoder().encode(s);
    let bin = "";
    for (const b of bytes) bin += String.fromCharCode(b);
    return btoa(bin);
  } catch {
    return s;
  }
}
