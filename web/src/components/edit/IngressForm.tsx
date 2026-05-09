// IngressForm — schema-aware form view for editing Ingresses.
// Pure passthrough on K8sSchemaForm; the allowlist narrows to
// metadata + spec.{ingressClassName, rules, tls, defaultBackend}.
// Controller-specific annotations (alb.ingress.* /
// nginx.ingress.* / etc.) are kv-mapped via metadata.annotations
// and pass through verbatim — no validation per #116.
//
// The TLS secretName picker against existing Secrets is a polish
// follow-up; v1.1 renders it as the standard string input the
// schema implies.

import { K8sSchemaForm } from "./K8sSchemaForm";
import type { SchemaFormMode } from "../../lib/schemaForm/SchemaForm";
import type { ReactNode } from "react";

interface IngressFormProps {
  cluster: string;
  valuesYaml: string;
  onValuesYamlChange: (next: string) => void;
  mode: SchemaFormMode;
  banner?: ReactNode;
}

export function IngressForm(props: IngressFormProps) {
  return <K8sSchemaForm {...props} kind="Ingress" />;
}
