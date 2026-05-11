// Pure-logic helpers for DetailOverlay. Kept in a sibling module so
// the component file can stay a clean component-only export
// (react-refresh/only-export-components rule). All exports here are
// pure, side-effect-free (modulo `activeElementIsEditable` reading
// document.activeElement), and unit-tested in DetailOverlay.test.ts.

/** clampWidth bounds a stored width to [min, runtime max].
 *  Runtime max is the smaller of `max` and 95% of containerWidth. */
export function clampWidth(
  value: number,
  min: number,
  max: number,
  containerWidth: number,
): number {
  const upper = Math.min(max, Math.floor(containerWidth * 0.95));
  if (value < min) return min;
  if (value > upper) return upper;
  return value;
}

/** isNarrowViewport — drawer collapses to full width below this. */
export function isNarrowViewport(
  containerWidth: number,
  initial: number,
): boolean {
  return containerWidth > 0 && containerWidth < initial + 180;
}

/** activeElementIsEditable — returns true when the focused element
 *  is something the user types into. Used to yield keyboard handling
 *  to inputs / textareas / contenteditable. */
export function activeElementIsEditable(): boolean {
  if (typeof document === "undefined") return false;
  const el = document.activeElement as HTMLElement | null;
  if (!el) return false;
  const tag = el.tagName;
  if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT") return true;
  if (el.isContentEditable) return true;
  return false;
}

/** chooseHandler picks which callback (if any) a keyboard event
 *  should fire. Returns null when the event should be ignored. */
export function chooseHandler(
  key: string,
  callbacks: {
    onDismiss?: () => void;
    onPrev?: () => void;
    onNext?: () => void;
  },
): (() => void) | null {
  if (key === "Escape") return callbacks.onDismiss ?? null;
  if (key === "j" || key === "ArrowDown") return callbacks.onNext ?? null;
  if (key === "k" || key === "ArrowUp") return callbacks.onPrev ?? null;
  return null;
}

/** pickNeighbors finds the previous and next entries adjacent to the
 *  current selection in a row array. Returns `null` for either side
 *  when the current row is at the corresponding boundary, or when
 *  the current row isn't in the list at all. */
export function pickNeighbors<T>(
  rows: readonly T[],
  isCurrent: (row: T) => boolean,
): { prev: T | null; next: T | null } {
  const idx = rows.findIndex(isCurrent);
  return {
    prev: idx > 0 ? rows[idx - 1] : null,
    next: idx >= 0 && idx < rows.length - 1 ? rows[idx + 1] : null,
  };
}

export interface OverlayNavCallbacks {
  onDismiss?: () => void;
  onPrev?: () => void;
  onNext?: () => void;
}

/** buildOverlayNav assembles the three pre-wrapped callbacks pages
 *  pass into DetailOverlay. Pure function — the page is responsible
 *  for already wrapping `navigateTo` and `dismiss` with
 *  `confirmDiscard` so the dirty-state gate fires on Esc + j/k just
 *  like it does on row clicks.
 *
 *  When `selectedKey` is null (no row is selected), returns an empty
 *  object so the overlay's listeners stay no-ops. */
export function buildOverlayNav<T>(args: {
  rows: readonly T[];
  selectedKey: string | null;
  keyOf: (row: T) => string;
  navigateTo: (row: T) => void;
  dismiss: () => void;
}): OverlayNavCallbacks {
  if (args.selectedKey === null) return {};
  const { prev, next } = pickNeighbors(
    args.rows,
    (r) => args.keyOf(r) === args.selectedKey,
  );
  return {
    onDismiss: args.dismiss,
    onPrev: prev ? () => args.navigateTo(prev) : undefined,
    onNext: next ? () => args.navigateTo(next) : undefined,
  };
}
