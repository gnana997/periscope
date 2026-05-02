// useAuth — consumer hook for the auth context. Lives in a separate
// file from <AuthProvider> so HMR/React Refresh treats AuthContext.tsx
// as a component-only file (eslint react-refresh/only-export-components).

import { useContext } from "react";
import { AuthContext, type AuthContextValue } from "./AuthContext";

export function useAuth(): AuthContextValue {
  const v = useContext(AuthContext);
  if (!v) {
    throw new Error("useAuth must be used inside <AuthProvider>");
  }
  return v;
}
