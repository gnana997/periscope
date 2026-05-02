// useCanI — gates write/delete actions in the SPA.
//
// In v1 this is a no-op stub that always returns true: the BACKEND is
// the authoritative gate (impersonated K8s RBAC). The SPA shows the
// action button regardless of the user's tier; if the action would be
// rejected, the user sees the existing 403 → ForbiddenState UX after
// they click. This is the deliberate decision discussed in PR-D — it
// works identically in shared / tier / raw authorization modes because
// it doesn't rely on Periscope-side knowledge of what each user can do.
//
// In v1.x the hook body will be replaced with caching backed by
// SelfSubjectAccessReview / SelfSubjectRulesReview against the apiserver
// under the impersonated identity. Every call site already calls this
// hook, so the upgrade is invisible — no refactor at the action sites.
//
// The hook intentionally has the same signature as the future SSAR
// implementation will need: a verb, a resource (plural form, matching
// the URL segment), and an optional namespace. Don't widen the surface
// here unless the future implementation actually needs more.
export interface CanIArgs {
  verb: "update" | "delete" | "get" | "create" | "patch";
  resource: string; // plural, e.g. "pods", "deployments", "secrets"
  namespace?: string;
}

export function useCanI(_args: CanIArgs): boolean {
  return true;
}
