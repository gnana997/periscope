// managers — classifier registry for K8s SSA field managers.
//
// Maps manager names (e.g. "kustomize-controller", "kubectl-edit",
// "horizontal-pod-autoscaler") to a category + human-readable
// consequence text. Used by:
//
//   - The conflict-resolution view, to color-code per-field manager
//     badges and warn the user about likely outcomes of force-applying
//     ("Flux will revert your change in ~5 min").
//   - The owner-glyph margin in the editor, to show which fields are
//     owned by which class of manager BEFORE the user even tries to
//     edit them.
//
// The registry is a default + heuristic fallback. Operators can extend
// it later via config (Phase 3 work) but the built-ins cover the
// common K8s ecosystem.

export type ManagerCategory =
  | "GITOPS"      // Flux, ArgoCD — edits are reverted on next reconcile
  | "CONTROLLER"  // HPA, deployment-controller — edits reset by control loop
  | "HUMAN"       // kubectl-edit, kubectl-client-side-apply — another operator
  | "HELM"        // helm-controller, helm-cli — reverts on chart upgrade
  | "PERISCOPE"   // ourselves; never conflicts (this manager wrote the field)
  | "UNKNOWN";

export interface ManagerInfo {
  name: string;             // raw manager string from managedFields
  category: ManagerCategory;
  display: string;          // human-readable name
  source: string;           // controlling system / namespace
  consequence: string;      // what happens if user force-applies over this
  prefer: string;           // suggested better path (often empty)
}

const REGISTRY: Record<string, Omit<ManagerInfo, "name">> = {
  // GitOps — Flux family
  "kustomize-controller": {
    category: "GITOPS",
    display: "kustomize-controller",
    source: "flux-system",
    consequence: "Flux will revert your change on the next reconcile (typically <5 min).",
    prefer: "Edit the source repo (the Kustomization in Git) instead of forcing here.",
  },
  "helm-controller": {
    category: "HELM",
    display: "helm-controller",
    source: "flux-system",
    consequence: "Helm will revert your change on the next chart upgrade or drift correction.",
    prefer: "Update the HelmRelease values in Git, or `helm upgrade` against the source chart.",
  },
  "source-controller": {
    category: "GITOPS",
    display: "source-controller",
    source: "flux-system",
    consequence: "Flux source-controller manages this artifact metadata; force-applying will be reverted.",
    prefer: "Update the Git/Helm repository source instead.",
  },
  "notification-controller": {
    category: "CONTROLLER",
    display: "notification-controller",
    source: "flux-system",
    consequence: "Managed by Flux's notification controller; will be reset on reconcile.",
    prefer: "",
  },

  // GitOps — ArgoCD
  "argocd-application-controller": {
    category: "GITOPS",
    display: "argocd-application-controller",
    source: "argo-cd",
    consequence: "ArgoCD will revert your change on the next sync (auto-sync = 3 min default).",
    prefer: "Edit the source repo, or temporarily disable auto-sync for this Application.",
  },
  "argocd-server": {
    category: "GITOPS",
    display: "argocd-server",
    source: "argo-cd",
    consequence: "ArgoCD's API server manages this; force-applying will desync the Application.",
    prefer: "Edit via ArgoCD UI/CLI to keep the Application state coherent.",
  },

  // K8s control loops
  "horizontal-pod-autoscaler": {
    category: "CONTROLLER",
    display: "horizontal-pod-autoscaler",
    source: "kube-system",
    consequence: "The HPA continuously sets this based on current load — your value will be overwritten within seconds.",
    prefer: "Edit the HPA's min/max replica bounds instead of the Deployment directly.",
  },
  "vertical-pod-autoscaler": {
    category: "CONTROLLER",
    display: "vertical-pod-autoscaler",
    source: "kube-system",
    consequence: "VPA will reset resource requests/limits based on its recommendations.",
    prefer: "Edit the VPA policy or pause its recommendations.",
  },
  "deployment-controller": {
    category: "CONTROLLER",
    display: "deployment-controller",
    source: "kube-system",
    consequence: "The Deployment controller manages this status field; force-applying server-managed fields is unsafe.",
    prefer: "These are server-managed fields (status, observed generation, etc.) — don't write them.",
  },
  "replicaset-controller": {
    category: "CONTROLLER",
    display: "replicaset-controller",
    source: "kube-system",
    consequence: "ReplicaSet controller manages pod lifecycle; force-applying may be reverted on next pod sync.",
    prefer: "",
  },
  "kube-controller-manager": {
    category: "CONTROLLER",
    display: "kube-controller-manager",
    source: "kube-system",
    consequence: "A core K8s controller manages this field; force-applying is rarely safe.",
    prefer: "",
  },

  // Humans / direct edits
  "kubectl-edit": {
    category: "HUMAN",
    display: "kubectl-edit",
    source: "another operator",
    consequence: "Another operator wrote this field manually with `kubectl edit`. Forcing will overwrite their change.",
    prefer: "Coordinate with whoever last edited this resource before forcing.",
  },
  "kubectl-client-side-apply": {
    category: "HUMAN",
    display: "kubectl (client-side apply)",
    source: "legacy kubectl",
    consequence: "Old client-side-apply ownership. Generally safe to take.",
    prefer: "Take ownership and use server-side apply going forward (`kubectl apply --server-side`).",
  },
  "kubectl-create": {
    category: "HUMAN",
    display: "kubectl create",
    source: "kubectl",
    consequence: "Initial create marker; safe to take ownership.",
    prefer: "",
  },
  "kubectl-patch": {
    category: "HUMAN",
    display: "kubectl patch",
    source: "kubectl",
    consequence: "Direct patch from kubectl; force-applying will overwrite.",
    prefer: "",
  },
  "kubectl-rollout": {
    category: "HUMAN",
    display: "kubectl rollout",
    source: "kubectl",
    consequence: "Set by `kubectl rollout restart` (or similar). Generally safe to take, but you'll lose the restart marker for any controller watching for it.",
    prefer: "",
  },
  "kubectl-apply": {
    category: "HUMAN",
    display: "kubectl (server-side apply)",
    source: "kubectl",
    consequence: "Another operator wrote this field with `kubectl apply --server-side`. Forcing will overwrite their change.",
    prefer: "Coordinate with whoever last edited this resource before forcing.",
  },
  "kubectl-annotate": {
    category: "HUMAN",
    display: "kubectl annotate",
    source: "kubectl",
    consequence: "Set by `kubectl annotate`; force-applying will overwrite.",
    prefer: "",
  },
  "kubectl-label": {
    category: "HUMAN",
    display: "kubectl label",
    source: "kubectl",
    consequence: "Set by `kubectl label`; force-applying will overwrite.",
    prefer: "",
  },
  "kubectl-scale": {
    category: "HUMAN",
    display: "kubectl scale",
    source: "kubectl",
    consequence: "Set by `kubectl scale`. If an HPA is also active, forcing here is futile — the HPA will reset within seconds.",
    prefer: "Edit the HPA's min/max instead, or check if HPA is enabled before forcing.",
  },
  "kubectl-set": {
    category: "HUMAN",
    display: "kubectl set",
    source: "kubectl",
    consequence: "Set by `kubectl set image/env/resources/...`; force-applying will overwrite.",
    prefer: "",
  },

  // K8s apiserver internals
  "kube-apiserver": {
    category: "CONTROLLER",
    display: "kube-apiserver",
    source: "kube-system",
    consequence: "Set by the apiserver during admission. Force-applying server-managed fields is unsafe.",
    prefer: "These fields are typically read-only.",
  },

  // Periscope (us)
  "periscope-spa": {
    category: "PERISCOPE",
    display: "periscope-spa",
    source: "this app",
    consequence: "Already managed by Periscope.",
    prefer: "",
  },
};

