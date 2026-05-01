import { useState } from "react";
import { useRevealSecretValue } from "../../../hooks/useResource";
import { cn } from "../../../lib/cn";
import type { SecretKey } from "../../../lib/types";

function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / (1024 * 1024)).toFixed(1)} MB`;
}

export function SecretKeyRow({
  cluster,
  ns,
  name,
  k,
}: {
  cluster: string;
  ns: string;
  name: string;
  k: SecretKey;
}) {
  const reveal = useRevealSecretValue();
  const [revealed, setRevealed] = useState(false);
  const [copied, setCopied] = useState(false);

  const value = revealed ? reveal.data : undefined;

  const handleReveal = async () => {
    if (revealed) {
      setRevealed(false);
      reveal.reset();
      return;
    }
    try {
      await reveal.mutateAsync({ cluster, ns, name, key: k.name });
      setRevealed(true);
    } catch {
      // error surfaces via reveal.error
    }
  };

  const handleCopy = async () => {
    try {
      const v =
        revealed && reveal.data
          ? reveal.data
          : await reveal.mutateAsync({ cluster, ns, name, key: k.name });
      await navigator.clipboard.writeText(v);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // silent — clipboard or fetch failed
    }
  };

  return (
    <li className="rounded-md border border-border bg-surface-2/40">
      <div className="flex items-center gap-3 px-3 py-1.5">
        <span className="font-mono text-[12px] text-ink">{k.name}</span>
        <span className="font-mono text-[11px] text-ink-faint tabular">
          {formatBytes(k.size)}
        </span>
        <div className="ml-auto flex items-center gap-1">
          <button
            type="button"
            onClick={handleReveal}
            disabled={reveal.isPending}
            className={cn(
              "inline-flex items-center gap-1 rounded px-2 py-0.5 font-mono text-[11px] transition-colors",
              revealed
                ? "border border-accent/30 bg-accent-soft text-accent"
                : "border border-border text-ink-muted hover:border-border-strong hover:text-ink",
              reveal.isPending && "cursor-not-allowed opacity-60",
            )}
            aria-pressed={revealed}
          >
            {revealed ? "hide" : "reveal"}
          </button>
          <button
            type="button"
            onClick={handleCopy}
            className={cn(
              "inline-flex items-center gap-1 rounded px-2 py-0.5 font-mono text-[11px] transition-colors",
              copied
                ? "border border-green/40 bg-green-soft text-green"
                : "border border-border text-ink-muted hover:border-border-strong hover:text-ink",
            )}
          >
            {copied ? "copied" : "copy"}
          </button>
        </div>
      </div>
      {revealed && value !== undefined && (
        <pre className="mx-3 mb-2 overflow-x-auto rounded border border-border bg-bg px-3 py-2 font-mono text-[11.5px] leading-relaxed text-ink">
          {value || <span className="text-ink-faint italic">(empty)</span>}
        </pre>
      )}
      {reveal.isError && (
        <div className="mx-3 mb-2 rounded border border-red/40 bg-red-soft px-2 py-1 font-mono text-[11px] text-red">
          reveal failed: {(reveal.error as Error)?.message ?? "unknown"}
        </div>
      )}
    </li>
  );
}
