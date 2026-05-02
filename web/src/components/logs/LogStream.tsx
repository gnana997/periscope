import { useEffect, useMemo, useRef, useState } from "react";
import { useVirtualizer } from "@tanstack/react-virtual";
import { cn } from "../../lib/cn";
import type { LogLine } from "../../hooks/useLogStream";
import { podColor } from "./podColor";

const SEARCH_CONTEXT = 5;

type Row =
  | { kind: "line"; line: LogLine; isMatch: boolean; index: number }
  | { kind: "divider"; key: string }
  | { kind: "no-matches" };

export interface LogStreamProps {
  lines: LogLine[];
  search: string;
  wrap: boolean;
  timestamps: boolean;
  follow: boolean;
  // For multi-pod (deployment) streams: when non-empty, only lines whose
  // pod attribution is in this set are shown. Empty = show all.
  podFilter?: string[];
}

export function LogStream(props: LogStreamProps) {
  const { lines, search, wrap, timestamps, follow, podFilter } = props;
  const parentRef = useRef<HTMLDivElement | null>(null);

  // Apply pod filter first; the search/context-expansion logic runs over
  // the already-filtered slice so context windows make sense.
  const filteredLines = useMemo(() => {
    if (!podFilter || podFilter.length === 0) return lines;
    const set = new Set(podFilter);
    return lines.filter((l) => l.pod !== undefined && set.has(l.pod));
  }, [lines, podFilter]);

  const rows: Row[] = useMemo(() => {
    if (!search) {
      return filteredLines.map((l, i) => ({
        kind: "line" as const,
        line: l,
        isMatch: false,
        index: i,
      }));
    }

    const q = search.toLowerCase();
    const matches: number[] = [];
    for (let i = 0; i < filteredLines.length; i++) {
      if (filteredLines[i].text.toLowerCase().includes(q)) matches.push(i);
    }
    if (matches.length === 0) return [{ kind: "no-matches" }];

    const matchSet = new Set(matches);
    const include = new Set<number>();
    for (const m of matches) {
      const lo = Math.max(0, m - SEARCH_CONTEXT);
      const hi = Math.min(filteredLines.length - 1, m + SEARCH_CONTEXT);
      for (let i = lo; i <= hi; i++) include.add(i);
    }
    const sorted = [...include].sort((a, b) => a - b);

    const out: Row[] = [];
    let prev = -2;
    for (const i of sorted) {
      if (prev !== -2 && i !== prev + 1) {
        out.push({ kind: "divider", key: `d-${prev}-${i}` });
      }
      out.push({
        kind: "line",
        line: filteredLines[i],
        isMatch: matchSet.has(i),
        index: i,
      });
      prev = i;
    }
    return out;
  }, [filteredLines, search]);

  // TanStack Virtual returns functions that the React Compiler can't
  // safely memoize; the warning is informational and doesn't apply
  // since useVirtualizer manages its own internal stability.
  // eslint-disable-next-line react-hooks/incompatible-library
  const virtualizer = useVirtualizer({
    count: rows.length,
    getScrollElement: () => parentRef.current,
    estimateSize: (i) => (rows[i]?.kind === "divider" ? 14 : 22),
    overscan: 24,
  });

  // Auto-stick to bottom while follow is on. wasAtBottomRef drives the
  // auto-scroll decision (avoids re-renders); isAtBottom mirrors it as
  // state so the jump-to-bottom FAB can show/hide.
  const wasAtBottomRef = useRef(true);
  const [isAtBottom, setIsAtBottom] = useState(true);

  // userScrollActiveRef gates the auto-scroll effect. When the user is
  // actively scrolling (wheel/touch/key input within the last 200ms), we
  // suppress auto-scroll so that incoming lines can't snap them back to
  // the bottom mid-scroll. Without this, a high-volume stream's auto-
  // scroll wins the race against the user's queued scroll event.
  const userScrollActiveRef = useRef(false);

  const recomputeAtBottom = () => {
    const el = parentRef.current;
    if (!el) return;
    const slack = 4;
    const overflowed = el.scrollHeight > el.clientHeight + slack;
    const atBottom = !overflowed
      ? true
      : el.scrollHeight - el.scrollTop - el.clientHeight <= slack;
    wasAtBottomRef.current = atBottom;
    setIsAtBottom((prev) => (prev === atBottom ? prev : atBottom));
  };

  useEffect(() => {
    const el = parentRef.current;
    if (!el) return;
    el.addEventListener("scroll", recomputeAtBottom);
    return () => el.removeEventListener("scroll", recomputeAtBottom);
     
  }, []);

  // Detect user-initiated scroll input synchronously, before the resulting
  // scroll event has had a chance to fire. We listen for wheel, touchmove
  // and arrow/page/home/end key presses on the scroll container.
  useEffect(() => {
    const el = parentRef.current;
    if (!el) return;
    let timer: number | undefined;
    const mark = () => {
      userScrollActiveRef.current = true;
      if (timer !== undefined) window.clearTimeout(timer);
      timer = window.setTimeout(() => {
        userScrollActiveRef.current = false;
      }, 200);
    };
    const isScrollKey = (e: KeyboardEvent) =>
      e.key === "ArrowUp" ||
      e.key === "ArrowDown" ||
      e.key === "PageUp" ||
      e.key === "PageDown" ||
      e.key === "Home" ||
      e.key === "End";
    const onKey = (e: KeyboardEvent) => {
      if (isScrollKey(e)) mark();
    };
    el.addEventListener("wheel", mark, { passive: true });
    el.addEventListener("touchmove", mark, { passive: true });
    el.addEventListener("keydown", onKey);
    return () => {
      if (timer !== undefined) window.clearTimeout(timer);
      el.removeEventListener("wheel", mark);
      el.removeEventListener("touchmove", mark);
      el.removeEventListener("keydown", onKey);
    };
  }, []);

  useEffect(() => {
    if (rows.length === 0) return;
    if (
      follow &&
      wasAtBottomRef.current &&
      !userScrollActiveRef.current
    ) {
      virtualizer.scrollToIndex(rows.length - 1, { align: "end" });
    }
    // After new rows render, the scroll container's geometry has changed —
    // recompute even if no scroll event fires (matters when initial lines
    // overflow the viewport before the user has interacted).
    recomputeAtBottom();
     
  }, [rows.length, follow, virtualizer]);

  const jumpToBottom = () => {
    if (rows.length === 0) return;
    virtualizer.scrollToIndex(rows.length - 1, { align: "end" });
    wasAtBottomRef.current = true;
    setIsAtBottom(true);
  };

  // Copy-with-context: when the user clicks the per-line copy button, we
  // grab ±COPY_CONTEXT lines from the unfiltered buffer (so the copy is
  // useful for incident reports even when search is filtering the view).
  const [copiedAt, setCopiedAt] = useState<number | null>(null);
  const copyTimerRef = useRef<number | null>(null);
  useEffect(() => {
    return () => {
      if (copyTimerRef.current !== null) {
        window.clearTimeout(copyTimerRef.current);
      }
    };
  }, []);

  // Per-line JSON pretty-print expansion. Tracked by buffer index so the
  // expansion sticks to the actual log entry, not the virtual row position.
  const [expandedSet, setExpandedSet] = useState<Set<number>>(new Set());
  const toggleExpand = (bufferIndex: number) => {
    setExpandedSet((prev) => {
      const next = new Set(prev);
      if (next.has(bufferIndex)) next.delete(bufferIndex);
      else next.add(bufferIndex);
      return next;
    });
  };

  const handleCopy = async (bufferIndex: number) => {
    const COPY_CONTEXT = 5;
    const lo = Math.max(0, bufferIndex - COPY_CONTEXT);
    const hi = Math.min(filteredLines.length - 1, bufferIndex + COPY_CONTEXT);
    const slice = filteredLines.slice(lo, hi + 1);
    const text = slice
      .map((l) => {
        const head = l.pod ? `[${l.pod}] ` : "";
        return l.ts ? `${l.ts} ${head}${l.text}` : `${head}${l.text}`;
      })
      .join("\n");
    try {
      await navigator.clipboard.writeText(text);
      setCopiedAt(bufferIndex);
      if (copyTimerRef.current !== null) {
        window.clearTimeout(copyTimerRef.current);
      }
      copyTimerRef.current = window.setTimeout(() => setCopiedAt(null), 1500);
    } catch {
      // Clipboard API can fail in insecure contexts; fail silently.
    }
  };

  if (lines.length === 0) {
    return (
      <div className="flex flex-1 items-center justify-center bg-bg">
        <div className="font-mono text-[12px] text-ink-faint">
          waiting for logs???
        </div>
      </div>
    );
  }

  return (
    <div className="relative min-h-0 flex-1 bg-bg">
      <div
        ref={parentRef}
        className="absolute inset-0 overflow-y-auto"
      >
      <div
        style={{
          height: virtualizer.getTotalSize(),
          width: "100%",
          position: "relative",
        }}
      >
        {virtualizer.getVirtualItems().map((vi) => {
          const row = rows[vi.index];
          return (
            <div
              key={vi.key}
              data-index={vi.index}
              ref={virtualizer.measureElement}
              style={{
                position: "absolute",
                top: 0,
                left: 0,
                width: "100%",
                transform: `translateY(${vi.start}px)`,
              }}
            >
              {row.kind === "divider" ? (
                <Divider />
              ) : row.kind === "no-matches" ? (
                <NoMatches />
              ) : (
                <Line
                  line={row.line}
                  isMatch={row.isMatch}
                  search={search}
                  wrap={wrap}
                  showTimestamp={timestamps}
                  bufferIndex={row.index}
                  copied={copiedAt === row.index}
                  onCopy={handleCopy}
                  expanded={expandedSet.has(row.index)}
                  onToggleExpand={toggleExpand}
                />
              )}
            </div>
          );
        })}
      </div>
      </div>
      {!isAtBottom && rows.length > 0 && (
        <button
          type="button"
          onClick={jumpToBottom}
          className="absolute bottom-4 right-6 z-10 flex items-center gap-1.5 rounded-full bg-accent px-3.5 py-2 font-mono text-[11.5px] font-medium text-bg shadow-lg transition-transform hover:scale-105"
        >
          <span className="text-[14px] leading-none">↓</span>
          jump to bottom
        </button>
      )}
    </div>
  );
}

