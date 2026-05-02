import { useEffect, useMemo, useRef, useState } from "react";
import { useMatch, useNavigate } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { api } from "../../lib/api";
import { cn } from "../../lib/cn";
import type { SearchKind, SearchResult } from "../../lib/types";

const IS_MAC =
  typeof navigator !== "undefined" &&
  /mac|iphone|ipad|ipod/i.test(navigator.platform);

/**
 * SearchPalette — global Cmd/Ctrl-K resource finder.
 *
 * Mounted at App root, OUTSIDE the <Routes> tree, so it can pop over
 * any page. Because the palette is outside Routes, `useParams` returns
 * empty — we use `useMatch("/clusters/:cluster/*")` to detect the
 * current cluster from the URL itself.
 *
 * Visual notes:
 *   - The input opts out of the global :focus-visible accent ring; the
 *     containing field's border shifts to border-accent on focus
 *     instead. Cleaner, in keeping with the rest of the operator UI.
 *   - Result rows lead with a 3-letter kind badge (pod, dep, sts, …)
 *     — saves horizontal space and matches kubectl muscle memory.
 *   - Matched substring gets an accent-soft background tint, more
 *     legible at our small mono sizes than an underline.
 */

// 3-letter codes mirror kubectl's `kubectl get` short forms where
// possible. Operators recognize these at a glance.
const KIND_BADGE: Record<SearchKind, string> = {
  pods: "pod",
  deployments: "dep",
  statefulsets: "sts",
  daemonsets: "ds",
  services: "svc",
  configmaps: "cm",
  secrets: "sec",
  namespaces: "ns",
};

const KIND_LABEL: Record<SearchKind, string> = {
  pods: "pods",
  deployments: "deployments",
  statefulsets: "statefulsets",
  daemonsets: "daemonsets",
  services: "services",
  configmaps: "configmaps",
  secrets: "secrets",
  namespaces: "namespaces",
};

