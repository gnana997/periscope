// useExecSessions — consumer hook for the ExecSessions context.
// Lives separately from ExecSessionsContext.tsx so React Refresh keeps
// that file component-only (eslint react-refresh/only-export-components).

import { use } from "react";
import { ExecSessionsCtx, type ExecSessionsContextValue } from "./ExecSessionsContext";

export function useExecSessions(): ExecSessionsContextValue {
  // React 19's `use()` reads context with the same semantics as
  // useContext but is allowed inside conditionals and loops, so it's
  // the forward-looking choice for new code (RFC 0001 #7 and the
  // react-doctor recommendation).
  const v = use(ExecSessionsCtx);
  if (!v) {
    throw new Error("useExecSessions must be used within ExecSessionsProvider");
  }
  return v;
}
