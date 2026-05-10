// schemaForm/sections.ts — pure helpers that group section-stamped
// FieldDescriptors for the SchemaForm renderer. Lives in its own
// file so SchemaForm.tsx exports only React components (the
// react-refresh/only-export-components lint rule fires when a
// component module also exports plain functions).

import type { FieldDescriptor, FieldSection } from "./types";

/** Map<section id → descriptors> + an aggregate total. The renderer
 *  iterates the kind's declared section list (from k8sAllowlist) in
 *  order and pulls each section's bucket from this map. Sections
 *  that don't appear in the map at all simply render empty. */
export interface SectionedBuckets {
  byId: Map<FieldSection, FieldDescriptor[]>;
  total: number;
}

/** Walk the descriptor tree and collect every descriptor whose walker
 *  stamped a section. Descriptors land in their bucket directly,
 *  even if they are nested children of an unsectioned object
 *  container (e.g. `metadata.name` is collected under `metadata`
 *  while its parent `metadata` descriptor itself is unsectioned and
 *  gets dropped from the layout).
 *
 *  STOPS at array-of-objects boundaries: per-row children of an
 *  array-of-objects descriptor are L2 — the array's renderer groups
 *  them inside each row, not the form-level L1 layout.
 *  `discriminator` descriptors stop for the same reason — their
 *  branch children belong inside the picker, not the L1 layout. */
export function collectSectioned(
  descriptors: FieldDescriptor[],
): SectionedBuckets {
  const byId = new Map<FieldSection, FieldDescriptor[]>();
  let total = 0;
  const push = (id: FieldSection, d: FieldDescriptor) => {
    let arr = byId.get(id);
    if (!arr) {
      arr = [];
      byId.set(id, arr);
    }
    arr.push(d);
    total += 1;
  };
  const visit = (d: FieldDescriptor) => {
    if (d.section) {
      push(d.section, d);
    }
    // L1 collection stops at array-of-objects + discriminator
    // boundaries; their inner descriptors are L2 / branch-local.
    if (d.children && d.type !== "array-of-objects" && d.type !== "discriminator") {
      for (const c of d.children) visit(c);
    }
  };
  for (const d of descriptors) visit(d);
  const byOrder = (a: FieldDescriptor, b: FieldDescriptor) =>
    (a.displayOrder ?? Number.MAX_SAFE_INTEGER) -
    (b.displayOrder ?? Number.MAX_SAFE_INTEGER);
  for (const arr of byId.values()) arr.sort(byOrder);
  return { byId, total };
}

/** L2 grouping for array-of-objects ROW children. Same shape as
 *  collectSectioned but operates on a flat row-children list (which
 *  is what the array-of-objects descriptor exposes). Doesn't recurse
 *  past array-of-objects — nested arrays inside a row would need
 *  their own L3 sub-section spec, which we don't support yet. */
export function collectRowSubSections(
  rowChildren: FieldDescriptor[],
): SectionedBuckets {
  const byId = new Map<FieldSection, FieldDescriptor[]>();
  let total = 0;
  for (const d of rowChildren) {
    if (d.section) {
      let arr = byId.get(d.section);
      if (!arr) {
        arr = [];
        byId.set(d.section, arr);
      }
      arr.push(d);
      total += 1;
    }
  }
  const byOrder = (a: FieldDescriptor, b: FieldDescriptor) =>
    (a.displayOrder ?? Number.MAX_SAFE_INTEGER) -
    (b.displayOrder ?? Number.MAX_SAFE_INTEGER);
  for (const arr of byId.values()) arr.sort(byOrder);
  return { byId, total };
}

export function descendantHasSection(d: FieldDescriptor): boolean {
  if (!d.children) return false;
  // Don't peek into array-of-objects rows — their stamps belong to
  // L2, not the L1 grouping.
  if (d.type === "array-of-objects" || d.type === "discriminator") return false;
  return d.children.some(
    (c) => c.section !== undefined || descendantHasSection(c),
  );
}

/** Return true if the descriptor's value is "populated" — used by the
 *  openWhenPopulated section flag. Conservative definition: an array
 *  with ≥1 element, an object with ≥1 own key, a non-empty string, or
 *  a non-undefined boolean / number. */
export function descriptorHasContent(
  descriptor: FieldDescriptor,
  values: Record<string, unknown>,
): boolean {
  let cursor: unknown = values;
  for (const seg of descriptor.path) {
    if (cursor == null || typeof cursor !== "object") return false;
    cursor = (cursor as Record<string, unknown>)[seg];
  }
  if (cursor == null) return false;
  if (Array.isArray(cursor)) return cursor.length > 0;
  if (typeof cursor === "object") return Object.keys(cursor).length > 0;
  if (typeof cursor === "string") return cursor.length > 0;
  return true;
}
