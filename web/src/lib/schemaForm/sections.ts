// schemaForm/sections.ts — pure helpers that group section-stamped
// FieldDescriptors for the SchemaForm renderer. Lives in its own
// file so SchemaForm.tsx exports only React components (the
// react-refresh/only-export-components lint rule fires when a
// component module also exports plain functions).

import type { FieldDescriptor } from "./types";

export interface SectionedBuckets {
  primary: FieldDescriptor[];
  metadata: FieldDescriptor[];
  advanced: FieldDescriptor[];
  total: number;
}

// Walk the descriptor tree and collect every descriptor whose walker
// stamped a section. Descriptors land in the bucket directly, even if
// they are nested children of an unsectioned object container (e.g.
// `metadata.name` is collected under "metadata" while its parent
// `metadata` descriptor itself is unsectioned and gets dropped from
// the layout).
export function collectSectioned(
  descriptors: FieldDescriptor[],
): SectionedBuckets {
  const buckets: SectionedBuckets = {
    primary: [],
    metadata: [],
    advanced: [],
    total: 0,
  };
  const visit = (d: FieldDescriptor) => {
    if (d.section) {
      buckets[d.section].push(d);
      buckets.total += 1;
    }
    if (d.children) {
      for (const c of d.children) visit(c);
    }
  };
  for (const d of descriptors) visit(d);
  const byOrder = (a: FieldDescriptor, b: FieldDescriptor) =>
    (a.displayOrder ?? Number.MAX_SAFE_INTEGER) -
    (b.displayOrder ?? Number.MAX_SAFE_INTEGER);
  buckets.primary.sort(byOrder);
  buckets.metadata.sort(byOrder);
  buckets.advanced.sort(byOrder);
  return buckets;
}

export function descendantHasSection(d: FieldDescriptor): boolean {
  if (!d.children) return false;
  return d.children.some(
    (c) => c.section !== undefined || descendantHasSection(c),
  );
}
