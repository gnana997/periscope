// lazyNamed — React.lazy for modules that use NAMED exports.
//
// React.lazy expects a module with a `default` export, but Periscope
// pages and components consistently use named exports. lazyNamed
// resolves the named export and re-shapes it into the {default} module
// React.lazy expects, so we don't have to add a default export to
// every file just to enable code splitting.
//
// Usage:
//
//   const PodsPage = lazyNamed(() => import("../pages/PodsPage"), "PodsPage");
//
// Type-safe: the second arg must be a key of the dynamically-imported
// module's shape, and the resulting component preserves the original's
// prop types.

import { lazy, type ComponentType } from "react";

export function lazyNamed<
  // ComponentType<any> is intentional: it lets the inferred M[K] preserve
  // each page's specific prop type. Narrowing to <unknown> would force
  // every consumer (e.g. WithCluster<{cluster: string}>) to widen its prop type.
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  M extends Record<string, ComponentType<any>>,
  K extends keyof M,
>(loader: () => Promise<M>, name: K): M[K] {
  // The cast is safe: React.lazy returns a LazyExoticComponent that
  // renders M[K], which is structurally identical to M[K] for the
  // call-site's purposes (JSX render, props).
  return lazy(() =>
    loader().then((mod) => ({ default: mod[name] })),
  ) as unknown as M[K];
}