/**
 * classifyManager looks up the registry for an exact match, then falls
 * back to suffix heuristics for things like custom controllers
 * (-controller / -operator). Always returns a valid ManagerInfo, even
 * for unknown names — UI renders the UNKNOWN category for those.
 */
export function classifyManager(name: string): ManagerInfo {
  const exact = REGISTRY[name];
  if (exact) return { name, ...exact };

  // Heuristic: anything ending in -controller is likely a controller-loop
  if (/-controller$/i.test(name)) {
    return {
      name,
      category: "CONTROLLER",
      display: name,
      source: "in-cluster controller",
      consequence: "An in-cluster controller manages this field; it may be reset by the control loop.",
      prefer: "",
    };
  }
  // -operator (operator-sdk style)
  if (/-operator$/i.test(name)) {
    return {
      name,
      category: "GITOPS",
      display: name,
      source: "operator",
      consequence: "An operator manages this resource; force-applying may be reverted on next reconcile.",
      prefer: "Edit the operator's CR/CRD instead of the managed resource.",
    };
  }
  // kubectl-* anything (kubectl-cordon, kubectl-drain, future subcommands…)
  if (/^kubectl/i.test(name)) {
    return {
      name,
      category: "HUMAN",
      display: name,
      source: "kubectl",
      consequence: "Another operator wrote this field with kubectl. Forcing will overwrite their change.",
      prefer: "Coordinate with whoever last edited this resource before forcing.",
    };
  }
  // helm-* anything
  if (/^helm/i.test(name)) {
    return {
      name,
      category: "HELM",
      display: name,
      source: "helm",
      consequence: "Helm manages this field; reverts on next chart upgrade.",
      prefer: "",
    };
  }
  return {
    name,
    category: "UNKNOWN",
    display: name,
    source: "unknown",
    consequence: "Manager not classified — outcome of force-applying is unclear.",
    prefer: "",
  };
}

/**
 * managerColorClass returns a Tailwind class set the UI uses to badge
 * a manager. Centralised here so the conflict view + glyph margins
 * stay consistent.
 */
export function managerColorClass(category: ManagerCategory): {
  text: string;
  bg: string;
  border: string;
  glyph: string;
} {
  switch (category) {
    case "GITOPS":
      return { text: "text-violet", bg: "bg-violet/10", border: "border-violet/40", glyph: "bg-violet" };
    case "CONTROLLER":
      return { text: "text-blue-500", bg: "bg-blue-500/10", border: "border-blue-500/40", glyph: "bg-blue-500" };
    case "HUMAN":
      return { text: "text-green", bg: "bg-green-soft", border: "border-green/40", glyph: "bg-green" };
    case "HELM":
      return { text: "text-teal-500", bg: "bg-teal-500/10", border: "border-teal-500/40", glyph: "bg-teal-500" };
    case "PERISCOPE":
      return { text: "text-accent", bg: "bg-accent-soft", border: "border-accent/40", glyph: "bg-accent" };
    case "UNKNOWN":
    default:
      return { text: "text-ink-muted", bg: "bg-surface-2", border: "border-border", glyph: "bg-ink-muted" };
  }
}
