// SecretForm — schema-aware form view for editing Secrets.
// Identical to ConfigMapForm except for a base64 layer over
// `data`: operators type plaintext, the apply pipeline still sees
// canonical base64. A "show raw base64" toggle bypasses the layer
// so operators can inspect the actual wire values when auditing.

import { useMemo, useState } from "react";
import type { ReactNode } from "react";
import { K8sSchemaForm } from "./K8sSchemaForm";
import {
  decodeSecretYaml,
  encodeSecretYaml,
} from "../../lib/schemaForm/base64DataLayer";
import type { SchemaFormMode } from "../../lib/schemaForm/SchemaForm";

interface SecretFormProps {
  cluster: string;
  valuesYaml: string;
  onValuesYamlChange: (next: string) => void;
  mode: SchemaFormMode;
  banner?: ReactNode;
}

export function SecretForm({
  cluster,
  valuesYaml,
  onValuesYamlChange,
  mode,
  banner,
}: SecretFormProps) {
  const [showRaw, setShowRaw] = useState(false);

  // The form sees plaintext under data[k] when showRaw is off; the
  // raw YAML kept in the parent stays canonical (base64).
  const formViewYaml = useMemo(
    () => (showRaw ? valuesYaml : decodeSecretYaml(valuesYaml)),
    [valuesYaml, showRaw],
  );

  const handleChange = (next: string) => {
    if (showRaw) {
      onValuesYamlChange(next);
      return;
    }
    onValuesYamlChange(encodeSecretYaml(next));
  };

  const composedBanner = (
    <>
      {banner}
      <div className="flex items-center justify-between rounded-sm border border-border bg-surface px-3 py-1.5">
        <span className="font-mono text-[11.5px] text-ink-muted">
          data values are {showRaw ? "shown as raw base64" : "decoded to plaintext for editing"}
        </span>
        <button
          type="button"
          onClick={() => setShowRaw((s) => !s)}
          className="font-mono text-[11.5px] text-accent hover:underline"
        >
          {showRaw ? "show plaintext" : "show raw base64"}
        </button>
      </div>
    </>
  );

  return (
    <K8sSchemaForm
      cluster={cluster}
      kind="Secret"
      valuesYaml={formViewYaml}
      onValuesYamlChange={handleChange}
      mode={mode}
      banner={composedBanner}
    />
  );
}
