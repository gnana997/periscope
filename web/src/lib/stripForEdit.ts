// stripForEdit — trim server-managed sub-blocks from a K8s YAML
// payload before showing it to the user as an editable buffer.
// `yamlPatch.computeOps` also strips metadata at diff time; this is
// purely a *display* concern (don't show the user a wall of
// `managedFields:`).
//
// Critical: scalars like `uid:`, `resourceVersion:`, `generation:`
// only get stripped when they are *direct children of the top-level
// metadata block*. Without that scope check, the K8s schema validator
// complains about `ownerReferences[].uid` being missing (it's
// required there) — same scalar name, different semantics depending
// on parent.

const META_SCALARS = new Set([
  "uid",
  "resourceVersion",
  "generation",
  "creationTimestamp",
]);

export function stripForEdit(yaml: string): string {
  if (!yaml.includes("managedFields:") && !yaml.includes("status:")) {
    return yaml;
  }
  const lines = yaml.split("\n");
  const out: string[] = [];
  let skipUntilDedentTo: number | null = null;
  // Indent of the `metadata:` block when we're inside it; null otherwise.
  // We're "inside metadata" while subsequent lines indent deeper than
  // metadataAt, and we leave when we hit a line at metadataAt or shallower.
  let metadataAt: number | null = null;

  for (const line of lines) {
    const indent = line.search(/\S/);

    // Active block-skip (continuing to drop a managedFields/status block).
    // The same-indent `- ` case is critical for compact list style:
    // some K8s serializers (cert-manager, Argo) emit list items at the
    // SAME column as the parent key (e.g. `managedFields:` at indent 2,
    // `- apiVersion: ...` also at indent 2). Without this, the orphan
    // list item leaks past the strip and corrupts the post-strip YAML
    // (metadata: would end up containing a stray `- ` sibling, breaking
    // the parse). Built-in apiserver output uses indented-list style so
    // this only bit Custom Resources.
    if (skipUntilDedentTo !== null) {
      const trimmedForSkip = line.trimStart();
      const stillInside =
        indent === -1 ||
        indent > skipUntilDedentTo ||
        (indent === skipUntilDedentTo && trimmedForSkip.startsWith("- "));
      if (stillInside) continue;
      skipUntilDedentTo = null;
    }
    // Track entry/exit of the top-level metadata block by indent.
    if (metadataAt !== null && indent !== -1 && indent <= metadataAt) {
      metadataAt = null;
    }

    const trimmed = line.trimStart();

    // status: and managedFields: are server-only blocks. Strip them
    // wherever they appear at the top level (managedFields lives in
    // metadata, status at root). The block-skip catches the children.
    if (
      trimmed.startsWith("status:") ||
      trimmed.startsWith("managedFields:")
    ) {
      const isBlock = !trimmed.includes(":") || /:\s*$/.test(trimmed);
      if (isBlock) skipUntilDedentTo = indent;
      continue;
    }

    // Direct metadata-scalar strip — only inside metadata: block, only
    // for the four well-known server-managed scalars.
    if (metadataAt !== null && indent === metadataAt + 2) {
      const colonIdx = trimmed.indexOf(":");
      const key = colonIdx > 0 ? trimmed.slice(0, colonIdx) : trimmed;
      if (META_SCALARS.has(key)) {
        const isBlock = /:\s*$/.test(trimmed);
        if (isBlock) skipUntilDedentTo = indent;
        continue;
      }
    }

    // Note we entered metadata: (after deciding not to strip this line).
    if (trimmed.startsWith("metadata:") && indent !== -1) {
      metadataAt = indent;
    }

    out.push(line);
  }
  return out.join("\n");
}