export function SearchPalette({
  open,
  onClose,
}: {
  open: boolean;
  onClose: () => void;
}) {
  // useMatch — works outside Routes, unlike useParams which only sees
  // params from the closest <Route> ancestor. Without this the palette
  // always thinks "no cluster selected" because it's a sibling of
  // <Routes>, not a descendant.
  const match = useMatch("/clusters/:cluster/*");
  const cluster = match?.params.cluster ?? "";
  const navigate = useNavigate();

  const [query, setQuery] = useState("");
  const [debounced, setDebounced] = useState("");
  const [activeIndex, setActiveIndex] = useState(0);
  const [focused, setFocused] = useState(false);
  const inputRef = useRef<HTMLInputElement | null>(null);
  const listRef = useRef<HTMLUListElement | null>(null);

  useEffect(() => {
    if (!open) {
      setQuery("");
      setDebounced("");
      setActiveIndex(0);
      setFocused(false);
    } else {
      queueMicrotask(() => inputRef.current?.focus());
    }
  }, [open]);

  useEffect(() => {
    const handle = window.setTimeout(() => setDebounced(query.trim()), 150);
    return () => window.clearTimeout(handle);
  }, [query]);

  const { data, isFetching, isError } = useQuery({
    queryKey: ["search", cluster, debounced],
    enabled: Boolean(open && cluster && debounced.length > 0),
    queryFn: ({ signal }) =>
      api.search(cluster, debounced, { limit: 10 }, signal),
    staleTime: 5_000,
  });

  const results = useMemo(() => data?.results ?? [], [data]);

  useEffect(() => {
    setActiveIndex(0);
  }, [results.length, debounced]);

  useEffect(() => {
    if (!listRef.current) return;
    const el = listRef.current.querySelector<HTMLElement>(`[data-active="true"]`);
    el?.scrollIntoView({ block: "nearest" });
  }, [activeIndex]);

  const grouped = useMemo(() => {
    const groups: { kind: SearchKind; rows: SearchResult[]; startIndex: number }[] = [];
    let currentKind: SearchKind | null = null;
    results.forEach((r, idx) => {
      if (r.kind !== currentKind) {
        groups.push({ kind: r.kind, rows: [], startIndex: idx });
        currentKind = r.kind;
      }
      groups[groups.length - 1].rows.push(r);
    });
    return groups;
  }, [results]);

  function navigateToResult(r: SearchResult) {
    const path = pathForResult(cluster, r);
    if (path) {
      navigate(path);
      onClose();
    }
  }

  function onKeyDown(e: React.KeyboardEvent<HTMLInputElement>) {
    if (e.key === "Escape") {
      e.preventDefault();
      onClose();
      return;
    }
    if (e.key === "Enter") {
      e.preventDefault();
      const r = results[activeIndex];
      if (r) navigateToResult(r);
      return;
    }
    if (e.key === "ArrowDown") {
      e.preventDefault();
      setActiveIndex((i) => Math.min(i + 1, Math.max(0, results.length - 1)));
      return;
    }
    if (e.key === "ArrowUp") {
      e.preventDefault();
      setActiveIndex((i) => Math.max(0, i - 1));
      return;
    }
  }

  if (!open) return null;

  return (
    <div
      role="presentation"
      onClick={onClose}
      className="fixed inset-0 z-50 flex items-start justify-center bg-ink/40 px-4 pt-[14vh] backdrop-blur-md"
    >
      <div
        onClick={(e) => e.stopPropagation()}
        className="w-full max-w-2xl overflow-hidden rounded-lg border border-border-strong bg-surface shadow-[0_24px_64px_-16px_rgba(0,0,0,0.45)]"
        role="dialog"
        aria-label="Search resources"
        aria-modal="true"
      >
        {/* Input field — wrapper carries the focus border, the input
            itself opts out of the global accent ring. */}
        <div
          className={cn(
            "flex items-center gap-2.5 border-b px-4 py-3 transition-colors",
            // Focus state shifts to the existing "stronger border" token
            // (~20% opacity ink) instead of border-accent. The accent is
            // burnt orange and reads as red on this otherwise neutral
            // surface — too loud for a focus ring.
            focused ? "border-border-strong" : "border-border",
          )}
        >
          <SearchGlyph focused={focused} />
          <input
            ref={inputRef}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onFocus={() => setFocused(true)}
            onBlur={() => setFocused(false)}
            onKeyDown={onKeyDown}
            placeholder={
              cluster
                ? `search resources in ${cluster}…`
                : "select a cluster to search"
            }
            spellCheck={false}
            autoComplete="off"
            autoCorrect="off"
            autoCapitalize="off"
            disabled={!cluster}
            // outline-none disables the global :focus-visible accent ring
            // for this input specifically. The wrapper border above
            // provides the focus indicator instead.
            className="min-w-0 flex-1 bg-transparent font-mono text-[14.5px] text-ink outline-none placeholder:text-ink-faint focus-visible:outline-none disabled:opacity-50"
          />
          {isFetching && results.length > 0 && (
            <span
              aria-hidden
              className="block size-3 shrink-0 animate-spin rounded-full border-[1.5px] border-border-strong border-t-accent"
            />
          )}
        </div>

        {/* Results body */}
        <div className="max-h-[60vh] overflow-y-auto">
          {!cluster ? (
            <Empty>open a cluster to search its resources</Empty>
          ) : !debounced ? (
            <EmptyHint />
          ) : isFetching && results.length === 0 ? (
            <Empty>searching…</Empty>
          ) : isError ? (
            <Empty tone="red">search failed — try again</Empty>
          ) : results.length === 0 ? (
            <Empty>
              no matches for{" "}
              <span className="font-medium text-ink-muted">"{debounced}"</span>
            </Empty>
          ) : (
            <ul ref={listRef}>
              {grouped.map((group) => (
                <li key={group.kind}>
                  <div className="border-y border-border bg-surface-2/50 px-4 py-1 font-mono text-[10px] uppercase tracking-[0.08em] text-ink-faint">
                    {KIND_LABEL[group.kind]}{" "}
                    <span className="ml-1 normal-case tracking-normal text-ink-faint/70">
                      ({group.rows.length})
                    </span>
                  </div>
                  <ul>
                    {group.rows.map((r, i) => {
                      const flatIndex = group.startIndex + i;
                      const isActive = flatIndex === activeIndex;
                      return (
                        <li
                          key={`${r.kind}/${r.namespace ?? ""}/${r.name}`}
                          data-active={isActive ? "true" : undefined}
                        >
                          <button
                            type="button"
                            onClick={() => navigateToResult(r)}
                            onMouseEnter={() => setActiveIndex(flatIndex)}
                            className={cn(
                              "flex w-full items-center gap-3 px-4 py-1.5 text-left transition-colors",
                              isActive
                                ? "bg-accent-soft"
                                : "hover:bg-surface-2/40",
                            )}
                          >
                            {/* 3-letter kind badge */}
                            <span
                              className={cn(
                                "shrink-0 rounded-sm border px-1 py-px font-mono text-[9.5px] uppercase tracking-[0.04em]",
                                isActive
                                  ? "border-accent/30 bg-surface text-accent"
                                  : "border-border bg-surface-2/60 text-ink-faint",
                              )}
                            >
                              {KIND_BADGE[r.kind]}
                            </span>
                            <HighlightedName
                              name={r.name}
                              query={debounced}
                              active={isActive}
                            />
                            {r.namespace && (
                              <span
                                className={cn(
                                  "shrink-0 font-mono text-[10.5px]",
                                  isActive ? "text-accent/70" : "text-ink-faint",
                                )}
                              >
                                {r.namespace}
                              </span>
                            )}
                          </button>
                        </li>
                      );
                    })}
                  </ul>
                </li>
              ))}
            </ul>
          )}
        </div>

        {/* Footer hint — compact */}
        <div className="flex items-center gap-3 border-t border-border bg-surface-2/40 px-4 py-1.5 font-mono text-[10px] text-ink-faint">
          <span className="flex items-center gap-1">
            <Kbd>↑</Kbd>
            <Kbd>↓</Kbd>
            <span className="ml-0.5">navigate</span>
          </span>
          <span className="flex items-center gap-1">
            <Kbd>↵</Kbd>
            <span className="ml-0.5">open</span>
          </span>
          <span className="flex items-center gap-1">
            <Kbd>esc</Kbd>
            <span className="ml-0.5">close</span>
          </span>
          <span className="ml-auto flex items-center gap-1">
            <Kbd>{IS_MAC ? "⌘" : "Ctrl"}</Kbd>
            <Kbd>K</Kbd>
            <span className="ml-0.5">to toggle</span>
          </span>
        </div>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------

function pathForResult(cluster: string, r: SearchResult): string | null {
  const c = encodeURIComponent(cluster);
  const ns = encodeURIComponent(r.namespace ?? "");
  const n = encodeURIComponent(r.name);
  switch (r.kind) {
    case "pods":
      return `/clusters/${c}/pods?selNs=${ns}&sel=${n}&tab=describe`;
    case "deployments":
      return `/clusters/${c}/deployments?selNs=${ns}&sel=${n}&tab=describe`;
    case "statefulsets":
      return `/clusters/${c}/statefulsets?selNs=${ns}&sel=${n}&tab=describe`;
    case "daemonsets":
      return `/clusters/${c}/daemonsets?selNs=${ns}&sel=${n}&tab=describe`;
    case "services":
      return `/clusters/${c}/services?selNs=${ns}&sel=${n}&tab=describe`;
    case "configmaps":
      return `/clusters/${c}/configmaps?selNs=${ns}&sel=${n}&tab=describe`;
    case "secrets":
      return `/clusters/${c}/secrets?selNs=${ns}&sel=${n}&tab=describe`;
    case "namespaces":
      return `/clusters/${c}/namespaces?sel=${n}&tab=describe`;
    default:
      return null;
  }
}

function HighlightedName({
  name,
  query,
  active,
}: {
  name: string;
  query: string;
  active: boolean;
}) {
  if (!query) {
    return (
      <span
        className={cn(
          "min-w-0 flex-1 truncate font-mono text-[12.5px]",
          active ? "text-accent" : "text-ink",
        )}
      >
        {name}
      </span>
    );
  }
  const idx = name.toLowerCase().indexOf(query.toLowerCase());
  if (idx < 0) {
    return (
      <span
        className={cn(
          "min-w-0 flex-1 truncate font-mono text-[12.5px]",
          active ? "text-accent" : "text-ink",
        )}
      >
        {name}
      </span>
    );
  }
  return (
    <span
      className={cn(
        "min-w-0 flex-1 truncate font-mono text-[12.5px]",
        active ? "text-accent" : "text-ink",
      )}
    >
      {name.slice(0, idx)}
      <span
        className={cn(
          "rounded-sm px-px",
          active
            ? "bg-accent/20 text-accent"
            : "bg-accent-soft text-accent",
        )}
      >
        {name.slice(idx, idx + query.length)}
      </span>
      {name.slice(idx + query.length)}
    </span>
  );
}

function Empty({
  children,
  tone,
}: {
  children: React.ReactNode;
  tone?: "red";
}) {
  return (
    <div
      className={cn(
        "px-4 py-8 text-center font-mono text-[12px]",
        tone === "red" ? "text-red" : "text-ink-faint",
      )}
    >
      {children}
    </div>
  );
}

function EmptyHint() {
  return (
    <div className="px-4 py-8 text-center">
      <p className="font-mono text-[12px] text-ink-muted">type to search</p>
      <p className="mt-1.5 font-mono text-[10.5px] text-ink-faint">
        across pods, deployments, services, configs and more
      </p>
    </div>
  );
}

function Kbd({ children }: { children: React.ReactNode }) {
  return (
    <kbd className="rounded border border-border bg-surface px-1 py-px font-mono text-[9.5px] leading-none text-ink-muted">
      {children}
    </kbd>
  );
}

function SearchGlyph({ focused }: { focused: boolean }) {
  return (
    <svg
      width="14"
      height="14"
      viewBox="0 0 13 13"
      aria-hidden
      className={cn(
        "shrink-0 transition-colors",
        // Match the border treatment: darker grey when focused, faint
        // when not. Keeps the modal in a single neutral family.
        focused ? "text-ink-muted" : "text-ink-faint",
      )}
    >
      <circle cx="5.5" cy="5.5" r="3.6" stroke="currentColor" strokeWidth="1.3" fill="none" />
      <path d="M8.3 8.3l2.4 2.4" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" />
    </svg>
  );
}
