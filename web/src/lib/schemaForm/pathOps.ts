// schemaForm/pathOps.ts — immutable path-based read/write helpers
// for the form's values tree. Pulled out of SchemaForm.tsx so the
// component file only exports React components (Vite fast-refresh
// requires that for HMR to work cleanly).

export function getAtPath(obj: unknown, path: string[]): unknown {
  let cur: unknown = obj;
  for (const seg of path) {
    if (cur === null || cur === undefined || typeof cur !== "object") return undefined;
    cur = (cur as Record<string, unknown>)[seg];
  }
  return cur;
}

export function setAtPath(
  obj: Record<string, unknown>,
  path: string[],
  value: unknown,
): Record<string, unknown> {
  if (path.length === 0) return obj;
  const [head, ...rest] = path;
  const next = { ...obj };
  if (rest.length === 0) {
    next[head] = value;
  } else {
    const child =
      obj[head] && typeof obj[head] === "object" && !Array.isArray(obj[head])
        ? (obj[head] as Record<string, unknown>)
        : {};
    next[head] = setAtPath(child, rest, value);
  }
  return next;
}
