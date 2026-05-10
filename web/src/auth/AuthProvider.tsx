import {
  useCallback,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import type { AuthUser } from "./types";
import { AuthContext, type AuthContextValue } from "./authContext";

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<AuthUser | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    setIsLoading(true);
    setError(null);
    // When true, we're navigating away (silent-SSO attempt). Skip the
    // setIsLoading(false) at the bottom so the spinner stays up
    // through the redirect instead of flashing <LoginScreen />.
    let navigating = false;
    try {
      const res = await fetch("/api/auth/whoami", {
        headers: { Accept: "application/json" },
      });
      if (res.status === 401) {
        setUser(null);
        // The backend's LogoutHandler redirects to /?signedOut=1 so
        // the SPA can distinguish "user just signed out" from "no
        // session, try silent SSO". Without this flag we'd kick off
        // /api/auth/login on every unauthenticated mount and Auth0's
        // still-valid SSO session would re-authenticate immediately,
        // looping the logout. Strip the param after reading so a
        // refresh doesn't keep blocking auto-SSO.
        const params = new URLSearchParams(window.location.search);
        if (params.has("signedOut")) {
          params.delete("signedOut");
          const qs = params.toString();
          const next = window.location.pathname + (qs ? `?${qs}` : "") + window.location.hash;
          window.history.replaceState(null, "", next);
          return;
        }
        navigating = true;
        window.location.replace("/api/auth/login");
        return;
      }
      if (!res.ok) {
        throw new Error(`whoami: ${res.status} ${res.statusText}`);
      }
      const u = (await res.json()) as AuthUser;
      setUser(u);
    } catch (e) {
      setError((e as Error).message);
      setUser(null);
    } finally {
      if (!navigating) setIsLoading(false);
    }
  }, []);

  // Fetch /whoami once on mount. refresh() calls setState internally;
  // this is the canonical "fetch on mount" pattern and there's no
  // cleaner alternative in current React (a `<Suspense>`-driven
  // approach would require lifting auth fetch into a
  // useSuspenseQuery, a non-trivial refactor).
  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect
    void refresh();
  }, [refresh]);

  // Background-tab keepalive. While the tab is hidden, the list-page
  // pollers (refetchIntervalInBackground: false) stop firing, so the
  // server-side session idle timer (default 30m) reaches expiry and
  // the next foreground action 401s. We send a single lightweight
  // /api/auth/whoami every 10 min when document.hidden — that hits
  // the auth middleware and bumps LastActivity, keeping the session
  // alive without re-enabling expensive background list polling.
  //
  // Foreground tabs bump LastActivity organically through the existing
  // list-endpoint pollers, so we deliberately gate on document.hidden
  // — no need to double-ping when foreground polling is doing the job.
  //
  // 10 min vs 30 min idle window leaves a comfortable safety margin
  // against browser-side timer throttling (Chrome / Firefox throttle
  // hidden-tab setInterval to ≤1/min, never below).
  useEffect(() => {
    if (!user) return;
    const id = window.setInterval(() => {
      if (!document.hidden) return;
      // Soft-fail: transient network errors here just delay the next
      // bump. If the session has already expired, the user's next
      // foreground action will surface that — we don't try to
      // resurrect from background.
      fetch("/api/auth/whoami", {
        credentials: "include",
        headers: { Accept: "application/json" },
      }).catch(() => {});
    }, 10 * 60 * 1000);
    return () => window.clearInterval(id);
  }, [user]);

  const signIn = useCallback(() => {
    window.location.href = "/api/auth/login";
  }, []);

  const signOut = useCallback(() => {
    window.location.href = "/api/auth/logout";
  }, []);

  const signOutEverywhere = useCallback(() => {
    window.location.href = "/api/auth/logout/everywhere";
  }, []);

  const value = useMemo<AuthContextValue>(
    () => ({ user, isLoading, error, signIn, signOut, signOutEverywhere, refresh }),
    [user, isLoading, error, signIn, signOut, signOutEverywhere, refresh],
  );

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}