function Divider() {
  return (
    <div className="flex items-center gap-2 px-5 py-1">
      <span className="h-px flex-1 bg-border" />
      <span className="font-mono text-[9.5px] uppercase tracking-[0.1em] text-ink-faint">
        ?????
      </span>
      <span className="h-px flex-1 bg-border" />
    </div>
  );
}

function NoMatches() {
  return (
    <div className="flex items-center justify-center px-5 py-6 font-mono text-[12px] text-ink-faint">
      no matches in buffer
    </div>
  );
}

const LEVEL_RE_HIGH = /(?:^|[\s[(])(ERROR|FATAL|PANIC|CRITICAL)(?:[\])\s:]|$)/;
const LEVEL_RE_MID = /(?:^|[\s[(])(WARN(?:ING)?)(?:[\])\s:]|$)/;
const LEVEL_RE_JSON_HIGH = /"level"\s*:\s*"(?:error|fatal|panic|critical)"/i;
const LEVEL_RE_JSON_MID = /"level"\s*:\s*"warn(?:ing)?"/i;

function inferLevel(text: string): "high" | "mid" | null {
  const head = text.slice(0, 120);
  if (LEVEL_RE_HIGH.test(head) || LEVEL_RE_JSON_HIGH.test(head)) return "high";
  if (LEVEL_RE_MID.test(head) || LEVEL_RE_JSON_MID.test(head)) return "mid";
  return null;
}

