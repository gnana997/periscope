/**
 * Resource catalog — the source of truth for the sidebar IA.
 * Adding a new resource means adding an entry here and the corresponding
 * page/table; no other place to edit.
 */

import type { ResourceKind } from "./types";

export type ResourceGroup = "Workloads" | "Networking" | "Config" | "Storage" | "Cluster" | "Access" | "Extensions";

export interface ResourceMeta {
  id: ResourceKind | SoonResource;
  label: string;
  group: ResourceGroup;
  /** Available in the current beta. */
  ready: boolean;
}

/** Resources that appear in the nav as "SOON". */
export type SoonResource =
  | "logs"
  | "exec";

export const RESOURCES: ResourceMeta[] = [
  { id: "overview",     label: "Overview",     group: "Cluster",    ready: true  },
  { id: "pods",         label: "Pods",         group: "Workloads",  ready: true  },
  { id: "deployments",  label: "Deployments",  group: "Workloads",  ready: true  },
  { id: "statefulsets", label: "StatefulSets", group: "Workloads",  ready: true  },
  { id: "daemonsets",   label: "DaemonSets",   group: "Workloads",  ready: true  },
  { id: "jobs",         label: "Jobs",         group: "Workloads",  ready: true  },
  { id: "cronjobs",     label: "CronJobs",     group: "Workloads",  ready: true  },
  { id: "replicasets",              label: "ReplicaSets",              group: "Workloads",  ready: true  },
  { id: "horizontalpodautoscalers", label: "HorizontalPodAutoscalers", group: "Workloads",  ready: true  },
  { id: "poddisruptionbudgets",     label: "PodDisruptionBudgets",     group: "Workloads",  ready: true  },
  { id: "services",     label: "Services",     group: "Networking", ready: true  },
  { id: "ingresses",    label: "Ingresses",    group: "Networking", ready: true  },
  { id: "networkpolicies",  label: "NetworkPolicies",  group: "Networking", ready: true  },
  { id: "ingressclasses",   label: "IngressClasses",   group: "Networking", ready: true  },
  { id: "configmaps",    label: "ConfigMaps",    group: "Config",   ready: true  },
  { id: "secrets",       label: "Secrets",       group: "Config",   ready: true  },
  { id: "resourcequotas",  label: "ResourceQuotas",  group: "Config", ready: true  },
  { id: "limitranges",     label: "LimitRanges",     group: "Config", ready: true  },
  { id: "pvcs",          label: "PersistentVolumeClaims", group: "Storage",  ready: true  },
  { id: "pvs",           label: "PersistentVolumes", group: "Storage", ready: true },
  { id: "storageclasses", label: "StorageClasses", group: "Storage", ready: true },
  { id: "nodes",               label: "Nodes",               group: "Cluster",  ready: true  },
  { id: "namespaces",          label: "Namespaces",          group: "Cluster",  ready: true  },
  { id: "events",              label: "Events",              group: "Cluster",  ready: true  },
  { id: "priorityclasses",  label: "PriorityClasses",  group: "Cluster", ready: true  },
  { id: "runtimeclasses",   label: "RuntimeClasses",   group: "Cluster", ready: true  },
  { id: "roles",               label: "Roles",               group: "Access",   ready: true  },
  { id: "clusterroles",        label: "ClusterRoles",        group: "Access",   ready: true  },
  { id: "rolebindings",        label: "RoleBindings",        group: "Access",   ready: true  },
  { id: "clusterrolebindings", label: "ClusterRoleBindings", group: "Access",   ready: true  },
  { id: "serviceaccounts",     label: "ServiceAccounts",     group: "Access",   ready: true  },
  { id: "crds",                label: "Custom Resources",     group: "Extensions", ready: true  },
];

export const RESOURCE_GROUPS: ResourceGroup[] = [
  "Cluster",
  "Workloads",
  "Networking",
  "Config",
  "Storage",
  "Access",
  "Extensions",
];

export function resourcesByGroup(group: ResourceGroup): ResourceMeta[] {
  return RESOURCES.filter((r) => r.group === group);
}

export function resourceLabel(id: string): string {
  return RESOURCES.find((r) => r.id === id)?.label ?? id;
}

export function isReadyResource(id: string): id is ResourceKind {
  const r = RESOURCES.find((r) => r.id === id);
  return r ? r.ready : false;
}
