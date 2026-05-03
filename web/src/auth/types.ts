/**
 * AuthUser is the public identity returned by /api/auth/whoami. Tokens
 * never reach the SPA; only this slice does.
 */
export interface AuthUser {
  subject: string;
  email: string;
  groups: string[];
  /** "dev" — no IdP wired; "oidc" — generic OIDC IdP (Auth0/Okta/etc). */
  mode: AuthMode;
  /** Active K8s authorization mode. "shared" / "tier" / "raw" / "". */
  authzMode: AuthzMode;
  /** Resolved tier when authzMode === "tier"; empty otherwise. */
  tier?: AuthzTier;
  /** True when the audit pipeline has SQLite persistence wired. */
  auditEnabled?: boolean;
  /** "self" — user sees only own actions; "all" — audit-admin. */
  auditScope?: "self" | "all";
  expiresAt: number;
}

export type AuthMode = "dev" | "oidc";
export type AuthzMode = "" | "shared" | "tier" | "raw";
export type AuthzTier = "" | "read" | "triage" | "write" | "maintain" | "admin";
