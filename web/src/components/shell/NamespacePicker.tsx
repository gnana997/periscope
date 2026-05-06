import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useParams, useSearchParams } from "react-router-dom";
import { useNamespaces } from "../../hooks/useClusters";
import { cn } from "../../lib/cn";

export function NamespacePicker() {
  const { cluster } = useParams<{ cluster: string }>();
  const [params, setParams] = useSearchParams();
  const namespace = params.get("ns");
  const { data, isLoading } = useNamespaces(cluster);
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const wrapperRef = useRef<HTMLDivElement>(null);
  const searchRef = useRef<HTMLInputElement>(null);

  // Single close path used by every dismissal route (button toggle,
  // outside-click, Escape, namespace selection). Resets the filter
  // here rather than via a cascading effect on `open` — sticky filter
  // across opens feels surprising on a list that changes per cluster.
  const closeMenu = useCallback(() => {
    setOpen(false);
    setQuery("");
  }, []);

  useEffect(() => {
    if (!open) return;
    // Auto-focus the search input on open so the user can start
    // typing immediately. Microtask defer keeps the click that
    // opened the menu from stealing focus back.
    queueMicrotask(() => searchRef.current?.focus());
    const onClick = (e: MouseEvent) => {
      if (
        wrapperRef.current &&
        !wrapperRef.current.contains(e.target as Node)
      ) {
        closeMenu();
      }
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") closeMenu();
    };
    document.addEventListener("mousedown", onClick);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onClick);
      document.removeEventListener("keydown", onKey);
    };
  }, [open, closeMenu]);

  const setNamespace = (ns: string | null) => {
    const next = new URLSearchParams(params);
    if (ns === null) next.delete("ns");
    else next.set("ns", ns);
    setParams(next, { replace: true });
    closeMenu();
  };

  const namespaces = useMemo(
    () => data?.namespaces.map((n) => n.name) ?? [],
    [data],
  );
  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return namespaces;
    return namespaces.filter((n) => n.toLowerCase().includes(q));
  }, [namespaces, query]);

  return (
    <div ref={wrapperRef} className="relative">
      <button
        type="button"
        onClick={() => (open ? closeMenu() : setOpen(true))}
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
        // Outer panel: width fixed at w-64; height is responsive to
        // viewport via min(70vh, 520px) so big screens get a tall
        // useful list while small screens still cap inside the
        // visible area. flex-col + overflow-hidden keeps the search
        // strip pinned while only the list area scrolls.
        <div className="absolute right-0 top-[calc(100%+4px)] z-30 flex w-64 max-h-[min(70vh,520px)] flex-col overflow-hidden rounded-md border border-border-strong bg-surface shadow-[0_8px_28px_rgba(0,0,0,0.12)] dark:shadow-[0_8px_28px_rgba(0,0,0,0.5)]">
          <div className="shrink-0 border-b border-border bg-surface p-2">
            <input
              ref={searchRef}
              type="text"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="filter namespaces…"
              autoComplete="off"
              spellCheck={false}
              className="w-full rounded-sm border border-border bg-bg px-2 py-1 font-mono text-[12px] text-ink placeholder:text-ink-faint focus:border-border-strong focus:outline-none"
            />
          </div>
          <div className="flex-1 overflow-auto py-1">
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
            {filtered.length === 0 ? (
              <div className="px-3 py-2 text-[12px] italic text-ink-faint">
                no namespaces match &ldquo;{query}&rdquo;
              </div>
            ) : (
              filtered.map((ns) => (
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
              ))
            )}
          </div>
        </div>
      )}
    </div>
  );
}
