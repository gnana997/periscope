import { useEffect, useMemo, useRef, useState } from "react";
import { useVirtualizer } from "@tanstack/react-virtual";
import { useClusters, useNamespaces } from "../hooks/useClusters";
import { useResource } from "../hooks/useResource";
import { useExecSessions } from "./useExecSessions";
import { cn } from "../lib/cn";
import type { Pod, PodList } from "../lib/types";

/**
 * EmptyPicker is the body of the drawer when it's open with no sessions.
 *
 * Shape: cluster dropdown → namespace dropdown → live filter → virtualized
 * pod list. Clicking a row opens a session (server resolves the default
 * container).
 *
 * The picker is intentionally one-step: type, click, shell. A power user
 * who needs a specific container in a multi-container pod still has the
 * OpenShellButton on the pod detail page with the explicit container picker.
 */

interface EmptyPickerProps {
  /** Cluster name from the current route, if any. Preselects the dropdown. */
  initialCluster?: string;
}

const PHASE_DOT: Record<string, string> = {
  Running: "bg-green",
  Pending: "bg-yellow",
  Succeeded: "bg-ink-faint/50",
  Failed: "bg-red",
  Unknown: "bg-ink-faint/50",
};

function phaseTone(phase: string): string {
  return PHASE_DOT[phase] ?? "bg-ink-faint/50";
}

export function EmptyPicker({ initialCluster }: EmptyPickerProps) {
  const { data: clustersData } = useClusters();
  // PR4: filter out clusters where the operator has disabled exec.
  // Older backends (pre-PR4) don't emit execEnabled — treat absence
  // as "enabled" so the SPA stays usable against mixed deployments.
  const clusters = (clustersData?.clusters ?? []).filter(
    (c) => c.execEnabled !== false,
  );
  const { openSession, setDrawerOpen } = useExecSessions();

  const [cluster, setCluster] = useState<string>(initialCluster ?? "");
  const [namespace, setNamespace] = useState<string>("");
  const [filter, setFilter] = useState<string>("");

  // If the user navigates to a different cluster while the picker is open,
  // sync the selection. Don't fight an explicit user choice though.
  const lastInitialRef = useRef<string | undefined>(initialCluster);
  useEffect(() => {
    if (initialCluster && initialCluster !== lastInitialRef.current) {
      setCluster(initialCluster);
      lastInitialRef.current = initialCluster;
    }
  }, [initialCluster]);

  // Fall back to the first registered cluster when the current selection
  // isn't valid (e.g. registry hadn't loaded on first render).
  useEffect(() => {
    if (clusters.length === 0) return;
    if (!cluster || !clusters.find((c) => c.name === cluster)) {
      setCluster(clusters[0].name);
    }
  }, [clusters, cluster]);

  const namespacesQuery = useNamespaces(cluster || undefined);
  const namespaceItems = namespacesQuery.data?.namespaces ?? [];

  const podsQuery = useResource({
    cluster,
    resource: "pods",
    namespace: namespace || undefined,
  });
  const pods = useMemo<Pod[]>(
    () => (podsQuery.data as PodList | undefined)?.pods ?? [],
    [podsQuery.data],
  );

  // Filter client-side: pods are already namespace-scoped if a ns is set.
  const filtered = useMemo(() => {
    if (!filter) return pods;
    const f = filter.toLowerCase();
    return pods.filter(
      (p) =>
        p.name.toLowerCase().includes(f) ||
        p.namespace.toLowerCase().includes(f),
    );
  }, [pods, filter]);

  // Auto-focus the filter input when the picker mounts so the user can
  // start typing immediately.
  const filterInputRef = useRef<HTMLInputElement | null>(null);
  useEffect(() => {
    filterInputRef.current?.focus();
  }, []);

  // Esc closes the drawer (cancels picker). Doesn't fight the page-level
  // listeners because we capture and stop on the keydown.
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") {
        e.preventDefault();
        setDrawerOpen(false);
      }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [setDrawerOpen]);

  function pickPod(p: Pod) {
    openSession({
      cluster,
      namespace: p.namespace,
      pod: p.name,
      // Empty container → server resolves via default-container annotation
      // or first non-init.
    });
  }

  // --- virtualized list ----------------------------------------------------
  const listRef = useRef<HTMLDivElement | null>(null);
  // TanStack Virtual returns functions that the React Compiler can't
  // safely memoize; warning is informational, useVirtualizer manages
  // its own internal stability.
  // eslint-disable-next-line react-hooks/incompatible-library
  const rowVirtualizer = useVirtualizer({
    count: filtered.length,
    getScrollElement: () => listRef.current,
    estimateSize: () => 28,
    overscan: 8,
  });

  // ------------------------------------------------------------------------

  if (clusters.length === 0) {
    return (
      <div className="flex h-full items-center justify-center px-6 font-mono text-[12px] text-ink-faint">
        no clusters configured — add one to clusters.yaml
      </div>
    );
  }

  const selectedCluster = clusters.find((c) => c.name === cluster);
  const showFilterCount = filter.length > 0;

  return (
    <div className="flex h-full flex-col px-4 py-3">
      {/* selectors row */}
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2 font-mono text-[11px]">
        <Selector
          label="cluster"
          value={cluster}
          onChange={setCluster}
          options={clusters.map((c) => ({ value: c.name, label: c.name }))}
        />
        <Selector
          label="namespace"
          value={namespace}
          onChange={setNamespace}
          options={[
            { value: "", label: "all" },
            ...namespaceItems.map((n) => ({ value: n.name, label: n.name })),
          ]}
        />
        {selectedCluster && (
          <span className="text-ink-faint">
            backend:{" "}
            <span className="text-ink-muted">{selectedCluster.backend}</span>
          </span>
        )}
        <span className="ml-auto tabular-nums text-ink-faint">
          {showFilterCount ? (
            <>
              <span className="text-ink-muted">{filtered.length}</span>
              <span> / </span>
              <span>{pods.length}</span>
            </>
          ) : (
            <span className="text-ink-muted">{pods.length}</span>
          )}{" "}
          pods
        </span>
      </div>

      {/* filter input */}
      <div className="mt-2 flex items-center gap-2 rounded-md border border-border bg-surface px-3 py-1.5 focus-within:border-border-strong">
        <svg
          width="12"
          height="12"
          viewBox="0 0 12 12"
          className="shrink-0 text-ink-faint"
          aria-hidden
        >
          <circle cx="5" cy="5" r="3.4" stroke="currentColor" strokeWidth="1.3" fill="none" />
          <path d="M7.6 7.6l2.2 2.2" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" />
        </svg>
        <input
          ref={filterInputRef}
          type="text"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
          placeholder="filter pods by name or namespace…"
          className="min-w-0 flex-1 bg-transparent font-mono text-[12px] text-ink outline-none placeholder:text-ink-faint"
          spellCheck={false}
          autoComplete="off"
          autoCorrect="off"
          autoCapitalize="off"
          onKeyDown={(e) => {
            // Enter on first match opens it — fastest possible "type and go".
            if (e.key === "Enter" && filtered.length > 0) {
              e.preventDefault();
              pickPod(filtered[0]);
            }
          }}
        />
        <span className="hidden font-mono text-[10px] text-ink-faint sm:inline">
          enter to open
        </span>
      </div>

      {/* result list */}
      <div
        ref={listRef}
        className="mt-2 min-h-0 flex-1 overflow-y-auto rounded-md border border-border bg-surface-2/30"
      >
        {podsQuery.isLoading && (
          <Empty>loading pods…</Empty>
        )}
        {podsQuery.isError && (
          <Empty tone="red">couldn't reach the cluster</Empty>
        )}
        {!podsQuery.isLoading && filtered.length === 0 && (
          <Empty>
            {pods.length === 0
              ? "no pods in this namespace"
              : "no matches — try a different filter"}
          </Empty>
        )}
        {!podsQuery.isLoading && filtered.length > 0 && (
          <div
            style={{
              height: rowVirtualizer.getTotalSize(),
              position: "relative",
            }}
          >
            {rowVirtualizer.getVirtualItems().map((row) => {
              const p = filtered[row.index];
              return (
                <div
                  key={`${p.namespace}/${p.name}`}
                  style={{
                    position: "absolute",
                    top: 0,
                    left: 0,
                    right: 0,
                    height: row.size,
                    transform: `translateY(${row.start}px)`,
                  }}
                >
                  <PodRow
                    pod={p}
                    onPick={() => pickPod(p)}
                    highlight={row.index === 0 && filter.length > 0}
                  />
                </div>
              );
            })}
          </div>
        )}
      </div>
    </div>
  );
}

