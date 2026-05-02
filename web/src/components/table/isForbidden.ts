// isForbidden — detect whether an error from getJSON represents
// a 403 response. The api wrapper preserves status on ApiError.
//
// Lives separately from states.tsx so that file stays component-only
// (eslint react-refresh/only-export-components).

export function isForbidden(err: unknown): boolean {
  if (!err || typeof err !== "object") return false;
  const status = (err as { status?: unknown }).status;
  return status === 403;
}
