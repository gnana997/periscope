// labels — validation for Kubernetes label keys and values.
//
// Mirrors the apiserver's enforcement so the EditLabelsModal can red-line
// invalid rows inline rather than waiting for a 422 round-trip. Rules
// from https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/.
//
// Key
//   Optional `prefix/`, where prefix is a DNS subdomain (≤253 chars).
//   Name segment: ≤63 chars, alphanumeric edges, interior may include
//   `-`, `_`, `.`. Required.
//
// Value
//   ≤63 chars, same character class as the name segment, OR empty.

const SEGMENT = /^[a-z0-9A-Z]([-a-z0-9A-Z_.]*[a-z0-9A-Z])?$/;

// DNS subdomain (RFC 1123): lowercase labels separated by dots, each
// label ≤63 chars and alphanumeric edges. Used for the optional key
// prefix and validated separately so we can give a specific error.
const DNS_SUBDOMAIN_LABEL = /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/;

export function validateLabelKey(key: string): string | null {
  if (key.length === 0) return "key is required";
  const slash = key.indexOf("/");
  let prefix: string | null = null;
  let name: string;
  if (slash >= 0) {
    prefix = key.slice(0, slash);
    name = key.slice(slash + 1);
  } else {
    name = key;
  }
  if (prefix !== null) {
    if (prefix.length === 0) return "prefix before '/' is empty";
    if (prefix.length > 253) return "prefix must be ≤253 chars";
    const labels = prefix.split(".");
    if (labels.some((l) => !DNS_SUBDOMAIN_LABEL.test(l))) {
      return "prefix must be a DNS subdomain (lowercase, alphanumeric, dot-separated)";
    }
  }
  if (name.length === 0) return "name segment after '/' is empty";
  if (name.length > 63) return "name segment must be ≤63 chars";
  if (!SEGMENT.test(name)) {
    return "name must start/end with alphanumeric; interior may use - _ .";
  }
  return null;
}

export function validateLabelValue(value: string): string | null {
  if (value.length === 0) return null; // empty value is allowed
  if (value.length > 63) return "value must be ≤63 chars";
  if (!SEGMENT.test(value)) {
    return "value must start/end with alphanumeric; interior may use - _ .";
  }
  return null;
}

export interface LabelRow {
  key: string;
  value: string;
}

// findDuplicateKeys returns the set of keys that appear more than once.
// Empty keys are ignored — they're already flagged by validateLabelKey.
export function findDuplicateKeys(rows: LabelRow[]): Set<string> {
  const counts = new Map<string, number>();
  for (const r of rows) {
    if (r.key.length === 0) continue;
    counts.set(r.key, (counts.get(r.key) ?? 0) + 1);
  }
  const dups = new Set<string>();
  for (const [k, n] of counts) if (n > 1) dups.add(k);
  return dups;
}
