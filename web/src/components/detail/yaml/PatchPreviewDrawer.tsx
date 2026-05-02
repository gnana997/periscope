// PatchPreviewDrawer — push panel showing what the editor will send to
// the apiserver. Three tabs:
//
//   minimal yaml — the SSA payload (only changed paths + identity)
//   ops          — JSON-Patch-shaped Op[] (the canonical internal model)
//   curl         — a runnable curl invocation against the existing PATCH
//                  endpoint, useful for sharing in incident channels
//
// Rendered as a flex sibling of the editor (push, not overlay) so no
// editor content is hidden behind the drawer. Width is controlled by
// the parent (YamlEditor) via the `width` prop and resized via a drag
// handle the parent renders alongside us.

import { useState } from "react";
import { cn } from "../../../lib/cn";
import type { Identity, Op } from "../../../lib/yamlPatch";
import { buildMinimalSSA } from "../../../lib/yamlPatch";

interface PatchPreviewDrawerProps {
  ops: Op[];
  identity: Identity | null;
  cluster: string;
  group: string;
  version: string;
  resource: string;
  namespace?: string;
  name: string;
  width: number;
  onClose: () => void;
}

type Tab = "yaml" | "ops" | "curl";

export function PatchPreviewDrawer({
  ops,
  identity,
  cluster,
  group,
  version,
  resource,
  namespace,
  name,
  width,
  onClose,
}: PatchPreviewDrawerProps) {
  const [tab, setTab] = useState<Tab>("yaml");
  const [copied, setCopied] = useState(false);

  const yamlBody = identity ? buildMinimalSSA(ops, identity) : "";
  const opsJson = JSON.stringify(ops, null, 2);
  const groupSeg = group === "" ? "core" : group;
  const url = namespace
    ? `/api/clusters/${cluster}/resources/${groupSeg}/${version}/${resource}/${namespace}/${name}`
    : `/api/clusters/${cluster}/resources/${groupSeg}/${version}/${resource}/${name}`;
  const curl =
    `curl -X PATCH \\\n` +
    `  '${url}' \\\n` +
    `  -H 'Content-Type: application/yaml' \\\n` +
    `  -H 'Accept: application/json' \\\n` +
    `  --data-binary @- <<'EOF'\n` +
    yamlBody +
    `EOF`;

  let content: string;
  if (tab === "yaml") content = yamlBody || "# (make an edit)";
  else if (tab === "ops") content = ops.length === 0 ? "[]\n# (make an edit)" : opsJson;
  else content = curl;

  const bytes =
    typeof TextEncoder !== "undefined"
      ? new TextEncoder().encode(yamlBody).length
      : yamlBody.length;

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(content);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // clipboard unavailable; silent
    }
  };

  return (
    <aside
      className="flex shrink-0 flex-col bg-surface-2"
      style={{ width: `${width}px` }}
      role="dialog"
      aria-label="Patch preview"
    >
      <header className="flex shrink-0 items-center gap-3 border-b border-border bg-surface px-4 py-2">
        <div className="min-w-0">
          <div className="font-display text-[16px] leading-tight text-ink">
            patch preview
          </div>
          <div className="font-mono text-[10.5px] text-ink-muted">
            what we&apos;ll send to the apiserver
          </div>
        </div>
        <button
          type="button"
          onClick={handleCopy}
          className={cn(
            "ml-auto rounded-sm border px-2 py-1 font-mono text-[10.5px] transition-colors",
            copied
              ? "border-green/40 bg-green-soft text-green"
              : "border-border-strong text-ink-muted hover:border-ink-muted hover:text-ink",
          )}
          title="Copy current tab to clipboard"
        >
          {copied ? "copied" : "copy"}
        </button>
        <button
          type="button"
          onClick={onClose}
          className="flex size-7 items-center justify-center rounded-md text-ink-muted hover:bg-surface-2 hover:text-ink"
          aria-label="Close patch preview"
        >
          <svg width="11" height="11" viewBox="0 0 11 11" aria-hidden>
            <path
              d="M2 2l7 7M9 2l-7 7"
              stroke="currentColor"
              strokeWidth="1.4"
              strokeLinecap="round"
            />
          </svg>
        </button>
      </header>

      <div className="flex shrink-0 gap-1 border-b border-border bg-surface px-2">
        <TabButton active={tab === "yaml"} onClick={() => setTab("yaml")}>
          minimal yaml
        </TabButton>
        <TabButton active={tab === "ops"} onClick={() => setTab("ops")}>
          ops
        </TabButton>
        <TabButton active={tab === "curl"} onClick={() => setTab("curl")}>
          curl
        </TabButton>
      </div>

      <pre className="flex-1 min-h-0 overflow-auto whitespace-pre bg-surface-2 p-3 font-mono text-[11.5px] leading-[1.55] text-ink">
        {content}
      </pre>

      <footer className="shrink-0 border-t border-border bg-surface px-4 py-2 font-mono text-[10.5px] text-ink-muted">
        <Row label="identity fields" value={identity ? "4" : "0"} />
        <Row label="changed fields" value={String(ops.length)} valueClass="text-accent" />
        <Row label="payload size" value={`${bytes} B`} />
        <Row label="field manager" value="periscope-spa" />
        <div className="mt-1 border-t border-border pt-1 text-[10px] text-ink-faint">
          Only fields you actually changed are sent — unchanged fields aren&apos;t
          claimed for ownership, eliminating spurious SSA conflicts.
        </div>
      </footer>
    </aside>
  );
}

function TabButton({
  active,
  onClick,
  children,
}: {
  active: boolean;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "border-b px-2 py-2 font-mono text-[10.5px] transition-colors",
        active
          ? "border-accent text-accent"
          : "border-transparent text-ink-muted hover:text-ink",
      )}
    >
      {children}
    </button>
  );
}

function Row({
  label,
  value,
  valueClass,
}: {
  label: string;
  value: string;
  valueClass?: string;
}) {
  return (
    <div className="flex justify-between gap-3 py-0.5">
      <span>{label}</span>
      <span className={cn("tabular text-ink", valueClass)}>{value}</span>
    </div>
  );
}
