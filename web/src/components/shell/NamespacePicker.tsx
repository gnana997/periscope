import { useEffect, useRef, useState } from "react";
import { useParams, useSearchParams } from "react-router-dom";
import { useNamespaces } from "../../hooks/useClusters";
import { cn } from "../../lib/cn";

export function NamespacePicker() {
  const { cluster } = useParams<{ cluster: string }>();
  const [params, setParams] = useSearchParams();
  const namespace = params.get("ns");
  const { data, isLoading } = useNamespaces(cluster);
  const [open, setOpen] = useState(false);
  const wrapperRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const onClick = (e: MouseEvent) => {
      if (
        wrapperRef.current &&
        !wrapperRef.current.contains(e.target as Node)
      ) {
        setOpen(false);
      }
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    document.addEventListener("mousedown", onClick);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onClick);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  const setNamespace = (ns: string | null) => {
    const next = new URLSearchParams(params);
    if (ns === null) next.delete("ns");
    else next.set("ns", ns);
    setParams(next, { replace: true });
    setOpen(false);
  };

  const namespaces = data?.namespaces.map((n) => n.name) ?? [];

  return (
    <div ref={wrapperRef} className="relative">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        disabled={isLoading || !cluster}
        className={cn(
          "flex items-center gap-2 rounded-md border border-border bg-surface px-3 py-1.5 text-[12.5px] transition-colors",
          "hover:border-border-strong",
          open && "border-border-strong",
          (isLoading || !cluster) && "cursor-not-allowed opacity-60",
        )}
      >
        <span className="text-ink-faint">ns</span>
        <span className="font-mono text-ink">{namespace ?? "all"}</span>
        <svg width="9" height="9" viewBox="0 0 10 10" aria-hidden>
          <path
            d="M2 4l3 3 3-3"
            stroke="currentColor"
            strokeWidth="1.3"
            fill="none"
            strokeLinecap="round"
          />
        </svg>
      </button>

      {open && (
        <div className="absolute left-0 top-[calc(100%+4px)] z-30 max-h-[320px] w-64 overflow-auto rounded-md border border-border-strong bg-surface py-1 shadow-[0_8px_28px_rgba(0,0,0,0.12)] dark:shadow-[0_8px_28px_rgba(0,0,0,0.5)]">
          <button
            type="button"
            onClick={() => setNamespace(null)}
            className={cn(
              "flex w-full items-center px-3 py-1.5 text-left text-[12.5px] transition-colors",
              namespace === null
                ? "bg-accent-soft text-accent"
                : "text-ink hover:bg-surface-2",
            )}
          >
            <span className="text-ink-muted">all namespaces</span>
            <span className="ml-auto font-mono text-[10.5px] text-ink-faint">
              {namespaces.length}
            </span>
          </button>
          <div className="my-1 h-px bg-border" />
          {namespaces.map((ns) => (
            <button
              key={ns}
              type="button"
              onClick={() => setNamespace(ns)}
              className={cn(
                "flex w-full items-center px-3 py-1.5 text-left text-[12.5px] transition-colors",
                ns === namespace
                  ? "bg-accent-soft text-accent"
                  : "text-ink hover:bg-surface-2",
              )}
            >
              <span className="font-mono">{ns}</span>
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
