import { useCallback, useEffect, useState } from "react";
import { NavLink, useLocation, useParams } from "react-router-dom";
import { cn } from "../../lib/cn";
import { RESOURCE_GROUPS, resourcesByGroup } from "../../lib/resources";
import type { ResourceGroup } from "../../lib/resources";

const STORAGE_KEY = "periscope.sidebar.openGroups";
const DEFAULT_OPEN: ResourceGroup[] = ["Cluster"];

function readOpenGroups(): Set<ResourceGroup> {
  try {
    const stored = localStorage.getItem(STORAGE_KEY);
    if (stored) {
      const parsed = JSON.parse(stored);
      if (Array.isArray(parsed)) return new Set(parsed as ResourceGroup[]);
    }
  } catch {
    // ignore
  }
  return new Set(DEFAULT_OPEN);
}

function groupForPath(pathname: string): ResourceGroup | null {
  for (const group of RESOURCE_GROUPS) {
    for (const r of resourcesByGroup(group)) {
      // match /clusters/:cluster/<resource>
      if (pathname.includes(`/${r.id}`)) return group;
    }
  }
  return null;
}

export function ResourceNav() {
  const { cluster } = useParams();
  const location = useLocation();
  const params = new URLSearchParams(location.search);
  const ns = params.get("ns");
  const linkSearch = ns ? `?ns=${encodeURIComponent(ns)}` : "";

  const [openGroups, setOpenGroups] = useState<Set<ResourceGroup>>(readOpenGroups);

  // Auto-expand the group of the active route when navigating
  useEffect(() => {
    const active = groupForPath(location.pathname);
    if (active) {
      setOpenGroups((prev) => {
        if (prev.has(active)) return prev;
        return new Set([...prev, active]);
      });
    }
  }, [location.pathname]);

  // Persist open state
  useEffect(() => {
    localStorage.setItem(STORAGE_KEY, JSON.stringify([...openGroups]));
  }, [openGroups]);

  const toggleGroup = useCallback((group: ResourceGroup) => {
    setOpenGroups((prev) => {
      const next = new Set(prev);
      if (next.has(group)) next.delete(group);
      else next.add(group);
      return next;
    });
  }, []);

  return (
    <nav className="flex-1 overflow-y-auto px-2 py-2">
      {RESOURCE_GROUPS.map((group) => {
        const isOpen = openGroups.has(group);
        const items = resourcesByGroup(group);
        return (
          <div key={group} className="mb-0.5 last:mb-0">
            {/* Group header / toggle */}
            <button
              type="button"
              onClick={() => toggleGroup(group)}
              className="flex w-full items-center gap-1.5 rounded-sm px-3 py-1.5 text-left transition-colors hover:bg-surface-2/50"
            >
              <svg
                width="11"
                height="11"
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
              <span className="text-[10px] font-medium uppercase tracking-[0.08em] text-ink-faint">
                {group}
              </span>
            </button>

            {/* Collapsible items — grid trick for smooth height animation */}
            <div
              className={cn(
                "grid transition-all duration-200 ease-in-out",
                isOpen ? "grid-rows-[1fr]" : "grid-rows-[0fr]",
              )}
            >
              <ul className="overflow-hidden">
                {items.map((r) => {
                  if (!r.ready) {
                    return (
                      <li key={r.id}>
                        <div
                          className="flex cursor-not-allowed items-center gap-2 rounded-sm px-3 py-1.5 text-[12.5px] text-ink-faint"
                          aria-disabled
                        >
                          <span className="block size-1 rounded-full bg-transparent" />
                          <span className="flex-1">{r.label}</span>
                          <SoonBadge />
                        </div>
                      </li>
                    );
                  }
                  return (
                    <li key={r.id}>
                      <NavLink
                        to={`/clusters/${cluster ?? "_"}/${r.id}${linkSearch}`}
                        className={({ isActive }) =>
                          cn(
                            "flex items-center gap-2 rounded-sm px-3 py-1.5 text-[12.5px] transition-colors",
                            isActive
                              ? "bg-accent-soft text-accent"
                              : "text-ink hover:bg-surface-2",
                          )
                        }
                      >
                        {({ isActive }) => (
                          <>
                            <span
                              className={cn(
                                "block size-1 shrink-0 rounded-full",
                                isActive ? "bg-accent" : "bg-transparent",
                              )}
                            />
                            <span className="flex-1">{r.label}</span>
                          </>
                        )}
                      </NavLink>
                    </li>
                  );
                })}
              </ul>
            </div>
          </div>
        );
      })}
    </nav>
  );
}

function SoonBadge() {
  return (
    <span className="rounded-sm border border-border px-1.5 py-px text-[9px] uppercase tracking-[0.06em] text-ink-faint">
      Soon
    </span>
  );
}
