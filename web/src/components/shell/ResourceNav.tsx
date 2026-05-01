import { NavLink, useLocation, useParams } from "react-router-dom";
import { RESOURCE_GROUPS, resourcesByGroup } from "../../lib/resources";
import { cn } from "../../lib/cn";

/**
 * Persisting `?ns` across resource switches lets the global namespace
 * picker behave like a sticky context. Per-page params (q, status, sel)
 * are dropped on switch — those are scoped to the resource being viewed.
 */
export function ResourceNav() {
  const { cluster } = useParams();
  const location = useLocation();
  const params = new URLSearchParams(location.search);
  const ns = params.get("ns");
  const linkSearch = ns ? `?ns=${encodeURIComponent(ns)}` : "";

  return (
    <nav className="flex-1 overflow-y-auto px-2 py-2">
      {RESOURCE_GROUPS.map((group) => (
        <div key={group} className="mb-3 last:mb-0">
          <div className="px-3 pb-1 pt-2 text-[10px] font-medium uppercase tracking-[0.08em] text-ink-faint">
            {group}
          </div>
          <ul>
            {resourcesByGroup(group).map((r) => {
              if (!r.ready) {
                return (
                  <li key={r.id}>
                    <div
                      className="flex cursor-not-allowed items-center gap-2 rounded-sm px-3 py-1.5 text-[13px] text-ink-faint"
                      aria-disabled
                    >
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
                        "flex items-center gap-2 rounded-sm px-3 py-1.5 text-[13px] transition-colors",
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
                            "block size-1 rounded-full",
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
      ))}
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
