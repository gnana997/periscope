// GlobalFetchingBar — 2px indeterminate sweep at the top of the main
// content region whenever a background refetch is in flight. Filtered
// to queries that already have data (predicate), so cold loads keep
// rendering their per-page skeletons without a doubled-up indicator.
//
// Mounted as the first child of <main> in AppShell so it sits above
// the page content but below the global sidebar — implying the
// content is refreshing, not the static nav.
//
// Animation: see `.animate-fetchbar` in src/index.css. Single keyframe
// + a custom utility — no Tailwind config (we're on v4 CSS-mode) and
// no new dependency.

import { useIsFetching } from "@tanstack/react-query";

export function GlobalFetchingBar() {
  const fetching = useIsFetching({
    predicate: (q) => q.state.data !== undefined,
  });
  if (fetching === 0) return null;
  return (
    <div
      role="progressbar"
      aria-busy="true"
      aria-label="Refreshing data"
      className="h-0.5 w-full overflow-hidden bg-transparent"
    >
      <div className="h-full w-1/3 animate-fetchbar bg-accent" />
    </div>
  );
}
