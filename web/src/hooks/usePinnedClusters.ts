import { useCallback, useEffect, useState } from "react";

const STORAGE_KEY = "periscope:pinnedClusters";

function load(): Set<string> {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return new Set();
    const arr = JSON.parse(raw);
    if (!Array.isArray(arr)) return new Set();
    return new Set(arr.filter((x): x is string => typeof x === "string"));
  } catch {
    return new Set();
  }
}

function persist(set: Set<string>) {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify([...set].sort()));
  } catch {
    // localStorage may be unavailable (private mode, quota); silently
    // degrade — pinning becomes per-session.
  }
}

/**
 * usePinnedClusters keeps a localStorage-backed set of cluster names
 * the user has pinned to the top of the Fleet view.
 *
 * Cross-tab sync: listens to the storage event so toggles in another
 * tab show up here without a reload.
 */
export function usePinnedClusters() {
  const [pinned, setPinned] = useState<Set<string>>(() => load());

  useEffect(() => {
    function onStorage(e: StorageEvent) {
      if (e.key === STORAGE_KEY) setPinned(load());
    }
    window.addEventListener("storage", onStorage);
    return () => window.removeEventListener("storage", onStorage);
  }, []);

  const toggle = useCallback((name: string) => {
    setPinned((prev) => {
      const next = new Set(prev);
      if (next.has(name)) next.delete(name);
      else next.add(name);
      persist(next);
      return next;
    });
  }, []);

  const isPinned = useCallback((name: string) => pinned.has(name), [pinned]);

  return { pinned, toggle, isPinned };
}
