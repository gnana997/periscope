// ConfigMapForm — schema-aware form view for editing ConfigMaps.
// Smallest of the four #116 forms; pure passthrough on the shared
// K8sSchemaForm composer (allowlist narrows the schema to
// metadata + data + binaryData + immutable; kv-map widget covers
// the data/labels/annotations editing).

import { K8sSchemaForm } from "./K8sSchemaForm";
import type { SchemaFormMode } from "../../lib/schemaForm/SchemaForm";
import type { ReactNode } from "react";

interface ConfigMapFormProps {
  cluster: string;
  valuesYaml: string;
  onValuesYamlChange: (next: string) => void;
  mode: SchemaFormMode;
  banner?: ReactNode;
}

export function ConfigMapForm(props: ConfigMapFormProps) {
  return <K8sSchemaForm {...props} kind="ConfigMap" />;
}
