/**
 * CRD renderer registry.
 *
 * Specialized describe renderers for popular operators. Each renderer
 * receives the unstructured object and returns the spec/status section
 * tree — the surrounding frame (metadata, labels, annotations, owner
 * refs, status badges, conditions) is provided by CustomResourceDescribe.
 *
 * To add a new renderer: implement a component matching CRDRenderer,
 * then map `<group>/<kind>` to it in RENDERERS below.
 */
import type { ReactNode } from "react";
import { Certificate } from "./Certificate";
import { Issuer } from "./Issuer";
import { Application } from "./Application";
import { ServiceMonitor } from "./ServiceMonitor";
import { VirtualService } from "./VirtualService";

export interface CRDRendererProps {
  obj: Record<string, unknown>;
  cluster: string;
  namespace: string | null;
}

export type CRDRenderer = (props: CRDRendererProps) => ReactNode;

const RENDERERS: Record<string, CRDRenderer> = {
  "cert-manager.io/Certificate": Certificate,
  "cert-manager.io/Issuer": Issuer,
  "cert-manager.io/ClusterIssuer": Issuer,
  "argoproj.io/Application": Application,
  "monitoring.coreos.com/ServiceMonitor": ServiceMonitor,
  "networking.istio.io/VirtualService": VirtualService,
};

export function getCRDRenderer(
  group: string,
  kind: string,
): CRDRenderer | undefined {
  return RENDERERS[`${group}/${kind}`];
}
