import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import type { AuthUser } from "./types";

interface AuthContextValue {
  user: AuthUser | null;
  isLoading: boolean;
  error: string | null;
  /** Triggers a full-page redirect to /api/auth/login. */
  signIn: () => void;
  /** Local logout: clears Periscope session, leaves Okta session alone. */
  signOut: () => void;
  /** RP-initiated logout: clears Periscope + Okta sessions. */
  signOutEverywhere: () => void;
  /** Re-fetch /api/auth/whoami. */
  refresh: () => Promise<void>;
}

const AuthContext = createContext<AuthContextValue | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<AuthUser | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    setIsLoading(true);
    setError(null);
    try {
      const res = await fetch("/api/auth/whoami", {
        headers: { Accept: "application/json" },
      });
      if (res.status === 401) {
        setUser(null);
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
      setIsLoading(false);
    }
  }, []);

  useEffect(() => {
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

export function useAuth(): AuthContextValue {
  const v = useContext(AuthContext);
  if (!v) {
    throw new Error("useAuth must be used inside <AuthProvider>");
  }
  return v;
}
