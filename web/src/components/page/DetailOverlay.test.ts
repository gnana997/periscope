import { describe, expect, it, vi } from "vitest";
import {
  chooseHandler,
  clampWidth,
  isNarrowViewport,
} from "./detailOverlayHelpers";

// ── clampWidth ─────────────────────────────────────────────────────

describe("clampWidth", () => {
  // Container is wide enough that the runtime ceiling = max.
  const WIDE = 2000; // 95% = 1900, so the explicit max (1600) wins.

  it("returns value unchanged when in range", () => {
    expect(clampWidth(720, 520, 1600, WIDE)).toBe(720);
  });

  it("clamps below min to min", () => {
    expect(clampWidth(400, 520, 1600, WIDE)).toBe(520);
  });

  it("clamps above max to max", () => {
    expect(clampWidth(2000, 520, 1600, WIDE)).toBe(1600);
  });

  it("uses 95% of container as runtime ceiling when narrower than max", () => {
    // Container 1000 → 95% = 950. Max stays 1600. Effective ceiling = 950.
    expect(clampWidth(1200, 520, 1600, 1000)).toBe(950);
  });

  it("floors the 95% ceiling so the value is an integer", () => {
    // Container 1234 → 95% = 1172.3 → floor 1172.
    expect(clampWidth(1500, 520, 1600, 1234)).toBe(1172);
  });
});

// ── isNarrowViewport ───────────────────────────────────────────────

describe("isNarrowViewport", () => {
  it("returns false on first mount when containerWidth is 0", () => {
    // Avoids a flash of narrow-mode before ResizeObserver settles.
    expect(isNarrowViewport(0, 720)).toBe(false);
  });

  it("returns true just below the threshold", () => {
    // initial 720 → threshold 900. 880 is narrow.
    expect(isNarrowViewport(880, 720)).toBe(true);
  });

  it("returns false at the threshold", () => {
    expect(isNarrowViewport(900, 720)).toBe(false);
  });

  it("returns false above the threshold", () => {
    expect(isNarrowViewport(1280, 720)).toBe(false);
  });

  it("threshold scales with initial width", () => {
    // initial 640 → threshold 820.
    expect(isNarrowViewport(800, 640)).toBe(true);
    expect(isNarrowViewport(820, 640)).toBe(false);
  });
});

// ── chooseHandler ──────────────────────────────────────────────────

describe("chooseHandler", () => {
  it("returns onDismiss for Escape", () => {
    const onDismiss = vi.fn();
    const h = chooseHandler("Escape", { onDismiss });
    expect(h).toBe(onDismiss);
  });

  it("returns onNext for j", () => {
    const onNext = vi.fn();
    expect(chooseHandler("j", { onNext })).toBe(onNext);
  });

  it("returns onNext for ArrowDown", () => {
    const onNext = vi.fn();
    expect(chooseHandler("ArrowDown", { onNext })).toBe(onNext);
  });

  it("returns onPrev for k", () => {
    const onPrev = vi.fn();
    expect(chooseHandler("k", { onPrev })).toBe(onPrev);
  });

  it("returns onPrev for ArrowUp", () => {
    const onPrev = vi.fn();
    expect(chooseHandler("ArrowUp", { onPrev })).toBe(onPrev);
  });

  it("returns null when callback is undefined (boundary behavior)", () => {
    // j on the last row — onNext is undefined, so the keystroke
    // is a no-op rather than wrapping or paginating.
    expect(chooseHandler("j", { onPrev: vi.fn() })).toBeNull();
    expect(chooseHandler("ArrowDown", {})).toBeNull();
  });

  it("returns null for unrelated keys", () => {
    expect(
      chooseHandler("h", { onDismiss: vi.fn(), onPrev: vi.fn(), onNext: vi.fn() }),
    ).toBeNull();
    expect(chooseHandler("Enter", { onDismiss: vi.fn() })).toBeNull();
    expect(chooseHandler(" ", { onDismiss: vi.fn() })).toBeNull();
  });
});

// ── pickNeighbors ──────────────────────────────────────────────────

import { buildOverlayNav, pickNeighbors } from "./detailOverlayHelpers";

describe("pickNeighbors", () => {
  const rows = ["a", "b", "c", "d"];

  it("returns null/null when the current row isn't found", () => {
    const r = pickNeighbors(rows, (s) => s === "z");
    expect(r.prev).toBeNull();
    expect(r.next).toBeNull();
  });

  it("returns null prev at the first row", () => {
    const r = pickNeighbors(rows, (s) => s === "a");
    expect(r.prev).toBeNull();
    expect(r.next).toBe("b");
  });

  it("returns null next at the last row", () => {
    const r = pickNeighbors(rows, (s) => s === "d");
    expect(r.prev).toBe("c");
    expect(r.next).toBeNull();
  });

  it("returns both neighbors in the middle", () => {
    const r = pickNeighbors(rows, (s) => s === "b");
    expect(r.prev).toBe("a");
    expect(r.next).toBe("c");
  });

  it("returns null/null for empty rows", () => {
    const r = pickNeighbors([] as string[], () => true);
    expect(r.prev).toBeNull();
    expect(r.next).toBeNull();
  });
});

// ── buildOverlayNav ────────────────────────────────────────────────

describe("buildOverlayNav", () => {
  const rows = [
    { ns: "default", name: "a" },
    { ns: "default", name: "b" },
    { ns: "kube-system", name: "c" },
  ];
  const keyOf = (r: { ns: string; name: string }) => `${r.ns}/${r.name}`;

  it("returns empty object when nothing is selected", () => {
    const nav = buildOverlayNav({
      rows,
      selectedKey: null,
      keyOf,
      navigateTo: vi.fn(),
      dismiss: vi.fn(),
    });
    expect(nav).toEqual({});
  });

  it("wires dismiss, prev, and next at the middle row", () => {
    const navigateTo = vi.fn();
    const dismiss = vi.fn();
    const nav = buildOverlayNav({
      rows,
      selectedKey: "default/b",
      keyOf,
      navigateTo,
      dismiss,
    });
    expect(nav.onDismiss).toBe(dismiss);
    nav.onPrev?.();
    expect(navigateTo).toHaveBeenCalledWith(rows[0]);
    nav.onNext?.();
    expect(navigateTo).toHaveBeenCalledWith(rows[2]);
  });

  it("leaves onPrev undefined at the first row (boundary)", () => {
    const nav = buildOverlayNav({
      rows,
      selectedKey: "default/a",
      keyOf,
      navigateTo: vi.fn(),
      dismiss: vi.fn(),
    });
    expect(nav.onPrev).toBeUndefined();
    expect(nav.onNext).toBeTypeOf("function");
  });

  it("leaves onNext undefined at the last row (boundary)", () => {
    const nav = buildOverlayNav({
      rows,
      selectedKey: "kube-system/c",
      keyOf,
      navigateTo: vi.fn(),
      dismiss: vi.fn(),
    });
    expect(nav.onPrev).toBeTypeOf("function");
    expect(nav.onNext).toBeUndefined();
  });

  it("leaves prev/next undefined when selection isn't in rows", () => {
    const nav = buildOverlayNav({
      rows,
      selectedKey: "default/zzz",
      keyOf,
      navigateTo: vi.fn(),
      dismiss: vi.fn(),
    });
    // Selection set → dismiss is still defined, but cycling is no-op.
    expect(nav.onDismiss).toBeTypeOf("function");
    expect(nav.onPrev).toBeUndefined();
    expect(nav.onNext).toBeUndefined();
  });
});
