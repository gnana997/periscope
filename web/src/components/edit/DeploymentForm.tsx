// DeploymentForm — schema-aware form view for editing Deployments.
//
// POC status: this wrapper is a thin passthrough on K8sSchemaForm,
// the same shape as ServiceForm/IngressForm. The allowlist in
// k8sAllowlist.ts surfaces metadata + spec.{replicas, selector,
// strategy, template, minReadySeconds, revisionHistoryLimit,
// progressDeadlineSeconds, paused}.
//
// Because `filterSchemaForKind` narrows only one level deep,
// allowlisting `spec.template` exposes the full PodTemplateSpec
// transitively. This is intentional for the POC — the goal of
// this iteration is to catalogue what the existing walker /
// renderer handles cleanly versus what it stumbles on (Quantity,
// IntOrString, env[*].valueFrom oneOf, ports[*].containerPort,
// volume oneOf with ~30 branches, probe handler oneOf, etc.).
// The gap report drives the follow-up scoping.

import { K8sSchemaForm } from "./K8sSchemaForm";
import type { SchemaFormMode } from "../../lib/schemaForm/SchemaForm";
import type { ReactNode } from "react";

interface DeploymentFormProps {
  cluster: string;
  valuesYaml: string;
  onValuesYamlChange: (next: string) => void;
  mode: SchemaFormMode;
  banner?: ReactNode;
}

export function DeploymentForm(props: DeploymentFormProps) {
  return <K8sSchemaForm {...props} kind="Deployment" />;
}
