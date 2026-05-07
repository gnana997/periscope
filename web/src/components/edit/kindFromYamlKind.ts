// kindFromYamlKind — pure mapping from the source's lowercase
// plural (`configmaps`, `secrets`, …) to the SupportedKind label.
// Pulled out of KindEditRouter.tsx so YamlView can call it without
// loading the React component tree (and so the component file
// stays react-refresh-clean).

import { isSupportedKind, type SupportedKind } from "../../lib/schemaForm/k8sAllowlist";

const PLURAL_TO_KIND: Record<string, SupportedKind> = {
  configmaps: "ConfigMap",
  secrets: "Secret",
  services: "Service",
  ingresses: "Ingress",
  deployments: "Deployment",
  statefulsets: "StatefulSet",
};

export function kindFromYamlKind(yamlKind: string): SupportedKind | undefined {
  const k = PLURAL_TO_KIND[yamlKind];
  return k && isSupportedKind(k) ? k : undefined;
}
