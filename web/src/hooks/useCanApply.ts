// useCanApply — gates the Apply YAML entry points (Sidebar button +
// Cmd+K quick action) on whether the operator has any apply permission
// in the cluster.
//
// We test three representative kinds (configmaps / deployments /
// namespaces) and pass when ANY of them is allowed. Reasoning:
//
//   - configmaps: the lowest-bar write op; broadly granted to
//     anything ≥ edit tier. Catches most "operator with namespaced
//     write" cases.
//   - deployments: catches operators with workload-write but no
//     core/v1 access (rare but possible with custom RBAC).
//   - namespaces: cluster-scoped check; catches admin-tier operators
//     who'd legitimately use Apply YAML to create a namespace.
//
// Three checks land in one POST via useCanIBatch, cached for 30s.
// Per-doc RBAC pre-flight (sub-task #55) is the real gate; this is
// just for "show the button at all".

import { useMemo } from "react";
import { useCanIBatch, type CanICheck } from "./useCanI";

const APPLY_PROBES: CanICheck[] = [
  { verb: "create", resource: "configmaps" },
  { verb: "create", resource: "deployments", group: "apps" },
  { verb: "create", resource: "namespaces" },
];

export interface CanApplyDecision {
  allowed: boolean;
  loading: boolean;
}

export function useCanApply(cluster: string): CanApplyDecision {
  const decisions = useCanIBatch(cluster, APPLY_PROBES);
  return useMemo(() => {
    const loading = decisions.some((d) => d.loading);
    const allowed = decisions.some((d) => d.allowed);
    return { allowed, loading };
  }, [decisions]);
}
