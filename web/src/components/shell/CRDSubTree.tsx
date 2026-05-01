import { useCallback, useEffect, useMemo, useState } from "react";
import { NavLink, useLocation } from "react-router-dom";
import { cn } from "../../lib/cn";
import { useCRDs } from "../../hooks/useResource";
import type { CRD } from "../../lib/types";

/**
 * CRDSubTree — dynamic sidebar tree for the Extensions group.
 *
 * Fetches every CRD installed on the current cluster, groups by API
 * group, and renders each group as its own collapsible section with
 * the CRD kinds as leaf links. Each link routes to the existing
 * CustomResourcesPage with the right group/version/plural slug.
 *
 * Why API group as the grouping axis (and not e.g. kubectl
 * `categories`)? The group field is universally set, kubectl displays
 * the same shape via `kubectl api-resources`, and operators are
 * already conditioned to think in terms of "the cert-manager.io
 * universe" or "the argoproj.io universe." Multi-group operators
 * (Istio: networking.istio.io + security.istio.io + install.istio.io)
 * surface as multiple sections; that mirrors kubectl and is what
 * users expect.
 *
 * Per-group collapse state is persisted in localStorage so collapsing
 * a noisy operator stays collapsed across reloads.
 */

const STORAGE_KEY = "periscope.sidebar.openCRDGroups.v1";

function readOpenGroups(): Set<string> {
  try {
    const stored = localStorage.getItem(STORAGE_KEY);
    if (stored) {
      const parsed = JSON.parse(stored);
      if (Array.isArray(parsed)) return new Set(parsed as string[]);
    }
  } catch {
    // localStorage may be blocked
  }
  return new Set();
}

interface APIGroupSection {
  apiGroup: string;
  crds: CRD[];
}

function groupByAPIGroup(crds: CRD[]): APIGroupSection[] {
  const map = new Map<string, CRD[]>();
  for (const c of crds) {
    const list = map.get(c.group) ?? [];
    list.push(c);
    map.set(c.group, list);
  }
  // Inside each group, sort kinds alphabetically.
  for (const list of map.values()) {
    list.sort((a, b) => a.kind.localeCompare(b.kind));
  }
  return [...map.entries()]
    .map(([apiGroup, crds]) => ({ apiGroup, crds }))
    .sort((a, b) => a.apiGroup.localeCompare(b.apiGroup));
}

export function CRDSubTree({ cluster }: { cluster: string }) {
  const { data, isLoading, isError } = useCRDs(cluster);
  const [openGroups, setOpenGroups] = useState<Set<string>>(readOpenGroups);
  const location = useLocation();

  const groups = useMemo(
    () => groupByAPIGroup(data?.crds ?? []),
    [data?.crds],
  );

  // Auto-expand the API group containing the active route. e.g. when
  // the user lands on /customresources/cert-manager.io/v1/certificates
  // we open cert-manager.io so they can see siblings.
  useEffect(() => {
    const m = location.pathname.match(
      /\/customresources\/([^/]+)\/[^/]+\/[^/]+/,
    );
    if (!m) return;
    const activeGroup = decodeURIComponent(m[1]);
    setOpenGroups((prev) => {
      if (prev.has(activeGroup)) return prev;
      return new Set([...prev, activeGroup]);
    });
  }, [location.pathname]);

  useEffect(() => {
    try {
      localStorage.setItem(STORAGE_KEY, JSON.stringify([...openGroups]));
    } catch {
      // ignore
    }
  }, [openGroups]);

  const toggle = useCallback((apiGroup: string) => {
    setOpenGroups((prev) => {
      const next = new Set(prev);
      if (next.has(apiGroup)) next.delete(apiGroup);
      else next.add(apiGroup);
      return next;
    });
  }, []);

  if (isLoading) {
    return (
      <div className="px-3 py-1 text-[11px] text-ink-faint italic">
        loading CRDs…
      </div>
    );
  }
  if (isError) {
    return (
      <div className="px-3 py-1 text-[11px] text-red">CRDs unavailable</div>
    );
  }
  if (groups.length === 0) {
    return (
      <div className="px-3 py-1 text-[11px] text-ink-faint italic">
        no CRDs installed
      </div>
    );
  }

  return (
    <div className="mt-0.5 space-y-0.5">
      {groups.map((g) => {
        const isOpen = openGroups.has(g.apiGroup);
        return (
          <div key={g.apiGroup}>
            <button
              type="button"
              onClick={() => toggle(g.apiGroup)}
              className="flex w-full items-center gap-1.5 rounded-sm py-1 pl-5 pr-3 text-left transition-colors hover:bg-surface-2/50"
            >
              <svg
                width="9"
                height="9"
                viewBox="0 0 11 11"
                className={cn(
                  "shrink-0 text-ink-faint transition-transform duration-200",
                  isOpen ? "rotate-90" : "rotate-0",
                )}
                aria-hidden
              >
                <path
                  d="M3.5 2l4 3.5-4 3.5"
                  stroke="currentColor"
                  strokeWidth="1.4"
                  strokeLinecap="round"
                  strokeLinejoin="round"
                  fill="none"
                />
              </svg>
              <span
                className="min-w-0 flex-1 truncate font-mono text-[11px] text-ink-muted"
                title={g.apiGroup}
              >
                {g.apiGroup}
              </span>
              <span className="shrink-0 font-mono text-[10px] tabular-nums text-ink-faint">
                {g.crds.length}
              </span>
            </button>

            <div
              className={cn(
                "grid transition-all duration-200 ease-in-out",
                isOpen ? "grid-rows-[1fr]" : "grid-rows-[0fr]",
              )}
            >
              <ul className="overflow-hidden">
                {g.crds.map((crd) => (
                  <li key={crd.name}>
                    <NavLink
                      to={`/clusters/${cluster}/customresources/${encodeURIComponent(crd.group)}/${encodeURIComponent(crd.servedVersion)}/${encodeURIComponent(crd.plural)}`}
                      className={({ isActive }) =>
                        cn(
                          "flex items-center gap-2 rounded-sm py-1 pl-9 pr-3 text-[12px] transition-colors",
                          isActive
                            ? "bg-accent-soft text-accent"
                            : "text-ink hover:bg-surface-2",
                        )
                      }
                      title={`${crd.group}/${crd.servedVersion}/${crd.plural}`}
                    >
                      {({ isActive }) => (
                        <>
                          <span
                            className={cn(
                              "block size-1 shrink-0 rounded-full",
                              isActive ? "bg-accent" : "bg-transparent",
                            )}
                          />
                          <span className="min-w-0 flex-1 truncate">
                            {crd.kind}
                          </span>
                        </>
                      )}
                    </NavLink>
                  </li>
                ))}
              </ul>
            </div>
          </div>
        );
      })}
    </div>
  );
}
