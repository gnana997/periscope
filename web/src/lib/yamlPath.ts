// yamlPath — given a Monaco model + line number, compute the dotted
// YAML path of that line. Used to map editor lines to K8s SSA
// managedFields paths for the owner-glyph margin.
//
// Algorithm: walk UPWARD from the target line, tracking decreasing
// indentation. Each level-up surfaces a parent key. For list items
// (lines starting with `- `), inspect the merge key (name / key /
// containerPort / mountPath) on the same item to produce
// `[name=foo]` style path segments matching the K8s SSA convention.
//
// Tolerant: ignores blank lines and `#` comments. Returns "" for
// lines that don't have a meaningful path (root, empty, etc.).
//
// Lifted (with cleanup) from the v2 mock — the algorithm is identical
// to the one we use for breadcrumbs and conflict-row jump-to-line.

import type * as monaco from "monaco-editor";

const MERGE_KEY_FIELDS = ["name", "key", "containerPort", "mountPath", "ip", "topologyKey", "type"];

/**
 * pathForLine returns the dotted YAML path of `lineNum` in `model`.
 * Returns the empty string if the line has no path (blank / top-level).
 */
export function pathForLine(model: monaco.editor.ITextModel, lineNum: number): string {
  const total = model.getLineCount();
  const target = Math.min(Math.max(lineNum, 1), total);
  const parts: string[] = [];
  let curIndent = -1;

  for (let i = target; i >= 1; i--) {
    const raw = model.getLineContent(i);
    const trimmed = raw.trim();
    if (!trimmed || trimmed.startsWith("#")) continue;
    const indent = raw.length - raw.trimStart().length;
    if (curIndent === -1 || indent < curIndent) {
      if (trimmed.startsWith("-")) {
        // List item. Look ahead to find the merge-key field on this
        // item — same indent or deeper, until the next sibling `-`.
        const mergeKey = findArrayMergeKey(model, i, indent);
        if (mergeKey) {
          parts.unshift(`[${mergeKey.field}=${mergeKey.value}]`);
        } else {
          parts.unshift(`[]`);
        }
      } else {
        const colonIdx = trimmed.indexOf(":");
        if (colonIdx > 0) {
          parts.unshift(trimmed.slice(0, colonIdx));
        }
      }
      curIndent = indent;
      if (indent === 0) break;
    }
  }

  // Collapse `prefix.[merge]` → `prefix[merge]` (no dot before brackets)
  return parts.join(".").replace(/\.\[/g, "[");
}

/**
 * findArrayMergeKey scans an array item starting at `atLine` (whose
 * leading character is `-`) for a merge-key field. Returns the field
 * name + value as strings. Returns null if no recognizable merge key
 * found in the first few lines of the item.
 */
function findArrayMergeKey(
  model: monaco.editor.ITextModel,
  atLine: number,
  atIndent: number,
): { field: string; value: string } | null {
  const total = model.getLineCount();
  // Inspect the first 10 lines of the item — merge keys are usually
  // the first or second field. The item ends when we hit a line at
  // `atIndent` or shallower (next sibling).
  for (let i = atLine; i <= Math.min(atLine + 10, total); i++) {
    const raw = model.getLineContent(i);
    if (!raw.trim()) continue;
    const indent = raw.length - raw.trimStart().length;
    if (i > atLine && indent <= atIndent) break;
    const trimmed = raw.trim().replace(/^-\s*/, "");
    const m = trimmed.match(/^([\w.\-/]+):\s*(.+?)\s*$/);
    if (m && MERGE_KEY_FIELDS.includes(m[1])) {
      // Strip surrounding quotes for value
      const value = m[2].replace(/^["']|["']$/g, "");
      return { field: m[1], value };
    }
  }
  return null;
}
