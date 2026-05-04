// conflictPolicy — classifies 409 FieldManagerConflict responses so that
// targeted, single-field actions (scale, label edit, etc.) can decide
// whether it's safe to auto-retry with SSA Force=true.
//
// The rule we encode here mirrors the consequences already documented in
// lib/managers.ts:
//
//   HUMAN  / UNKNOWN   — safe to take ownership. kubectl-client-side-apply
//                        in particular is the canonical case ("first time
//                        installing periscope on a cluster managed via
//                        kubectl/Rancher"); the registry literally marks
//                        it as "Generally safe to take".
//   GITOPS / HELM      — unsafe to force. Flux/Argo/Helm will revert the
//                        change on the next reconcile; auto-forcing would
//                        give the user a green toast for a change that
//                        silently disappears minutes later.
//   CONTROLLER         — unsafe to force. HPA, deployment-controller, etc.
//                        run continuously; the value will be overwritten
//                        within seconds.
//   PERISCOPE          — never appears in a 409 against periscope-spa
//                        (we'd be conflicting with ourselves). Treated as
//                        safe for completeness.
//
// The 409 body shape is the standard apiserver Status:
//   {
//     details: {
//       causes: [
//         { reason: "FieldManagerConflict",
//           message: "conflict with \"kubectl-client-side-apply\" using ...",
//           field: ".spec.replicas" }
//       ]
//     }
//   }
//
// The manager name lives inside `message` (not in any structured field),
// so we parse it out with the same regex YamlEditor uses for the manual
// conflict-resolution view.
//
// This module is dependency-free aside from managers.ts so it stays
// trivially testable. Hook code that uses it lives in
// hooks/mutations/_applyWithLenientConflict.ts.

import { ApiError } from "./api";
import { classifyManager, type ManagerCategory, type ManagerInfo } from "./managers";

export interface FieldConflictCause {
  /** Normalised dotted path the apiserver reported, e.g. "spec.replicas". */
  field: string;
  /** Manager name extracted from the cause message, e.g. "kubectl-client-side-apply". */
  manager: ManagerInfo;
}

export interface ConflictAnalysis {
  causes: FieldConflictCause[];
  /** True when every conflicting manager is in a category we consider safe
   *  to take ownership of via SSA force. */
  allSafeToTakeover: boolean;
  /** First cause whose manager is in a non-safe category. Used to surface
   *  the registry's `consequence`/`prefer` text in error toasts. */
  firstBlocking?: FieldConflictCause;
}

const SAFE_CATEGORIES: ReadonlySet<ManagerCategory> = new Set([
  "HUMAN",
  "UNKNOWN",
  "PERISCOPE",
]);

/**
 * analyzeConflict inspects an error and, if it's a 409 carrying parseable
 * FieldManagerConflict causes, returns the classified analysis. Returns
 * null for any error that isn't a recognisable field-manager conflict —
 * callers should propagate those untouched.
 */
export function analyzeConflict(err: unknown): ConflictAnalysis | null {
  if (!(err instanceof ApiError)) return null;
  if (err.status !== 409) return null;
  const causes = parseFieldManagerCauses(err.bodyText);
  if (causes.length === 0) return null;

  let firstBlocking: FieldConflictCause | undefined;
  for (const c of causes) {
    if (!SAFE_CATEGORIES.has(c.manager.category)) {
      firstBlocking = c;
      break;
    }
  }
  return {
    causes,
    allSafeToTakeover: firstBlocking === undefined,
    firstBlocking,
  };
}

/**
 * formatBlockingMessage renders the manager-aware error string used in
 * toasts when a conflict can't be auto-forced. Format:
 *   "<action> blocked by <manager> on <field>: <consequence>[ — <prefer>]"
 *
 * Example:
 *   "scale blocked by kustomize-controller on spec.replicas:
 *    Flux will revert your change on the next reconcile (typically <5 min).
 *    Edit the source repo (the Kustomization in Git) instead of forcing here."
 */
export function formatBlockingMessage(
  actionPhrase: string,
  cause: FieldConflictCause,
): string {
  const m = cause.manager;
  const head = `${actionPhrase} blocked by ${m.display} on ${cause.field}`;
  const tail = m.prefer ? `${m.consequence} ${m.prefer}` : m.consequence;
  return `${head}: ${tail}`;
}

// --- internals --------------------------------------------------------------

interface RawCause {
  reason?: string;
  message?: string;
  field?: string;
}

const MANAGER_RE = /conflict with "([^"]+)"/;

function parseFieldManagerCauses(bodyText: string | undefined): FieldConflictCause[] {
  if (!bodyText) return [];
  let parsed: unknown;
  try {
    parsed = JSON.parse(bodyText);
  } catch {
    return [];
  }
  const status = parsed as { details?: { causes?: RawCause[] } };
  const raw = status?.details?.causes;
  if (!Array.isArray(raw)) return [];

  const out: FieldConflictCause[] = [];
  for (const cause of raw) {
    if (cause.reason !== "FieldManagerConflict") continue;
    const m = (cause.message ?? "").match(MANAGER_RE);
    if (!m) continue;
    out.push({
      field: normalizeFieldPath(cause.field ?? ""),
      manager: classifyManager(m[1]),
    });
  }
  return out;
}

// normalizeFieldPath strips the leading "." apiserver returns and unquotes
// keyed selectors so paths are comparable across apiserver versions. This
// is intentionally a near-duplicate of managedFields.ts:normalizeStatusFieldPath
// — keeping conflictPolicy dependency-free of managedFields keeps the
// import graph shallow (managedFields pulls in YAML helpers we don't need).
function normalizeFieldPath(field: string): string {
  let p = field.trim();
  if (p.startsWith(".")) p = p.slice(1);
  p = p.replace(/\[(\w+)=["']([^"']+)["']\]/g, "[$1=$2]");
  return p;
}