function PodRow({
  pod,
  onPick,
  highlight,
}: {
  pod: Pod;
  onPick: () => void;
  highlight: boolean;
}) {
  return (
    <button
      type="button"
      onClick={onPick}
      className={cn(
        "flex h-7 w-full items-center gap-2 px-3 font-mono text-[11.5px] text-ink-muted transition-colors",
        "hover:bg-accent-soft hover:text-accent",
        highlight && "bg-surface/60",
      )}
    >
      <span
        aria-hidden
        className={cn("block size-1.5 shrink-0 rounded-full", phaseTone(pod.phase))}
      />
      <span className="min-w-0 flex-1 truncate text-left text-ink">
        {pod.name}
      </span>
      <span className="shrink-0 text-ink-faint">{pod.namespace}</span>
      <span className="shrink-0 text-[10.5px] uppercase tracking-[0.06em] text-ink-faint">
        {pod.phase}
      </span>
    </button>
  );
}

function Selector({
  label,
  value,
  onChange,
  options,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  options: { value: string; label: string }[];
}) {
  return (
    <label className="flex items-center gap-1.5">
      <span className="uppercase tracking-[0.06em] text-ink-faint">
        {label}
      </span>
      <span className="relative flex">
        <select
          value={value}
          onChange={(e) => onChange(e.target.value)}
          className="appearance-none rounded-md border border-border bg-surface py-1 pl-2 pr-6 font-mono text-[11px] text-ink hover:border-border-strong focus:border-border-strong focus:outline-none"
        >
          {options.map((o) => (
            <option key={o.value} value={o.value}>
              {o.label}
            </option>
          ))}
        </select>
        <svg
          width="9"
          height="9"
          viewBox="0 0 9 9"
          className="pointer-events-none absolute right-1.5 top-1/2 -translate-y-1/2 text-ink-faint"
          aria-hidden
        >
          <path
            d="M2 3.5l2.5 2.5L7 3.5"
            stroke="currentColor"
            strokeWidth="1.3"
            fill="none"
            strokeLinecap="round"
            strokeLinejoin="round"
          />
        </svg>
      </span>
    </label>
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
        "flex h-full items-center justify-center px-4 py-6 font-mono text-[11.5px]",
        tone === "red" ? "text-red" : "text-ink-faint",
      )}
    >
      {children}
    </div>
  );
}
