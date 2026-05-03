// canIReason — composes the tooltip body shown on disabled action
// buttons. Three sources, picked in order:
//
//   1. The apiserver's own `Status.Reason` from the SAR/SSRR (best —
//      it tells the user exactly which RBAC binding denied them, but
//      is often empty in practice because the apiserver only fills
//      it for a handful of authorizer paths).
//
//   2. A mode-aware default that mentions the user's tier when we're
//      in tier mode ("your tier (`triage`) cannot delete pods"). This
//      is the legibility win — the SPA reflects the tier model
//      instead of leaving the user guessing.
//
//   3. A generic per-verb fallback when even the mode/tier are
//      unknown (loading, anonymous, dev-mode).
//
// Kept independent of React so it's trivially unit-testable later.

import type { CanICheck, CanIResult } from "./api";
import type { AuthzMode, AuthzTier } from "../auth/types";

// Map verbs to a human verb phrase.
const verbPhrase: Record<CanICheck["verb"], string> = {
  get: "view",
  list: "list",
  watch: "watch",
  create: "create",
  update: "update",
  patch: "edit",
  delete: "delete",
};

// Hide internal-looking impersonation prefixes in tier-mode messages.
function prettyTier(tier: AuthzTier | undefined): string {
  return tier ? tier : "";
}

interface FormatArgs {
  /** The apiserver's response for this single check. */
  result: CanIResult;
  /** The check the user asked about — used for verb/resource phrasing. */
  check: CanICheck;
  /** From useAuth().user — drives the mode-aware copy. */
  authzMode: AuthzMode | undefined;
  tier: AuthzTier | undefined;
}

/**
 * formatCanIDeniedReason returns the tooltip body for a denied check.
 * Returns an empty string when the check is allowed (the caller
 * shouldn't render a tooltip in that case).
 */
export function formatCanIDeniedReason(args: FormatArgs): string {
  const { result, check, authzMode, tier } = args;
  if (result.allowed) return "";

  // Apiserver-supplied reason wins when present and non-trivial. The
  // backend forwards the SAR's Status.Reason untouched. Some classified
  // failures ("apiserver_unreachable", "auth_failed", "denied") are
  // codes the SPA can specialise on.
  const r = (result.reason || "").trim();
  if (r === "apiserver_unreachable" || r === "timeout") {
    return "Cannot check permission right now (cluster unreachable). Try again in a moment.";
  }
  if (r === "auth_failed") {
    return "Your session expired. Reload the page to sign in again.";
  }
  if (r === "unauthenticated") {
    return "Sign in to perform this action.";
  }
  if (r && r !== "denied") {
    // Pass through the apiserver's own explanation when it's
    // substantive (e.g. "RBAC: forbidden: User ...").
    return r;
  }

  const action = `${verbPhrase[check.verb] ?? check.verb} ${check.resource}`;
  const where = check.namespace ? ` in ${check.namespace}` : "";

  switch (authzMode) {
    case "tier": {
      const t = prettyTier(tier);
      if (t) {
        return `Your tier (${t}) cannot ${action}${where}. Contact your platform team.`;
      }
      return `Your tier doesn't allow you to ${action}${where}.`;
    }
    case "raw":
      return `You don't have permission to ${action}${where}.`;
    case "shared":
      return `The dashboard's K8s role doesn't allow ${action}${where}.`;
    default:
      // Empty authzMode (loading, dev mode, etc.). Generic.
      return `You don't have permission to ${action}${where}.`;
  }
}
