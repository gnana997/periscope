// AuthContext primitive — lives in its own file so React Refresh can
// hot-reload <AuthProvider> without losing context identity, and so
// the eslint react-refresh/only-export-components rule is satisfied
// on AuthContext.tsx (which now only exports the Provider component).

import { createContext } from "react";
import type { AuthUser } from "./types";

export interface AuthContextValue {
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

export const AuthContext = createContext<AuthContextValue | null>(null);