function Line({
  line,
  isMatch,
  search,
  wrap,
  showTimestamp,
  bufferIndex,
  copied,
  onCopy,
  expanded,
  onToggleExpand,
}: {
  line: LogLine;
  isMatch: boolean;
  search: string;
  wrap: boolean;
  showTimestamp: boolean;
  bufferIndex: number;
  copied: boolean;
  onCopy: (bufferIndex: number) => void;
  expanded: boolean;
  onToggleExpand: (bufferIndex: number) => void;
}) {
  const level = inferLevel(line.text);
  const ts = line.ts ? formatShortTimestamp(line.ts) : "";
  const prettyJson = useMemo(() => findJsonObject(line.text), [line.text]);

  return (
    <div
      className={cn(
        "group relative border-l-2 pl-5 font-mono text-[12px] leading-[1.6]",
        level === "high"
          ? "border-l-red bg-red-soft/15 text-red"
          : level === "mid"
            ? "border-l-yellow bg-yellow-soft/15 text-yellow"
            : "border-l-transparent text-ink",
        isMatch && "bg-accent-soft/40",
      )}
    >
      <div className="absolute right-2 top-1 z-10 flex items-center gap-1">
        {prettyJson && (
          <button
            type="button"
            onClick={(e) => {
              e.stopPropagation();
              onToggleExpand(bufferIndex);
            }}
            title={expanded ? "Collapse JSON" : "Pretty-print JSON inline"}
            className={cn(
              "rounded border px-1.5 py-0.5 font-mono text-[10px] transition-opacity",
              expanded
                ? "border-accent bg-bg text-accent opacity-100"
                : "border-border bg-bg text-ink-faint opacity-0 hover:border-border-strong hover:text-accent group-hover:opacity-100",
            )}
          >
            {expanded ? "▾ json" : "▸ json"}
          </button>
        )}
        <button
          type="button"
          onClick={(e) => {
            e.stopPropagation();
            onCopy(bufferIndex);
          }}
          title={copied ? "Copied!" : "Copy this line + ±5 surrounding"}
          className={cn(
            "rounded border px-1.5 py-0.5 font-mono text-[10px] transition-opacity",
            copied
              ? "border-green bg-bg text-green opacity-100"
              : "border-border bg-bg text-ink-faint opacity-0 hover:border-border-strong hover:text-accent group-hover:opacity-100",
          )}
        >
          {copied ? "✓ copied" : "copy ±5"}
        </button>
      </div>
      <div className="flex items-start pr-24">
      {showTimestamp && (
        <span
          style={{ paddingRight: "1.5rem", minWidth: "5.25rem" }}
          className="sticky left-0 shrink-0 select-none text-[10.5px] tabular-nums text-ink-faint"
          title={line.ts}
        >
          {ts}
        </span>
      )}
      {line.pod && (
        <span
          className="mr-3 shrink-0 select-none font-medium text-[10.5px]"
          style={{ color: podColor(line.pod) }}
          title={line.pod}
        >
          {line.pod}
        </span>
      )}
      <span
        className={cn(
          "min-w-0",
          wrap ? "break-words whitespace-pre-wrap" : "truncate",
        )}
      >
        {search ? (
          <Highlighted text={line.text} query={search} />
        ) : (
          line.text
        )}
      </span>
      </div>
      {expanded && prettyJson && (
        <pre
          className="mb-2 ml-[5.25rem] mr-2 mt-1 overflow-x-auto rounded-md border border-border bg-surface-2/40 px-3 py-2 text-[11.5px] leading-[1.5] text-ink whitespace-pre"
        >
          {prettyJson}
        </pre>
      )}
    </div>
  );
}

