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
