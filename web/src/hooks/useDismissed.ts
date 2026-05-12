import { useCallback, useState } from "react";

/**
 * useDismissed — per-session, per-key boolean stored in sessionStorage.
 *
 * Returns `[dismissed, dismiss]`. Initialised from sessionStorage on
 * mount. `dismiss()` flips the flag AND writes it back. The flag
 * naturally clears when the browser tab closes (sessionStorage scope)
 * — opening the same resource in a fresh tab re-prompts.
 *
 * Used by CoManagementBanner so a quiet ack doesn't bury the
 * "this resource is co-managed by X" message forever — only for the
 * current session.
 */
export function useDismissed(key: string): [boolean, () => void] {
  const [dismissed, setDismissed] = useState<boolean>(() => readFlag(key));

  const dismiss = useCallback(() => {
    setDismissed(true);
    try {
      window.sessionStorage.setItem(key, "1");
    } catch {
      // sessionStorage unavailable (Safari private mode pre-15, SSR)
      // — keep the in-memory state, accept that a reload would re-prompt.
    }
  }, [key]);

  return [dismissed, dismiss];
}

function readFlag(key: string): boolean {
  if (typeof window === "undefined") return false;
  try {
    return window.sessionStorage.getItem(key) === "1";
  } catch {
    return false;
  }
}
