import { Fragment, type ReactNode, useState } from "react";
import { useYaml } from "../../hooks/useResource";
import { cn } from "../../lib/cn";
import { DetailError, DetailLoading } from "./states";

interface YamlViewProps {
  cluster: string;
  kind: "pods" | "deployments" | "services" | "configmaps" | "namespaces";
  ns: string;
  name: string;
}

export function YamlView({ cluster, kind, ns, name }: YamlViewProps) {
  const { data, isLoading, isError, error } = useYaml(cluster, kind, ns, name, true);
  const [copied, setCopied] = useState(false);

  if (isLoading) return <DetailLoading label="loading yaml…" />;
  if (isError)
    return <DetailError message={(error as Error)?.message ?? "unknown"} />;
  if (!data) return null;

  const lines = data.replace(/\n+$/, "").split("\n");

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(data);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // clipboard unavailable (insecure context, etc.) — silent fail
    }
  };

  return (
    <div className="relative">
      {/* Sticky copy chip */}
      <div className="pointer-events-none sticky top-0 z-10 flex justify-end">
        <button
          type="button"
          onClick={handleCopy}
          className={cn(
            "pointer-events-auto m-2 inline-flex items-center gap-1.5 rounded-md border bg-surface px-2.5 py-1 font-mono text-[11px] shadow-sm transition-colors",
            copied
              ? "border-green/40 bg-green-soft text-green"
              : "border-border text-ink-muted hover:border-border-strong hover:text-ink",
          )}
          aria-label="Copy YAML to clipboard"
        >
          {copied ? <CheckIcon /> : <ClipboardIcon />}
          {copied ? "copied" : "copy"}
        </button>
      </div>

      <pre className="grid grid-cols-[auto_1fr] gap-x-4 px-4 pb-5 pt-1 font-mono text-[11.5px] leading-[1.55]">
        {lines.map((line, i) => (
          <Fragment key={i}>
            <span className="select-none text-right text-ink-faint tabular">
              {i + 1}
            </span>
            <code className="whitespace-pre text-ink">{highlightLine(line)}</code>
          </Fragment>
        ))}
      </pre>
    </div>
  );
}

// ---------- syntax highlighter ----------

/**
 * Tokenize a single YAML line and return styled React nodes. Designed for
 * the simple YAML the Kubernetes API produces (no anchors, no aliases, no
 * multi-line scalars in our output). Anything not parsed cleanly falls
 * through as default-styled text.
 */
function highlightLine(line: string): ReactNode {
  if (line === "") return " "; // preserve blank line height

  // Whole-line comment: leading whitespace + "# rest"
  const fullComment = line.match(/^(\s*)(#.*)$/);
  if (fullComment) {
    return (
      <>
        {fullComment[1]}
        <span className="italic text-ink-faint">{fullComment[2]}</span>
      </>
    );
  }

  // Match: indent (\s*), optional "- " list marker,
  //        optional key (chars + colon), optional value (rest of line)
  const m = line.match(
    /^(\s*)(-\s+)?(?:([\w.\-/]+)(:))?(\s*)(.*?)(\s+#.*)?$/,
  );
  if (!m) return line;

  const [, indent, dash, key, colon, valueGap, value, trailingComment] = m;

  const out: ReactNode[] = [];
  if (indent) out.push(indent);
  if (dash) out.push(<span key="dash" className="text-ink-faint">{dash}</span>);
  if (key && colon) {
    out.push(<span key="k" className="text-ink">{key}</span>);
    out.push(<span key="c" className="text-ink-faint">{colon}</span>);
  }
  if (valueGap) out.push(valueGap);
  if (value) out.push(<Fragment key="v">{highlightValue(value)}</Fragment>);
  if (trailingComment)
    out.push(
      <span key="tc" className="italic text-ink-faint">{trailingComment}</span>,
    );

  return <>{out}</>;
}

function highlightValue(value: string): ReactNode {
  // Quoted strings — green
  if (
    (value.startsWith('"') && value.endsWith('"')) ||
    (value.startsWith("'") && value.endsWith("'"))
  ) {
    return <span className="text-green">{value}</span>;
  }
  // Pure numbers — yellow
  if (/^-?\d+(\.\d+)?$/.test(value)) {
    return <span className="text-yellow">{value}</span>;
  }
  // Booleans / null — accent (orange)
  if (/^(true|false|null|~)$/.test(value)) {
    return <span className="text-accent">{value}</span>;
  }
  // Block-scalar indicators (|, >, |-, >-, |+, >+) — accent, indicating "incoming text"
  if (/^[|>][+-]?$/.test(value)) {
    return <span className="text-accent">{value}</span>;
  }
  // Default ink
  return value;
}

// ---------- icons ----------

function ClipboardIcon() {
  return (
    <svg width="11" height="11" viewBox="0 0 11 11" aria-hidden>
      <rect
        x="2.5"
        y="1.5"
        width="6"
        height="8"
        rx="1"
        stroke="currentColor"
        strokeWidth="1.2"
        fill="none"
      />
      <path
        d="M4 1.5h3v1.5H4z"
        fill="currentColor"
      />
    </svg>
  );
}

function CheckIcon() {
  return (
    <svg width="11" height="11" viewBox="0 0 11 11" aria-hidden>
      <path
        d="M2 5.5l2.4 2.4L9 3.2"
        stroke="currentColor"
        strokeWidth="1.6"
        strokeLinecap="round"
        strokeLinejoin="round"
        fill="none"
      />
    </svg>
  );
}
