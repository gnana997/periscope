// StatefulSetForm — schema-aware form view for editing StatefulSets.
//
// Thin passthrough on K8sSchemaForm. The allowlist in
// k8sAllowlist.ts surfaces the StatefulSet-specific fields
// (serviceName, podManagementPolicy, updateStrategy,
// volumeClaimTemplates, persistentVolumeClaimRetentionPolicy,
// ordinals) plus the same curated PodSpec subset Deployment uses
// (containers, initContainers, volumes, probes, lifecycle,
// pod-level admin surface flagged advanced).
//
// Selector / serviceName / podManagementPolicy / volumeClaimTemplates
// are create-only — the apiserver rejects mutations after create.

import { K8sSchemaForm } from "./K8sSchemaForm";
import type { SchemaFormMode } from "../../lib/schemaForm/SchemaForm";
import type { ReactNode } from "react";

interface StatefulSetFormProps {
  cluster: string;
  valuesYaml: string;
  onValuesYamlChange: (next: string) => void;
  mode: SchemaFormMode;
  banner?: ReactNode;
}

export function StatefulSetForm(props: StatefulSetFormProps) {
  return <K8sSchemaForm {...props} kind="StatefulSet" />;
}
