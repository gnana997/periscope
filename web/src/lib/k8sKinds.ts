// k8sKinds — registry mapping YamlKind URL segments to GVRK metadata.
//
// Used by YamlView to derive a ResourceRef from the (kind, cluster, ns,
// name) it already receives, so pages don't have to thread the same
// information twice (once for ResourceActions, once for YamlView).
//
// When new resource types are added to the SPA, add them here too. The
// values come straight from K8s API conventions:
//
//   group:    "" for core/v1 resources, the API group otherwise
//   version:  the API version (e.g. "v1", "v1beta1")
//   resource: plural URL segment (matches the YamlKind tag)
//   kind:     PascalCase singular kind (used as a human label + for
//             schema lookup via x-kubernetes-group-version-kind)

import type { YamlKind } from "./api";

export interface KindMeta {
  group: string;
  version: string;
  resource: string;
  kind: string;
}

export const KIND_REGISTRY: Record<YamlKind, KindMeta> = {
  // core/v1
  pods:                 { group: "",                       version: "v1",       resource: "pods",                       kind: "Pod" },
  services:             { group: "",                       version: "v1",       resource: "services",                   kind: "Service" },
  configmaps:           { group: "",                       version: "v1",       resource: "configmaps",                 kind: "ConfigMap" },
  secrets:              { group: "",                       version: "v1",       resource: "secrets",                    kind: "Secret" },
  namespaces:           { group: "",                       version: "v1",       resource: "namespaces",                 kind: "Namespace" },
  pvs:                  { group: "",                       version: "v1",       resource: "persistentvolumes",          kind: "PersistentVolume" },
  pvcs:                 { group: "",                       version: "v1",       resource: "persistentvolumeclaims",     kind: "PersistentVolumeClaim" },
  serviceaccounts:      { group: "",                       version: "v1",       resource: "serviceaccounts",            kind: "ServiceAccount" },
  resourcequotas:       { group: "",                       version: "v1",       resource: "resourcequotas",             kind: "ResourceQuota" },
  limitranges:          { group: "",                       version: "v1",       resource: "limitranges",                kind: "LimitRange" },

  // apps/v1
  deployments:          { group: "apps",                   version: "v1",       resource: "deployments",                kind: "Deployment" },
  statefulsets:         { group: "apps",                   version: "v1",       resource: "statefulsets",               kind: "StatefulSet" },
  daemonsets:           { group: "apps",                   version: "v1",       resource: "daemonsets",                  kind: "DaemonSet" },
  replicasets:          { group: "apps",                   version: "v1",       resource: "replicasets",                kind: "ReplicaSet" },

  // batch/v1
  jobs:                 { group: "batch",                  version: "v1",       resource: "jobs",                       kind: "Job" },
  cronjobs:             { group: "batch",                  version: "v1",       resource: "cronjobs",                   kind: "CronJob" },

  // networking.k8s.io/v1
  ingresses:            { group: "networking.k8s.io",      version: "v1",       resource: "ingresses",                  kind: "Ingress" },
  ingressclasses:       { group: "networking.k8s.io",      version: "v1",       resource: "ingressclasses",             kind: "IngressClass" },
  networkpolicies:      { group: "networking.k8s.io",      version: "v1",       resource: "networkpolicies",            kind: "NetworkPolicy" },

  // rbac.authorization.k8s.io/v1
  roles:                { group: "rbac.authorization.k8s.io", version: "v1",    resource: "roles",                      kind: "Role" },
  rolebindings:         { group: "rbac.authorization.k8s.io", version: "v1",    resource: "rolebindings",               kind: "RoleBinding" },
  clusterroles:         { group: "rbac.authorization.k8s.io", version: "v1",    resource: "clusterroles",               kind: "ClusterRole" },
  clusterrolebindings:  { group: "rbac.authorization.k8s.io", version: "v1",    resource: "clusterrolebindings",        kind: "ClusterRoleBinding" },

  // storage.k8s.io/v1
  storageclasses:       { group: "storage.k8s.io",         version: "v1",       resource: "storageclasses",             kind: "StorageClass" },

  // autoscaling/v2
  horizontalpodautoscalers: { group: "autoscaling",        version: "v2",       resource: "horizontalpodautoscalers",   kind: "HorizontalPodAutoscaler" },

  // policy/v1
  poddisruptionbudgets: { group: "policy",                 version: "v1",       resource: "poddisruptionbudgets",       kind: "PodDisruptionBudget" },

  // scheduling.k8s.io/v1
  priorityclasses:      { group: "scheduling.k8s.io",      version: "v1",       resource: "priorityclasses",            kind: "PriorityClass" },

  // node.k8s.io/v1
  runtimeclasses:       { group: "node.k8s.io",            version: "v1",       resource: "runtimeclasses",             kind: "RuntimeClass" },
  nodes:                { group: "",                       version: "v1",       resource: "nodes",                      kind: "Node" },
};
