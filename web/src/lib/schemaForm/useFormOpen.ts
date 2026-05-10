// schemaForm/useFormOpen.ts — open-state hook for the form's
// collapsible sections, sub-sections, and array-of-objects rows.
//
// Model:
//   - Every collapsible element has a stable string id (e.g.
//     `section.primary`, `row.spec.template.spec.containers[0]`,
//     `subsection.spec.template.spec.containers[0].probes`).
//   - State carries a single `mode` ("default-closed" | "default-open")
//     plus a Set<string> of `overrides`. An element is open iff:
//       mode === "default-open" XOR overrides.has(id)
//     i.e. overrides flip the default for clicked elements.
//   - Toggling a single element flips its membership in `overrides`.
//   - Expand-all switches mode to "default-open" and clears overrides.
//   - Collapse-all switches mode to "default-closed" and clears overrides.
//
// LocalStorage persistence:
//   When `formKey` is provided, the FIRST L1 section the user opens
//   from a fresh-default-closed state gets remembered. On next mount
//   that id is restored as a single-element override (so the form
//   opens with that section already expanded). Only the most-recent
//   single click is remembered — to keep "default-closed" the
//   genuine zero state.

import { createContext, useCallback, useContext, useMemo, useRef, useState } from "react";

export type OpenMode = "default-closed" | "default-open";

export interface FormOpenApi {
  /** True iff the element with the given id should render open. */
  isOpen: (id: string) => boolean;
  /** Flip the open state of a single element. */
  toggle: (id: string) => void;
  /** Set every element to open (mode=open, clear overrides). */
  expandAll: () => void;
  /** Set every element to closed (mode=closed, clear overrides). */
  collapseAll: () => void;
  /** Coarse "are we entirely default-closed with no overrides?" — the
   *  collapse-all button can disable itself when this is true. */
  isAllCollapsed: boolean;
  /** Coarse "are we entirely default-open with no overrides?" — the
   *  expand-all button can disable itself when this is true. */
  isAllExpanded: boolean;
}

interface State {
  mode: OpenMode;
  overrides: Set<string>;
}

const STORAGE_PREFIX = "periscope.form.lastOpened.";

export function useFormOpen(formKey?: string): FormOpenApi {
  // Restore last-opened L1 section from localStorage on mount.
  const initial = useMemo<State>(() => {
    if (!formKey || typeof window === "undefined") {
      return { mode: "default-closed", overrides: new Set() };
    }
    try {
      const stored = window.localStorage.getItem(STORAGE_PREFIX + formKey);
      if (stored) {
        return { mode: "default-closed", overrides: new Set([stored]) };
      }
    } catch {
      /* localStorage unavailable (Safari private mode, etc.) — fall
       * through to fresh-closed. */
    }
    return { mode: "default-closed", overrides: new Set() };
  }, [formKey]);

  const [state, setState] = useState<State>(initial);
  // Track the previous overrides set to detect "first L1 click" for
  // localStorage persistence. We only remember single L1 clicks —
  // L2 sub-section clicks and row toggles aren't worth persisting
  // (would be noise in the restore).
  const prevOverridesRef = useRef<Set<string>>(initial.overrides);

  const persistIfFirstL1Click = useCallback(
    (next: Set<string>) => {
      if (!formKey || typeof window === "undefined") return;
      // Only persist if going from empty → 1 entry AND that entry
      // is an L1 section.
      if (
        prevOverridesRef.current.size === 0 &&
        next.size === 1
      ) {
        const onlyId = next.values().next().value as string;
        if (onlyId.startsWith("section.")) {
          try {
            window.localStorage.setItem(STORAGE_PREFIX + formKey, onlyId);
          } catch {
            /* ignore */
          }
        }
      }
      prevOverridesRef.current = next;
    },
    [formKey],
  );

  const isOpen = useCallback(
    (id: string) => {
      const overridden = state.overrides.has(id);
      return state.mode === "default-open" ? !overridden : overridden;
    },
    [state],
  );

  const toggle = useCallback(
    (id: string) => {
      setState((prev) => {
        const next = new Set(prev.overrides);
        if (next.has(id)) next.delete(id);
        else next.add(id);
        persistIfFirstL1Click(next);
        return { mode: prev.mode, overrides: next };
      });
    },
    [persistIfFirstL1Click],
  );

  const expandAll = useCallback(() => {
    setState({ mode: "default-open", overrides: new Set() });
    prevOverridesRef.current = new Set();
  }, []);

  const collapseAll = useCallback(() => {
    setState({ mode: "default-closed", overrides: new Set() });
    prevOverridesRef.current = new Set();
  }, []);

  const isAllCollapsed = state.mode === "default-closed" && state.overrides.size === 0;
  const isAllExpanded = state.mode === "default-open" && state.overrides.size === 0;

  return { isOpen, toggle, expandAll, collapseAll, isAllCollapsed, isAllExpanded };
}

// FormOpenContext — provides the open-state API to nested
// renderers (ArrayRow, ArrayRowChildren) without prop drilling.
// Lives here rather than in SchemaForm.tsx so the latter only
// exports React components (react-refresh/only-export-components
// rule).


export const FormOpenContext = createContext<FormOpenApi | null>(null);

export function useFormOpenContext(): FormOpenApi | null {
  return useContext(FormOpenContext);
}
