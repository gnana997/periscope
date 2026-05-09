// ServiceForm — schema-aware form view for editing Services.
// Pure passthrough on K8sSchemaForm; the allowlist narrows to
// metadata + spec.{type, selector, ports, clusterIP,
// externalTrafficPolicy, sessionAffinity, ipFamilies,
// ipFamilyPolicy}. spec.type renders as a <select> via the
// schema's existing `enum` definition; the array-of-objects
// widget covers spec.ports.

import { K8sSchemaForm } from "./K8sSchemaForm";
import type { SchemaFormMode } from "../../lib/schemaForm/SchemaForm";
import type { ReactNode } from "react";

interface ServiceFormProps {
  cluster: string;
  valuesYaml: string;
  onValuesYamlChange: (next: string) => void;
  mode: SchemaFormMode;
  banner?: ReactNode;
}

export function ServiceForm(props: ServiceFormProps) {
  return <K8sSchemaForm {...props} kind="Service" />;
}
