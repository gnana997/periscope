// k8sDiscriminatorHints.ts — hint table for K8s schemas that
// encode polymorphism via sibling properties instead of JSON
// Schema `oneOf`. The walker consults this table (via
// `WalkOptions.discriminatorHints`) and emits Shape B
// discriminator descriptors for matched types.
//
// Every entry here corresponds to a K8s API type whose Go struct
// uses pointer fields the apiserver rejects unless exactly one is
// set, but whose OpenAPI v3 schema doesn't model that constraint.
// Without the hint the walker would surface 30 sibling properties
// for `Volume`, 4 for `Probe`'s handler, etc., all simultaneously
// editable — operators could fill multiple branches at once and the
// apply would 422 with a confusing message.
//
// Hints are matched against the `$ref` of the property's primary
// type, looking through a single-element `allOf:[{$ref:...}]`
// envelope (the standard kube-openapi shape).
//
// New types should be added here, NOT inferred structurally —
// sibling-property polymorphism is a K8s convention, not a schema
// pattern, so there's no reliable way to detect it without a list.

import type { DiscriminatorHint } from "./walker";

const REF = (name: string) => `#/components/schemas/${name}`;

/** Build the K8s hint table. Returns a fresh Map per call so callers
 *  can mutate it (e.g. to layer in CRD-specific hints) without
 *  affecting other consumers. */
export function buildK8sDiscriminatorHints(): Map<string, DiscriminatorHint> {
  const t = new Map<string, DiscriminatorHint>();

  // Probe — used by livenessProbe / readinessProbe / startupProbe.
  // Branches: handler types. Shared: threshold knobs (rendered as
  // sharedChildren by the walker).
  t.set(REF("io.k8s.api.core.v1.Probe"), {
    branches: ["httpGet", "tcpSocket", "exec", "grpc"],
    labels: {
      httpGet: "HTTP GET",
      tcpSocket: "TCP socket",
      exec: "exec",
      grpc: "gRPC",
    },
  });

  // LifecycleHandler — used by lifecycle.preStop / lifecycle.postStart.
  // Pre-1.23 was named `Handler` — modern clusters expose this name.
  t.set(REF("io.k8s.api.core.v1.LifecycleHandler"), {
    branches: ["httpGet", "tcpSocket", "exec", "sleep"],
    labels: {
      httpGet: "HTTP GET",
      tcpSocket: "TCP socket",
      exec: "exec",
      sleep: "sleep",
    },
  });

  // EnvVarSource — used by env[*].valueFrom.
  t.set(REF("io.k8s.api.core.v1.EnvVarSource"), {
    branches: ["fieldRef", "resourceFieldRef", "configMapKeyRef", "secretKeyRef"],
    labels: {
      fieldRef: "Field reference",
      resourceFieldRef: "Resource field reference",
      configMapKeyRef: "ConfigMap key",
      secretKeyRef: "Secret key",
    },
  });

  // EnvFromSource — used by container `envFrom[]` items. `prefix`
  // is shared (always-on); branches are configMapRef / secretRef.
  t.set(REF("io.k8s.api.core.v1.EnvFromSource"), {
    branches: ["configMapRef", "secretRef"],
    labels: {
      configMapRef: "ConfigMap",
      secretRef: "Secret",
    },
  });

  // Volume — used by spec.template.spec.volumes[]. `name` is
  // shared (the volume's identifier); branches are the volume
  // types. Many branches → renderer collapses to a <select>.
  //
  // We intentionally include the full set so operators don't see
  // "yaml only" for an obscure volume type. The kube-openapi-listed
  // names are stable across K8s versions; new types added by future
  // APIs would silently render as siblings until added here (same
  // failure mode as the rest of the schema-form pipeline).
  t.set(REF("io.k8s.api.core.v1.Volume"), {
    branches: [
      "configMap",
      "secret",
      "emptyDir",
      "persistentVolumeClaim",
      "hostPath",
      "projected",
      "downwardAPI",
      "csi",
      "ephemeral",
      "nfs",
      "iscsi",
      "fc",
      "rbd",
      "cephfs",
      "glusterfs",
      "gitRepo",
      "azureDisk",
      "azureFile",
      "gcePersistentDisk",
      "awsElasticBlockStore",
      "vsphereVolume",
      "cinder",
      "flexVolume",
      "flocker",
      "photonPersistentDisk",
      "portworxVolume",
      "quobyte",
      "scaleIO",
      "storageos",
              ],
    labels: {
      configMap: "ConfigMap",
      secret: "Secret",
      emptyDir: "emptyDir",
      persistentVolumeClaim: "PVC",
      hostPath: "hostPath",
      projected: "Projected",
      downwardAPI: "Downward API",
      csi: "CSI",
      ephemeral: "Ephemeral",
      nfs: "NFS",
      iscsi: "iSCSI",
      fc: "Fibre Channel",
      rbd: "RBD",
      cephfs: "CephFS",
      glusterfs: "GlusterFS",
      gitRepo: "Git repo",
      azureDisk: "Azure Disk",
      azureFile: "Azure File",
      gcePersistentDisk: "GCE PD",
      awsElasticBlockStore: "AWS EBS",
      vsphereVolume: "vSphere",
      cinder: "Cinder",
      flexVolume: "FlexVolume",
      flocker: "Flocker",
      photonPersistentDisk: "Photon PD",
      portworxVolume: "Portworx",
      quobyte: "Quobyte",
      scaleIO: "ScaleIO",
      storageos: "StorageOS",
    },
  });

  return t;
}