// findJsonObject pulls the first balanced JSON object out of a log line, if
// one is present. Naïve strategy: find the first `{` and the last `}` and
// try to parse the slice — handles the common case (one JSON object per
// line, possibly with surrounding text) and fails fast on multi-object or
// malformed lines, which is fine since the chip just won't render.
function findJsonObject(text: string): string | null {
  const start = text.indexOf("{");
  const end = text.lastIndexOf("}");
  if (start < 0 || end <= start) return null;
  const candidate = text.slice(start, end + 1);
  try {
    const parsed = JSON.parse(candidate);
    if (typeof parsed === "object" && parsed !== null) {
      return JSON.stringify(parsed, null, 2);
    }
  } catch {
    // Not valid JSON; no chip will render.
  }
  return null;
}

function formatShortTimestamp(iso: string): string {
  // "2026-05-01T12:34:56.789Z" -> "12:34:56"
  const t = iso.indexOf("T");
  if (t < 0) return iso;
  const tail = iso.slice(t + 1, t + 9);
  return tail;
}

function Highlighted({ text, query }: { text: string; query: string }) {
  if (!query) return <>{text}</>;
  const lc = text.toLowerCase();
  const q = query.toLowerCase();
  const parts: Array<{ text: string; match: boolean }> = [];
  let i = 0;
  while (i < text.length) {
    const found = lc.indexOf(q, i);
    if (found < 0) {
      parts.push({ text: text.slice(i), match: false });
      break;
    }
    if (found > i) parts.push({ text: text.slice(i, found), match: false });
    parts.push({ text: text.slice(found, found + q.length), match: true });
    i = found + q.length;
  }
  return (
    <>
      {parts.map((p, idx) =>
        p.match ? (
          <mark
            key={idx}
            className="bg-accent text-bg px-0.5 rounded-[2px]"
          >
            {p.text}
          </mark>
        ) : (
          <span key={idx}>{p.text}</span>
        ),
      )}
    </>
  );
}
