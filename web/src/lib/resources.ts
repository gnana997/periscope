/**
 * Resource catalog — the source of truth for the sidebar IA.
 * Adding a new resource means adding an entry here and the corresponding
 * page/table; no other place to edit.
 */

import type { ResourceKind } from "./types";

export type ResourceGroup = "Workloads" | "Networking" | "Config" | "Cluster";

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
  | "exec"
  | "events"
  | "statefulsets"
  | "daemonsets"
  | "ingresses"
  | "secrets"
  | "jobs"
  | "cronjobs";

export const RESOURCES: ResourceMeta[] = [
  { id: "pods",         label: "Pods",         group: "Workloads",  ready: true  },
  { id: "deployments",  label: "Deployments",  group: "Workloads",  ready: true  },
  { id: "statefulsets", label: "StatefulSets", group: "Workloads",  ready: false },
  { id: "daemonsets",   label: "DaemonSets",   group: "Workloads",  ready: false },
  { id: "jobs",         label: "Jobs",         group: "Workloads",  ready: false },
  { id: "cronjobs",     label: "CronJobs",     group: "Workloads",  ready: false },
  { id: "services",     label: "Services",     group: "Networking", ready: true  },
  { id: "ingresses",    label: "Ingresses",    group: "Networking", ready: false },
  { id: "configmaps",   label: "ConfigMaps",   group: "Config",     ready: true  },
  { id: "secrets",      label: "Secrets",      group: "Config",     ready: false },
  { id: "namespaces",   label: "Namespaces",   group: "Cluster",    ready: true  },
  { id: "events",       label: "Events",       group: "Cluster",    ready: false },
];

export const RESOURCE_GROUPS: ResourceGroup[] = [
  "Workloads",
  "Networking",
  "Config",
  "Cluster",
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
