// Round-trip property tests per #116 acceptance:
//   "form → YAML → form for representative manifests of each kind
//    produces identical state."
//
// We pull representative manifests for ConfigMap / Secret /
// Service / Ingress, parse them, run them through the walker
// against a synthetic OpenAPI v3 doc that mirrors the real
// apiserver shape (the four root types tagged with
// x-kubernetes-group-version-kind), and verify that:
//
//   1. Parse → stringify → parse is idempotent (the SchemaFormBridge
//      round-trip preserves the values object structurally).
//   2. The walker emits descriptors that cover the manifest's
//      operator-facing fields (so the form actually renders them).
//   3. Editing a leaf via setAtPath updates the YAML on the round
//      trip without dropping sibling keys.

import { describe, expect, it } from "vitest";
import { parse as parseYaml, stringify as stringifyYaml } from "yaml";
import { buildFieldDescriptors } from "./walker";
import { buildRefResolver, findSchemaByGVK } from "./refResolver";
import { buildK8sDiscriminatorHints } from "./k8sDiscriminatorHints";
import {
  filterSchemaForKind,
  getAdvancedPaths,
  getCreateOnlyPaths,
  getKindGVK,
  type SupportedKind,
} from "./k8sAllowlist";
import { getAtPath, setAtPath } from "./pathOps";
import type { JSONSchema, FieldDescriptor } from "./types";
import type { OpenAPIDoc } from "../api";

// Synthetic doc covering all four kinds with the shapes the real
// K8s OpenAPI v3 exposes (just enough for the round-trip walker
// to render the operator-facing fields).
const synthDoc: OpenAPIDoc = {
  components: {
    schemas: {
      "io.k8s.apimachinery.pkg.apis.meta.v1.ObjectMeta": {
        type: "object",
        properties: {
          name: { type: "string" },
          namespace: { type: "string" },
          labels: { type: "object", additionalProperties: { type: "string" } },
          annotations: { type: "object", additionalProperties: { type: "string" } },
          uid: { type: "string" },
          creationTimestamp: { type: "string" },
          resourceVersion: { type: "string" },
          managedFields: { type: "array", items: { type: "object" } },
        },
      },
      "io.k8s.api.core.v1.ConfigMap": {
        type: "object",
        "x-kubernetes-group-version-kind": [
          { group: "", version: "v1", kind: "ConfigMap" },
        ],
        properties: {
          apiVersion: { type: "string" },
          kind: { type: "string" },
          metadata: { $ref: "#/components/schemas/io.k8s.apimachinery.pkg.apis.meta.v1.ObjectMeta" },
          data: { type: "object", additionalProperties: { type: "string" } },
          binaryData: { type: "object", additionalProperties: { type: "string" } },
          immutable: { type: "boolean" },
        },
      },
      "io.k8s.api.core.v1.Secret": {
        type: "object",
        "x-kubernetes-group-version-kind": [
          { group: "", version: "v1", kind: "Secret" },
        ],
        properties: {
          apiVersion: { type: "string" },
          kind: { type: "string" },
          metadata: { $ref: "#/components/schemas/io.k8s.apimachinery.pkg.apis.meta.v1.ObjectMeta" },
          type: { type: "string" },
          data: { type: "object", additionalProperties: { type: "string" } },
          stringData: { type: "object", additionalProperties: { type: "string" } },
          immutable: { type: "boolean" },
        },
      },
      "io.k8s.api.core.v1.ServicePort": {
        type: "object",
        properties: {
          name: { type: "string" },
          port: { type: "integer" },
          targetPort: { type: "integer" },
          protocol: { type: "string", enum: ["TCP", "UDP", "SCTP"] },
          nodePort: { type: "integer" },
        },
        required: ["port"],
      },
      "io.k8s.api.core.v1.ServiceSpec": {
        type: "object",
        properties: {
          type: { type: "string", enum: ["ClusterIP", "NodePort", "LoadBalancer", "ExternalName"] },
          selector: { type: "object", additionalProperties: { type: "string" } },
          ports: {
            type: "array",
            items: { $ref: "#/components/schemas/io.k8s.api.core.v1.ServicePort" },
          },
          clusterIP: { type: "string" },
          externalTrafficPolicy: { type: "string" },
          sessionAffinity: { type: "string" },
        },
      },
      "io.k8s.api.core.v1.Service": {
        type: "object",
        "x-kubernetes-group-version-kind": [
          { group: "", version: "v1", kind: "Service" },
        ],
        properties: {
          apiVersion: { type: "string" },
          kind: { type: "string" },
          metadata: { $ref: "#/components/schemas/io.k8s.apimachinery.pkg.apis.meta.v1.ObjectMeta" },
          spec: { $ref: "#/components/schemas/io.k8s.api.core.v1.ServiceSpec" },
          status: { type: "object" },
        },
      },
      "io.k8s.api.networking.v1.HTTPIngressPath": {
        type: "object",
        properties: {
          path: { type: "string" },
          pathType: { type: "string" },
        },
        required: ["path", "pathType"],
      },
      "io.k8s.api.networking.v1.IngressRule": {
        type: "object",
        properties: {
          host: { type: "string" },
          // Real schema nests http.paths; flattened here for the test.
          paths: {
            type: "array",
            items: { $ref: "#/components/schemas/io.k8s.api.networking.v1.HTTPIngressPath" },
          },
        },
      },
      "io.k8s.api.networking.v1.IngressTLS": {
        type: "object",
        properties: {
          hosts: { type: "array", items: { type: "string" } },
          secretName: { type: "string" },
        },
      },
      "io.k8s.api.networking.v1.IngressSpec": {
        type: "object",
        properties: {
          ingressClassName: { type: "string" },
          rules: {
            type: "array",
            items: { $ref: "#/components/schemas/io.k8s.api.networking.v1.IngressRule" },
          },
          tls: {
            type: "array",
            items: { $ref: "#/components/schemas/io.k8s.api.networking.v1.IngressTLS" },
          },
        },
      },
      "io.k8s.api.networking.v1.Ingress": {
        type: "object",
        "x-kubernetes-group-version-kind": [
          { group: "networking.k8s.io", version: "v1", kind: "Ingress" },
        ],
        properties: {
          apiVersion: { type: "string" },
          kind: { type: "string" },
          metadata: { $ref: "#/components/schemas/io.k8s.apimachinery.pkg.apis.meta.v1.ObjectMeta" },
          spec: { $ref: "#/components/schemas/io.k8s.api.networking.v1.IngressSpec" },
          status: { type: "object" },
        },
      },
      // ── Deployment shapes ─────────────────────────────────────
      // These mirror the real K8s OpenAPI v3: every field that
      // references another type is wrapped in `allOf:[{$ref: ...}]`
      // (the kube-openapi convention). The filter has to expand
      // those envelopes to narrow inside.
      "io.k8s.apimachinery.pkg.apis.meta.v1.LabelSelector": {
        type: "object",
        properties: {
          matchLabels: { type: "object", additionalProperties: { type: "string" } },
          matchExpressions: {
            type: "array",
            items: {
              type: "object",
              properties: {
                key: { type: "string" },
                operator: { type: "string" },
                values: { type: "array", items: { type: "string" } },
              },
              required: ["key", "operator"],
            },
          },
        },
      },
      "io.k8s.api.core.v1.ContainerPort": {
        type: "object",
        properties: {
          name: { type: "string" },
          containerPort: { type: "integer" },
          protocol: { type: "string", enum: ["TCP", "UDP", "SCTP"] },
          hostPort: { type: "integer" },
          hostIP: { type: "string" },
        },
        required: ["containerPort"],
      },
      "io.k8s.api.core.v1.EnvVar": {
        type: "object",
        properties: {
          name: { type: "string" },
          value: { type: "string" },
          // `valueFrom` would normally also live here as a sibling-
          // encoded oneOf — out of scope for the curated allowlist.
        },
        required: ["name"],
      },
      "io.k8s.api.core.v1.ResourceRequirements": {
        type: "object",
        properties: {
          limits: { type: "object", additionalProperties: { type: "string" } },
          requests: { type: "object", additionalProperties: { type: "string" } },
        },
      },
      // ── K8s sibling-encoded oneOfs (consumed by the hint table) ──
      "io.k8s.api.core.v1.HTTPGetAction": {
        type: "object",
        properties: {
          path: { type: "string" },
          port: { type: "string", format: "int-or-string" },
          scheme: { type: "string", enum: ["HTTP", "HTTPS"] },
        },
      },
      "io.k8s.api.core.v1.TCPSocketAction": {
        type: "object",
        properties: {
          port: { type: "string", format: "int-or-string" },
          host: { type: "string" },
        },
      },
      "io.k8s.api.core.v1.ExecAction": {
        type: "object",
        properties: {
          command: { type: "array", items: { type: "string" } },
        },
      },
      "io.k8s.api.core.v1.GRPCAction": {
        type: "object",
        properties: {
          port: { type: "integer" },
          service: { type: "string" },
        },
      },
      "io.k8s.api.core.v1.SleepAction": {
        type: "object",
        properties: { seconds: { type: "integer" } },
      },
      "io.k8s.api.core.v1.Probe": {
        type: "object",
        properties: {
          httpGet: { allOf: [{ $ref: "#/components/schemas/io.k8s.api.core.v1.HTTPGetAction" }] },
          tcpSocket: { allOf: [{ $ref: "#/components/schemas/io.k8s.api.core.v1.TCPSocketAction" }] },
          exec: { allOf: [{ $ref: "#/components/schemas/io.k8s.api.core.v1.ExecAction" }] },
          grpc: { allOf: [{ $ref: "#/components/schemas/io.k8s.api.core.v1.GRPCAction" }] },
          initialDelaySeconds: { type: "integer" },
          periodSeconds: { type: "integer" },
          timeoutSeconds: { type: "integer" },
          successThreshold: { type: "integer" },
          failureThreshold: { type: "integer" },
        },
      },
      "io.k8s.api.core.v1.LifecycleHandler": {
        type: "object",
        properties: {
          httpGet: { allOf: [{ $ref: "#/components/schemas/io.k8s.api.core.v1.HTTPGetAction" }] },
          tcpSocket: { allOf: [{ $ref: "#/components/schemas/io.k8s.api.core.v1.TCPSocketAction" }] },
          exec: { allOf: [{ $ref: "#/components/schemas/io.k8s.api.core.v1.ExecAction" }] },
          sleep: { allOf: [{ $ref: "#/components/schemas/io.k8s.api.core.v1.SleepAction" }] },
        },
      },
      "io.k8s.api.core.v1.Lifecycle": {
        type: "object",
        properties: {
          preStop: { allOf: [{ $ref: "#/components/schemas/io.k8s.api.core.v1.LifecycleHandler" }] },
          postStart: { allOf: [{ $ref: "#/components/schemas/io.k8s.api.core.v1.LifecycleHandler" }] },
        },
      },
      // Volume + sources (subset: configMap, secret, emptyDir) —
      // hint table covers the full ~30-volume canon; we model just
      // enough here to validate the array-of-discriminators path.
      "io.k8s.api.core.v1.ConfigMapVolumeSource": {
        type: "object",
        properties: {
          name: { type: "string" },
          optional: { type: "boolean" },
          defaultMode: { type: "integer" },
        },
      },
      "io.k8s.api.core.v1.SecretVolumeSource": {
        type: "object",
        properties: {
          secretName: { type: "string" },
          optional: { type: "boolean" },
          defaultMode: { type: "integer" },
        },
      },
      "io.k8s.api.core.v1.EmptyDirVolumeSource": {
        type: "object",
        properties: {
          medium: { type: "string" },
          sizeLimit: { type: "string" },
        },
      },
      "io.k8s.api.core.v1.Volume": {
        type: "object",
        properties: {
          name: { type: "string" },
          configMap: {
            allOf: [{ $ref: "#/components/schemas/io.k8s.api.core.v1.ConfigMapVolumeSource" }],
          },
          secret: {
            allOf: [{ $ref: "#/components/schemas/io.k8s.api.core.v1.SecretVolumeSource" }],
          },
          emptyDir: {
            allOf: [{ $ref: "#/components/schemas/io.k8s.api.core.v1.EmptyDirVolumeSource" }],
          },
        },
        required: ["name"],
      },
      "io.k8s.api.core.v1.VolumeMount": {
        type: "object",
        properties: {
          name: { type: "string" },
          mountPath: { type: "string" },
          readOnly: { type: "boolean" },
          subPath: { type: "string" },
        },
        required: ["name", "mountPath"],
      },
      "io.k8s.api.core.v1.ConfigMapEnvSource": {
        type: "object",
        properties: { name: { type: "string" }, optional: { type: "boolean" } },
      },
      "io.k8s.api.core.v1.SecretEnvSource": {
        type: "object",
        properties: { name: { type: "string" }, optional: { type: "boolean" } },
      },
      "io.k8s.api.core.v1.EnvFromSource": {
        type: "object",
        properties: {
          prefix: { type: "string" },
          configMapRef: {
            allOf: [{ $ref: "#/components/schemas/io.k8s.api.core.v1.ConfigMapEnvSource" }],
          },
          secretRef: {
            allOf: [{ $ref: "#/components/schemas/io.k8s.api.core.v1.SecretEnvSource" }],
          },
        },
      },
      "io.k8s.api.core.v1.Container": {
        type: "object",
        properties: {
          name: { type: "string" },
          image: { type: "string" },
          imagePullPolicy: { type: "string", enum: ["Always", "Never", "IfNotPresent"] },
          command: { type: "array", items: { type: "string" } },
          args: { type: "array", items: { type: "string" } },
          workingDir: { type: "string" },
          ports: {
            type: "array",
            items: { $ref: "#/components/schemas/io.k8s.api.core.v1.ContainerPort" },
          },
          env: {
            type: "array",
            items: { $ref: "#/components/schemas/io.k8s.api.core.v1.EnvVar" },
          },
          envFrom: {
            type: "array",
            items: { $ref: "#/components/schemas/io.k8s.api.core.v1.EnvFromSource" },
          },
          resources: {
            allOf: [{ $ref: "#/components/schemas/io.k8s.api.core.v1.ResourceRequirements" }],
          },
          volumeMounts: {
            type: "array",
            items: { $ref: "#/components/schemas/io.k8s.api.core.v1.VolumeMount" },
          },
          livenessProbe: { allOf: [{ $ref: "#/components/schemas/io.k8s.api.core.v1.Probe" }] },
          readinessProbe: { allOf: [{ $ref: "#/components/schemas/io.k8s.api.core.v1.Probe" }] },
          startupProbe: { allOf: [{ $ref: "#/components/schemas/io.k8s.api.core.v1.Probe" }] },
          lifecycle: { allOf: [{ $ref: "#/components/schemas/io.k8s.api.core.v1.Lifecycle" }] },
          terminationMessagePath: { type: "string" },
          terminationMessagePolicy: { type: "string" },
          tty: { type: "boolean" },
          stdin: { type: "boolean" },
          stdinOnce: { type: "boolean" },
        },
        required: ["name"],
      },
      // Pod-level admin shapes used to validate the `advanced` flag.
      // These are intentionally minimal — we just need the FILTER
      // to surface them and the WALKER to flag them as advanced.
      "io.k8s.api.core.v1.PodSecurityContext": {
        type: "object",
        properties: {
          runAsUser: { type: "integer" },
          runAsGroup: { type: "integer" },
          fsGroup: { type: "integer" },
          runAsNonRoot: { type: "boolean" },
        },
      },
      "io.k8s.api.core.v1.Toleration": {
        type: "object",
        properties: {
          key: { type: "string" },
          operator: { type: "string", enum: ["Exists", "Equal"] },
          value: { type: "string" },
          effect: { type: "string", enum: ["NoSchedule", "PreferNoSchedule", "NoExecute"] },
          tolerationSeconds: { type: "integer" },
        },
      },
      "io.k8s.api.core.v1.TopologySpreadConstraint": {
        type: "object",
        properties: {
          maxSkew: { type: "integer" },
          topologyKey: { type: "string" },
          whenUnsatisfiable: { type: "string", enum: ["DoNotSchedule", "ScheduleAnyway"] },
        },
      },
      "io.k8s.api.core.v1.Affinity": {
        type: "object",
        properties: {
          nodeAffinity: { type: "object" },
          podAffinity: { type: "object" },
          podAntiAffinity: { type: "object" },
        },
      },
      "io.k8s.api.core.v1.PodSpec": {
        type: "object",
        properties: {
          restartPolicy: { type: "string", enum: ["Always", "OnFailure", "Never"] },
          serviceAccountName: { type: "string" },
          nodeSelector: { type: "object", additionalProperties: { type: "string" } },
          terminationGracePeriodSeconds: { type: "integer" },
          containers: {
            type: "array",
            items: { $ref: "#/components/schemas/io.k8s.api.core.v1.Container" },
          },
          volumes: {
            type: "array",
            items: { $ref: "#/components/schemas/io.k8s.api.core.v1.Volume" },
          },
          initContainers: {
            type: "array",
            items: { $ref: "#/components/schemas/io.k8s.api.core.v1.Container" },
          },
          // Pod-level admin surfaces — allowlisted as `advanced`.
          securityContext: {
            allOf: [{ $ref: "#/components/schemas/io.k8s.api.core.v1.PodSecurityContext" }],
          },
          affinity: { allOf: [{ $ref: "#/components/schemas/io.k8s.api.core.v1.Affinity" }] },
          tolerations: {
            type: "array",
            items: { $ref: "#/components/schemas/io.k8s.api.core.v1.Toleration" },
          },
          topologySpreadConstraints: {
            type: "array",
            items: { $ref: "#/components/schemas/io.k8s.api.core.v1.TopologySpreadConstraint" },
          },
        },
        required: ["containers"],
      },
      "io.k8s.api.core.v1.PodTemplateSpec": {
        type: "object",
        properties: {
          metadata: {
            allOf: [
              { $ref: "#/components/schemas/io.k8s.apimachinery.pkg.apis.meta.v1.ObjectMeta" },
            ],
          },
          spec: {
            allOf: [{ $ref: "#/components/schemas/io.k8s.api.core.v1.PodSpec" }],
          },
        },
      },
      "io.k8s.api.apps.v1.RollingUpdateDeployment": {
        type: "object",
        properties: {
          maxUnavailable: { type: "string", format: "int-or-string" },
          maxSurge: { type: "string", format: "int-or-string" },
        },
      },
      "io.k8s.api.apps.v1.DeploymentStrategy": {
        type: "object",
        properties: {
          type: { type: "string", enum: ["Recreate", "RollingUpdate"] },
          rollingUpdate: {
            allOf: [
              { $ref: "#/components/schemas/io.k8s.api.apps.v1.RollingUpdateDeployment" },
            ],
          },
        },
      },
      "io.k8s.api.apps.v1.DeploymentSpec": {
        type: "object",
        properties: {
          replicas: { type: "integer" },
          minReadySeconds: { type: "integer" },
          revisionHistoryLimit: { type: "integer" },
          progressDeadlineSeconds: { type: "integer" },
          paused: { type: "boolean" },
          selector: {
            allOf: [
              { $ref: "#/components/schemas/io.k8s.apimachinery.pkg.apis.meta.v1.LabelSelector" },
            ],
          },
          strategy: {
            allOf: [{ $ref: "#/components/schemas/io.k8s.api.apps.v1.DeploymentStrategy" }],
          },
          template: {
            allOf: [{ $ref: "#/components/schemas/io.k8s.api.core.v1.PodTemplateSpec" }],
          },
        },
        required: ["selector", "template"],
      },
      "io.k8s.api.apps.v1.Deployment": {
        type: "object",
        "x-kubernetes-group-version-kind": [
          { group: "apps", version: "v1", kind: "Deployment" },
        ],
        properties: {
          apiVersion: { type: "string" },
          kind: { type: "string" },
          metadata: {
            allOf: [
              { $ref: "#/components/schemas/io.k8s.apimachinery.pkg.apis.meta.v1.ObjectMeta" },
            ],
          },
          spec: {
            allOf: [{ $ref: "#/components/schemas/io.k8s.api.apps.v1.DeploymentSpec" }],
          },
          status: { type: "object" },
        },
      },
      // ── StatefulSet shapes ────────────────────────────────────
      // Reuses the Deployment-side PodSpec / Container / Volume /
      // ObjectMeta etc. wholesale — only the StatefulSet-specific
      // wrappers are modelled here.
      "io.k8s.api.apps.v1.RollingUpdateStatefulSetStrategy": {
        type: "object",
        properties: {
          partition: { type: "integer" },
          maxUnavailable: { type: "string", format: "int-or-string" },
        },
      },
      "io.k8s.api.apps.v1.StatefulSetUpdateStrategy": {
        type: "object",
        properties: {
          type: { type: "string", enum: ["RollingUpdate", "OnDelete"] },
          rollingUpdate: {
            allOf: [
              {
                $ref:
                  "#/components/schemas/io.k8s.api.apps.v1.RollingUpdateStatefulSetStrategy",
              },
            ],
          },
        },
      },
      "io.k8s.api.apps.v1.StatefulSetPersistentVolumeClaimRetentionPolicy": {
        type: "object",
        properties: {
          whenDeleted: { type: "string", enum: ["Retain", "Delete"] },
          whenScaled: { type: "string", enum: ["Retain", "Delete"] },
        },
      },
      "io.k8s.api.apps.v1.StatefulSetOrdinals": {
        type: "object",
        properties: { start: { type: "integer" } },
      },
      "io.k8s.api.core.v1.PersistentVolumeClaimSpec": {
        type: "object",
        properties: {
          accessModes: { type: "array", items: { type: "string" } },
          storageClassName: { type: "string" },
          volumeMode: { type: "string", enum: ["Filesystem", "Block"] },
          resources: {
            allOf: [{ $ref: "#/components/schemas/io.k8s.api.core.v1.ResourceRequirements" }],
          },
        },
      },
      "io.k8s.api.core.v1.PersistentVolumeClaim": {
        type: "object",
        properties: {
          metadata: {
            allOf: [
              { $ref: "#/components/schemas/io.k8s.apimachinery.pkg.apis.meta.v1.ObjectMeta" },
            ],
          },
          spec: {
            allOf: [
              { $ref: "#/components/schemas/io.k8s.api.core.v1.PersistentVolumeClaimSpec" },
            ],
          },
        },
      },
      "io.k8s.api.apps.v1.StatefulSetSpec": {
        type: "object",
        properties: {
          replicas: { type: "integer" },
          serviceName: { type: "string" },
          podManagementPolicy: { type: "string", enum: ["OrderedReady", "Parallel"] },
          minReadySeconds: { type: "integer" },
          revisionHistoryLimit: { type: "integer" },
          selector: {
            allOf: [
              { $ref: "#/components/schemas/io.k8s.apimachinery.pkg.apis.meta.v1.LabelSelector" },
            ],
          },
          template: {
            allOf: [{ $ref: "#/components/schemas/io.k8s.api.core.v1.PodTemplateSpec" }],
          },
          updateStrategy: {
            allOf: [{ $ref: "#/components/schemas/io.k8s.api.apps.v1.StatefulSetUpdateStrategy" }],
          },
          volumeClaimTemplates: {
            type: "array",
            items: { $ref: "#/components/schemas/io.k8s.api.core.v1.PersistentVolumeClaim" },
          },
          persistentVolumeClaimRetentionPolicy: {
            allOf: [
              {
                $ref:
                  "#/components/schemas/io.k8s.api.apps.v1.StatefulSetPersistentVolumeClaimRetentionPolicy",
              },
            ],
          },
          ordinals: {
            allOf: [{ $ref: "#/components/schemas/io.k8s.api.apps.v1.StatefulSetOrdinals" }],
          },
        },
        required: ["selector", "template", "serviceName"],
      },
      "io.k8s.api.apps.v1.StatefulSet": {
        type: "object",
        "x-kubernetes-group-version-kind": [
          { group: "apps", version: "v1", kind: "StatefulSet" },
        ],
        properties: {
          apiVersion: { type: "string" },
          kind: { type: "string" },
          metadata: {
            allOf: [
              { $ref: "#/components/schemas/io.k8s.apimachinery.pkg.apis.meta.v1.ObjectMeta" },
            ],
          },
          spec: {
            allOf: [{ $ref: "#/components/schemas/io.k8s.api.apps.v1.StatefulSetSpec" }],
          },
          status: { type: "object" },
        },
      },
    },
  },
} as unknown as OpenAPIDoc;

const walkOptionsFor = (kind: SupportedKind) => ({
  resolveRef: buildRefResolver(synthDoc),
  allowKvMap: true,
  allowArrayOfObjects: true,
  allowOneOfDiscriminator: true,
  // Hint table is K8s-wide — same wiring K8sSchemaForm uses. Helm
  // and CRD callers leave this off.
  discriminatorHints: buildK8sDiscriminatorHints(),
  createOnlyPaths: getCreateOnlyPaths(kind),
  advancedPaths: getAdvancedPaths(kind),
});

const schemaFor = (kind: SupportedKind): JSONSchema => {
  const gvk = getKindGVK(kind);
  const root = findSchemaByGVK(synthDoc, gvk.group, gvk.version, gvk.kind);
  if (!root) throw new Error(`no schema for ${kind}`);
  // Pass the same resolver into the filter so it can narrow inside
  // K8s `{allOf:[{$ref: ...}]}` envelopes — without it the filter
  // can't peek at metadata or spec sub-fields.
  return filterSchemaForKind(root, kind, { resolveRef: buildRefResolver(synthDoc) });
};

const flatten = (descriptors: FieldDescriptor[]): FieldDescriptor[] => {
  const out: FieldDescriptor[] = [];
  const visit = (ds: FieldDescriptor[]) => {
    for (const d of ds) {
      out.push(d);
      if (d.children) visit(d.children);
    }
  };
  visit(descriptors);
  return out;
};

const yamlRoundTrip = (yaml: string): unknown => {
  // Simulates SchemaFormBridge: parse, then re-stringify, then
  // re-parse. The intermediate stringify is the form-mode write.
  // schema: "yaml-1.1" matches what SchemaFormBridge passes — see
  // that file for the rationale (off/on/yes/no must round-trip as
  // strings, not get re-emitted unquoted and then parsed by the
  // K8s apiserver as booleans).
  const obj = parseYaml(yaml);
  const re = stringifyYaml(obj, { lineWidth: 0, schema: "yaml-1.1" });
  return parseYaml(re);
};

describe("ConfigMap — round-trip", () => {
  const yaml = [
    "apiVersion: v1",
    "kind: ConfigMap",
    "metadata:",
    "  name: app-config",
    "  namespace: default",
    "  labels:",
    "    team: platform",
    "data:",
    "  DATABASE_URL: postgres://db:5432/app",
    "  LOG_LEVEL: info",
    "immutable: false",
    "",
  ].join("\n");

  it("walker emits descriptors covering the operator-facing fields", () => {
    const all = flatten(buildFieldDescriptors(schemaFor("ConfigMap"), walkOptionsFor("ConfigMap")));
    const paths = all.map((d) => d.path.join("."));
    expect(paths).toEqual(expect.arrayContaining(["metadata", "metadata.name", "data", "immutable"]));
    const dataDesc = all.find((d) => d.path.join(".") === "data");
    expect(dataDesc?.type).toBe("kv-map");
    const nameDesc = all.find((d) => d.path.join(".") === "metadata.name");
    expect(nameDesc?.editable).toBe("create-only");
  });

  it("parse → stringify → parse is structurally idempotent", () => {
    expect(yamlRoundTrip(yaml)).toEqual(parseYaml(yaml));
  });

  it("editing data[k] via setAtPath preserves siblings", () => {
    const obj = parseYaml(yaml) as Record<string, unknown>;
    const next = setAtPath(obj, ["data", "DATABASE_URL"], "postgres://newhost/app");
    const data = getAtPath(next, ["data"]) as Record<string, string>;
    expect(data.DATABASE_URL).toBe("postgres://newhost/app");
    expect(data.LOG_LEVEL).toBe("info");
    expect(getAtPath(next, ["metadata", "name"])).toBe("app-config");
  });
});

describe("Secret — round-trip", () => {
  const yaml = [
    "apiVersion: v1",
    "kind: Secret",
    "metadata:",
    "  name: db-creds",
    "type: Opaque",
    "data:",
    "  password: aGVsbG8td29ybGQ=",
    "",
  ].join("\n");

  it("walker emits a kv-map descriptor for data", () => {
    const all = flatten(buildFieldDescriptors(schemaFor("Secret"), walkOptionsFor("Secret")));
    const dataDesc = all.find((d) => d.path.join(".") === "data");
    expect(dataDesc?.type).toBe("kv-map");
    expect(dataDesc?.kvValueType).toBe("string");
  });

  it("structure preserved through round-trip", () => {
    expect(yamlRoundTrip(yaml)).toEqual(parseYaml(yaml));
  });
});

describe("Service — round-trip", () => {
  const yaml = [
    "apiVersion: v1",
    "kind: Service",
    "metadata:",
    "  name: api",
    "  namespace: default",
    "spec:",
    "  type: ClusterIP",
    "  selector:",
    "    app: api",
    "  ports:",
    "    - name: http",
    "      port: 80",
    "      targetPort: 8080",
    "      protocol: TCP",
    "    - name: metrics",
    "      port: 9090",
    "      targetPort: 9090",
    "      protocol: TCP",
    "",
  ].join("\n");

  it("walker emits array-of-objects for spec.ports with relative-path children", () => {
    const all = flatten(buildFieldDescriptors(schemaFor("Service"), walkOptionsFor("Service")));
    const ports = all.find((d) => d.path.join(".") === "spec.ports");
    expect(ports?.type).toBe("array-of-objects");
    const portChildPaths = (ports?.children ?? []).map((c) => c.path.join("."));
    expect(portChildPaths).toEqual(expect.arrayContaining(["name", "port", "protocol"]));
  });

  it("walker emits select-style enum for spec.type", () => {
    const all = flatten(buildFieldDescriptors(schemaFor("Service"), walkOptionsFor("Service")));
    const typeDesc = all.find((d) => d.path.join(".") === "spec.type");
    expect(typeDesc?.type).toBe("string");
    expect(typeDesc?.enum).toEqual(
      expect.arrayContaining(["ClusterIP", "NodePort", "LoadBalancer"]),
    );
  });

  it("walker flags spec.clusterIP as create-only", () => {
    const all = flatten(buildFieldDescriptors(schemaFor("Service"), walkOptionsFor("Service")));
    const clusterIP = all.find((d) => d.path.join(".") === "spec.clusterIP");
    expect(clusterIP?.editable).toBe("create-only");
  });

  it("structure preserved through round-trip including nested ports[]", () => {
    const before = parseYaml(yaml) as Record<string, unknown>;
    const after = yamlRoundTrip(yaml) as Record<string, unknown>;
    expect(after).toEqual(before);
    const ports = getAtPath(after, ["spec", "ports"]) as unknown[];
    expect(ports).toHaveLength(2);
  });

  it("editing a port row via setAtPath preserves siblings", () => {
    const obj = parseYaml(yaml) as Record<string, unknown>;
    const ports = getAtPath(obj, ["spec", "ports"]) as Record<string, unknown>[];
    const updatedPorts = [...ports];
    updatedPorts[0] = { ...updatedPorts[0], port: 8081 };
    const next = setAtPath(obj, ["spec", "ports"], updatedPorts);
    expect((getAtPath(next, ["spec", "ports"]) as Record<string, unknown>[])[0].port).toBe(8081);
    expect((getAtPath(next, ["spec", "ports"]) as Record<string, unknown>[])[1].port).toBe(9090);
    expect(getAtPath(next, ["spec", "selector", "app"])).toBe("api");
  });
});

describe("Ingress — round-trip", () => {
  const yaml = [
    "apiVersion: networking.k8s.io/v1",
    "kind: Ingress",
    "metadata:",
    "  name: api",
    "  annotations:",
    "    nginx.ingress.kubernetes.io/rewrite-target: /",
    "spec:",
    "  ingressClassName: nginx",
    "  rules:",
    "    - host: api.example.com",
    "      paths:",
    "        - path: /",
    "          pathType: Prefix",
    "  tls:",
    "    - hosts:",
    "        - api.example.com",
    "      secretName: api-tls",
    "",
  ].join("\n");

  it("walker emits array-of-objects for rules[] and tls[]", () => {
    const all = flatten(buildFieldDescriptors(schemaFor("Ingress"), walkOptionsFor("Ingress")));
    const rules = all.find((d) => d.path.join(".") === "spec.rules");
    const tls = all.find((d) => d.path.join(".") === "spec.tls");
    expect(rules?.type).toBe("array-of-objects");
    expect(tls?.type).toBe("array-of-objects");
  });

  it("walker passes controller-specific annotations through as kv-map (no validation)", () => {
    const all = flatten(buildFieldDescriptors(schemaFor("Ingress"), walkOptionsFor("Ingress")));
    const annotations = all.find((d) => d.path.join(".") === "metadata.annotations");
    expect(annotations?.type).toBe("kv-map");
  });

  it("structure preserved through round-trip", () => {
    expect(yamlRoundTrip(yaml)).toEqual(parseYaml(yaml));
  });
});

describe("Deployment — round-trip", () => {
  const yaml = [
    "apiVersion: apps/v1",
    "kind: Deployment",
    "metadata:",
    "  name: api",
    "  namespace: default",
    "  labels:",
    "    app: api",
    "spec:",
    "  replicas: 3",
    "  selector:",
    "    matchLabels:",
    "      app: api",
    "  strategy:",
    "    type: RollingUpdate",
    "    rollingUpdate:",
    "      maxUnavailable: 1",
    "      maxSurge: 25%",
    "  template:",
    "    metadata:",
    "      labels:",
    "        app: api",
    "    spec:",
    "      restartPolicy: Always",
    "      serviceAccountName: api-sa",
    "      containers:",
    "        - name: api",
    "          image: ghcr.io/example/api:1.0.0",
    "          imagePullPolicy: IfNotPresent",
    "          ports:",
    "            - name: http",
    "              containerPort: 8080",
    "              protocol: TCP",
    "          env:",
    "            - name: LOG_LEVEL",
    "              value: info",
    "          resources:",
    "            requests:",
    "              cpu: 100m",
    "              memory: 128Mi",
    "            limits:",
    "              cpu: 500m",
    "              memory: 512Mi",
    "",
  ].join("\n");

  it("walker covers the curated PodSpec subset (replicas, selector, strategy, template basics, containers[*])", () => {
    const all = flatten(buildFieldDescriptors(schemaFor("Deployment"), walkOptionsFor("Deployment")));
    const paths = all.map((d) => d.path.join("."));
    expect(paths).toEqual(
      expect.arrayContaining([
        "metadata",
        "metadata.name",
        "metadata.namespace",
        "metadata.labels",
        "spec.replicas",
        "spec.selector",
        "spec.strategy.type",
        "spec.strategy.rollingUpdate",
        "spec.template.metadata.labels",
        "spec.template.spec.restartPolicy",
        "spec.template.spec.serviceAccountName",
        "spec.template.spec.containers",
      ]),
    );
  });

  it("walker surfaces initContainers as array-of-objects (now that per-row collapse keeps them scannable)", () => {
    const all = flatten(buildFieldDescriptors(schemaFor("Deployment"), walkOptionsFor("Deployment")));
    const initContainers = all.find((d) => d.path.join(".") === "spec.template.spec.initContainers");
    expect(initContainers?.type).toBe("array-of-objects");
    const childPaths = (initContainers?.children ?? []).map((c) => c.path.join("."));
    expect(childPaths).toEqual(expect.arrayContaining(["name", "image", "command", "args"]));
  });

  it("walker prunes ObjectMeta noise (uid, creationTimestamp, managedFields) at metadata and spec.template.metadata", () => {
    const all = flatten(buildFieldDescriptors(schemaFor("Deployment"), walkOptionsFor("Deployment")));
    const paths = all.map((d) => d.path.join("."));
    expect(paths).not.toContain("metadata.uid");
    expect(paths).not.toContain("metadata.creationTimestamp");
    expect(paths).not.toContain("metadata.managedFields");
    expect(paths).not.toContain("spec.template.metadata.uid");
    expect(paths).not.toContain("spec.template.metadata.managedFields");
  });

  it("walker emits array-of-objects for containers[] with relative-path children narrowed to the curated set", () => {
    const all = flatten(buildFieldDescriptors(schemaFor("Deployment"), walkOptionsFor("Deployment")));
    const containers = all.find((d) => d.path.join(".") === "spec.template.spec.containers");
    expect(containers?.type).toBe("array-of-objects");
    const childPaths = (containers?.children ?? []).map((c) => c.path.join("."));
    expect(childPaths).toEqual(
      expect.arrayContaining([
        "name",
        "image",
        "imagePullPolicy",
        "command",
        "args",
        "ports",
        "env",
        "resources",
        // Hinted discriminators — re-enabled now that the K8s
        // hint table renders them as proper Shape B pickers.
        "livenessProbe",
        "readinessProbe",
        "startupProbe",
        "lifecycle",
      ]),
    );
    // volumeMounts joined the curated set alongside volumes (now
    // that array-of-discriminators handles volumes). securityContext
    // stays out — wide admin-leaning surface, pending the
    // collapsible "advanced" affordance.
    expect(childPaths).toContain("volumeMounts");
    expect(childPaths).not.toContain("securityContext");
  });

  it("walker emits kv-maps for resources.requests/limits (Quantity values render as strings)", () => {
    const all = flatten(buildFieldDescriptors(schemaFor("Deployment"), walkOptionsFor("Deployment")));
    const containers = all.find((d) => d.path.join(".") === "spec.template.spec.containers");
    const resources = containers?.children?.find((c) => c.path.join(".") === "resources");
    const requests = resources?.children?.find((c) => c.path.join(".") === "resources.requests");
    expect(requests?.type).toBe("kv-map");
    expect(requests?.kvValueType).toBe("string");
  });

  it("walker flags spec.selector as create-only (immutable on Deployment)", () => {
    const all = flatten(buildFieldDescriptors(schemaFor("Deployment"), walkOptionsFor("Deployment")));
    const selector = all.find((d) => d.path.join(".") === "spec.selector");
    expect(selector?.editable).toBe("create-only");
  });

  it("structure preserved through round-trip including containers[]", () => {
    const before = parseYaml(yaml) as Record<string, unknown>;
    const after = yamlRoundTrip(yaml) as Record<string, unknown>;
    expect(after).toEqual(before);
    const containers = getAtPath(after, ["spec", "template", "spec", "containers"]) as unknown[];
    expect(containers).toHaveLength(1);
  });

  it("editing containers[0].image via setAtPath preserves siblings", () => {
    const obj = parseYaml(yaml) as Record<string, unknown>;
    const containers = getAtPath(obj, ["spec", "template", "spec", "containers"]) as Record<
      string,
      unknown
    >[];
    const updated = [...containers];
    updated[0] = { ...updated[0], image: "ghcr.io/example/api:2.0.0" };
    const next = setAtPath(obj, ["spec", "template", "spec", "containers"], updated);
    const after = getAtPath(next, ["spec", "template", "spec", "containers"]) as Record<
      string,
      unknown
    >[];
    expect(after[0].image).toBe("ghcr.io/example/api:2.0.0");
    expect(after[0].name).toBe("api");
    expect(getAtPath(next, ["spec", "replicas"])).toBe(3);
    expect(getAtPath(next, ["metadata", "name"])).toBe("api");
  });

  // ── hinted discriminators (K8s sibling-encoded oneOfs) ─────────

  it("livenessProbe / readinessProbe / startupProbe surface as hinted discriminators inside a container", () => {
    const all = flatten(buildFieldDescriptors(schemaFor("Deployment"), walkOptionsFor("Deployment")));
    const containers = all.find((d) => d.path.join(".") === "spec.template.spec.containers");
    const liveness = containers?.children?.find((c) => c.path.join(".") === "livenessProbe");
    expect(liveness?.type).toBe("discriminator");
    const branchKeys = (liveness?.branches ?? []).map((b) => b.discriminatorKey).sort();
    expect(branchKeys).toEqual(["exec", "grpc", "httpGet", "tcpSocket"]);

    // Threshold knobs come through as sharedChildren (rendered
    // alongside the picker in DiscriminatorInput).
    const sharedPaths = (liveness?.sharedChildren ?? []).map((d) => d.path.join("."));
    expect(sharedPaths).toEqual(
      expect.arrayContaining(["initialDelaySeconds", "periodSeconds", "failureThreshold"]),
    );
  });

  it("lifecycle.preStop / postStart each surface as hinted discriminators (LifecycleHandler)", () => {
    const all = flatten(buildFieldDescriptors(schemaFor("Deployment"), walkOptionsFor("Deployment")));
    const containers = all.find((d) => d.path.join(".") === "spec.template.spec.containers");
    const lifecycle = containers?.children?.find((c) => c.path.join(".") === "lifecycle");
    expect(lifecycle?.type).toBe("object");
    const preStop = lifecycle?.children?.find((c) => c.path.join(".") === "lifecycle.preStop");
    const postStart = lifecycle?.children?.find((c) => c.path.join(".") === "lifecycle.postStart");
    expect(preStop?.type).toBe("discriminator");
    expect(postStart?.type).toBe("discriminator");
    const branchKeys = (preStop?.branches ?? []).map((b) => b.discriminatorKey).sort();
    expect(branchKeys).toEqual(["exec", "httpGet", "sleep", "tcpSocket"]);
  });

  it("spec.template.spec.volumes surfaces as array-of-discriminators (per-row volume-type picker)", () => {
    const all = flatten(buildFieldDescriptors(schemaFor("Deployment"), walkOptionsFor("Deployment")));
    const volumes = all.find((d) => d.path.join(".") === "spec.template.spec.volumes");
    expect(volumes?.type).toBe("array-of-discriminators");
    const branchKeys = (volumes?.branches ?? []).map((b) => b.discriminatorKey);
    expect(branchKeys).toEqual(
      expect.arrayContaining(["configMap", "secret", "emptyDir"]),
    );
    // `name` is shared across all volume types — it's the volume's
    // identifier, not a branch.
    const sharedPaths = (volumes?.sharedChildren ?? []).map((d) => d.path.join("."));
    expect(sharedPaths).toContain("name");
  });

  it("container envFrom surfaces as array-of-discriminators (per-row configMap-or-secret)", () => {
    const all = flatten(buildFieldDescriptors(schemaFor("Deployment"), walkOptionsFor("Deployment")));
    const containers = all.find((d) => d.path.join(".") === "spec.template.spec.containers");
    const envFrom = containers?.children?.find((c) => c.path.join(".") === "envFrom");
    expect(envFrom?.type).toBe("array-of-discriminators");
    const branchKeys = (envFrom?.branches ?? []).map((b) => b.discriminatorKey).sort();
    expect(branchKeys).toEqual(["configMapRef", "secretRef"]);
    const sharedPaths = (envFrom?.sharedChildren ?? []).map((d) => d.path.join("."));
    expect(sharedPaths).toContain("prefix");
  });

  it("container volumeMounts surfaces as plain array-of-objects (no hint, no polymorphism)", () => {
    const all = flatten(buildFieldDescriptors(schemaFor("Deployment"), walkOptionsFor("Deployment")));
    const containers = all.find((d) => d.path.join(".") === "spec.template.spec.containers");
    const volumeMounts = containers?.children?.find((c) => c.path.join(".") === "volumeMounts");
    expect(volumeMounts?.type).toBe("array-of-objects");
    const childPaths = (volumeMounts?.children ?? []).map((d) => d.path.join("."));
    expect(childPaths).toEqual(
      expect.arrayContaining(["name", "mountPath", "readOnly", "subPath"]),
    );
  });

  it("volumes round-trip through YAML preserving the chosen branch + name", () => {
    const volumesYaml = [
      "apiVersion: apps/v1",
      "kind: Deployment",
      "metadata:",
      "  name: api",
      "spec:",
      "  selector:",
      "    matchLabels:",
      "      app: api",
      "  template:",
      "    metadata:",
      "      labels:",
      "        app: api",
      "    spec:",
      "      volumes:",
      "        - name: config",
      "          configMap:",
      "            name: app-config",
      "        - name: scratch",
      "          emptyDir: {}",
      "      containers:",
      "        - name: api",
      "          image: ghcr.io/example/api:1.0.0",
      "          volumeMounts:",
      "            - name: config",
      "              mountPath: /etc/app",
      "              readOnly: true",
      "            - name: scratch",
      "              mountPath: /tmp",
      "",
    ].join("\n");
    expect(yamlRoundTrip(volumesYaml)).toEqual(parseYaml(volumesYaml));
  });

  // ── advanced flag (collapsed-by-default in renderer) ───────────

  it("pod-level securityContext / affinity / tolerations / topologySpreadConstraints surface AND are flagged advanced", () => {
    const all = flatten(buildFieldDescriptors(schemaFor("Deployment"), walkOptionsFor("Deployment")));
    const advancedExpected = [
      "spec.template.spec.securityContext",
      "spec.template.spec.affinity",
      "spec.template.spec.tolerations",
      "spec.template.spec.topologySpreadConstraints",
    ];
    for (const dotted of advancedExpected) {
      const d = all.find((x) => x.path.join(".") === dotted);
      expect(d, `missing descriptor for ${dotted}`).toBeDefined();
      expect(d?.advanced, `${dotted} should be flagged advanced`).toBe(true);
    }
  });

  it("non-advanced Deployment fields stay un-flagged (replicas, selector, containers)", () => {
    const all = flatten(buildFieldDescriptors(schemaFor("Deployment"), walkOptionsFor("Deployment")));
    const replicas = all.find((d) => d.path.join(".") === "spec.replicas");
    const selector = all.find((d) => d.path.join(".") === "spec.selector");
    const containers = all.find((d) => d.path.join(".") === "spec.template.spec.containers");
    expect(replicas?.advanced).toBeUndefined();
    expect(selector?.advanced).toBeUndefined();
    expect(containers?.advanced).toBeUndefined();
  });

  it("hinted Probe round-trips through YAML preserving handler + threshold values", () => {
    const probedYaml = [
      "apiVersion: apps/v1",
      "kind: Deployment",
      "metadata:",
      "  name: api",
      "spec:",
      "  selector:",
      "    matchLabels:",
      "      app: api",
      "  template:",
      "    metadata:",
      "      labels:",
      "        app: api",
      "    spec:",
      "      containers:",
      "        - name: api",
      "          image: ghcr.io/example/api:1.0.0",
      "          livenessProbe:",
      "            httpGet:",
      "              path: /healthz",
      "              port: 8080",
      "              scheme: HTTP",
      "            initialDelaySeconds: 5",
      "            periodSeconds: 10",
      "",
    ].join("\n");
    expect(yamlRoundTrip(probedYaml)).toEqual(parseYaml(probedYaml));
  });
});

describe("StatefulSet — round-trip", () => {
  const yaml = [
    "apiVersion: apps/v1",
    "kind: StatefulSet",
    "metadata:",
    "  name: db",
    "  namespace: default",
    "spec:",
    "  replicas: 3",
    "  serviceName: db-headless",
    "  podManagementPolicy: OrderedReady",
    "  selector:",
    "    matchLabels:",
    "      app: db",
    "  updateStrategy:",
    "    type: RollingUpdate",
    "    rollingUpdate:",
    "      partition: 0",
    "  volumeClaimTemplates:",
    "    - metadata:",
    "        name: data",
    "      spec:",
    "        accessModes:",
    "          - ReadWriteOnce",
    "        storageClassName: standard",
    "        resources:",
    "          requests:",
    "            storage: 10Gi",
    "  template:",
    "    metadata:",
    "      labels:",
    "        app: db",
    "    spec:",
    "      containers:",
    "        - name: db",
    "          image: postgres:16",
    "          ports:",
    "            - name: pg",
    "              containerPort: 5432",
    "          volumeMounts:",
    "            - name: data",
    "              mountPath: /var/lib/postgresql/data",
    "",
  ].join("\n");

  it("walker covers StatefulSet-specific fields (serviceName, podManagementPolicy, updateStrategy, volumeClaimTemplates)", () => {
    const all = flatten(buildFieldDescriptors(schemaFor("StatefulSet"), walkOptionsFor("StatefulSet")));
    const paths = all.map((d) => d.path.join("."));
    expect(paths).toEqual(
      expect.arrayContaining([
        "metadata.name",
        "spec.replicas",
        "spec.serviceName",
        "spec.podManagementPolicy",
        "spec.selector",
        "spec.updateStrategy.type",
        "spec.updateStrategy.rollingUpdate",
        "spec.volumeClaimTemplates",
        "spec.template.spec.containers",
      ]),
    );
  });

  it("walker reuses the same PodSpec curated subset as Deployment (containers, initContainers via array-of-objects; volumes via array-of-discriminators)", () => {
    const all = flatten(buildFieldDescriptors(schemaFor("StatefulSet"), walkOptionsFor("StatefulSet")));
    const containers = all.find((d) => d.path.join(".") === "spec.template.spec.containers");
    const volumes = all.find((d) => d.path.join(".") === "spec.template.spec.volumes");
    const initContainers = all.find((d) => d.path.join(".") === "spec.template.spec.initContainers");
    expect(containers?.type).toBe("array-of-objects");
    expect(volumes?.type).toBe("array-of-discriminators");
    expect(initContainers?.type).toBe("array-of-objects");
  });

  it("walker flags StatefulSet immutable fields as create-only (selector, serviceName, podManagementPolicy, volumeClaimTemplates)", () => {
    const all = flatten(buildFieldDescriptors(schemaFor("StatefulSet"), walkOptionsFor("StatefulSet")));
    for (const dotted of [
      "spec.selector",
      "spec.serviceName",
      "spec.podManagementPolicy",
      "spec.volumeClaimTemplates",
    ]) {
      const d = all.find((x) => x.path.join(".") === dotted);
      expect(d, `missing descriptor for ${dotted}`).toBeDefined();
      expect(d?.editable, `${dotted} should be create-only`).toBe("create-only");
    }
  });

  it("walker emits updateStrategy.type as a RollingUpdate|OnDelete enum (StatefulSet-specific values)", () => {
    const all = flatten(buildFieldDescriptors(schemaFor("StatefulSet"), walkOptionsFor("StatefulSet")));
    const strategyType = all.find((d) => d.path.join(".") === "spec.updateStrategy.type");
    expect(strategyType?.type).toBe("string");
    expect(strategyType?.enum).toEqual(expect.arrayContaining(["RollingUpdate", "OnDelete"]));
  });

  it("walker emits volumeClaimTemplates as array-of-objects with curated PVC subset (metadata.name + spec.{accessModes, storageClassName, resources, volumeMode})", () => {
    const all = flatten(buildFieldDescriptors(schemaFor("StatefulSet"), walkOptionsFor("StatefulSet")));
    const vct = all.find((d) => d.path.join(".") === "spec.volumeClaimTemplates");
    expect(vct?.type).toBe("array-of-objects");
    const childPaths = (vct?.children ?? []).map((c) => c.path.join("."));
    expect(childPaths).toEqual(expect.arrayContaining(["metadata", "spec"]));
    const specChild = vct?.children?.find((c) => c.path.join(".") === "spec");
    const specChildPaths = (specChild?.children ?? []).map((c) => c.path.join("."));
    expect(specChildPaths).toEqual(
      expect.arrayContaining([
        "spec.accessModes",
        "spec.storageClassName",
        "spec.volumeMode",
        "spec.resources",
      ]),
    );
  });

  it("walker flags PVC retention policy + ordinals as advanced", () => {
    const all = flatten(buildFieldDescriptors(schemaFor("StatefulSet"), walkOptionsFor("StatefulSet")));
    const retention = all.find((d) => d.path.join(".") === "spec.persistentVolumeClaimRetentionPolicy");
    const ordinals = all.find((d) => d.path.join(".") === "spec.ordinals");
    expect(retention?.advanced).toBe(true);
    expect(ordinals?.advanced).toBe(true);
  });

  it("structure preserved through round-trip including updateStrategy + volumeClaimTemplates", () => {
    const before = parseYaml(yaml) as Record<string, unknown>;
    const after = yamlRoundTrip(yaml) as Record<string, unknown>;
    expect(after).toEqual(before);
    expect(getAtPath(after, ["spec", "serviceName"])).toBe("db-headless");
    const vct = getAtPath(after, ["spec", "volumeClaimTemplates"]) as unknown[];
    expect(vct).toHaveLength(1);
  });

  it("editing replicas via setAtPath preserves siblings (volumeClaimTemplates, template.spec.containers)", () => {
    const obj = parseYaml(yaml) as Record<string, unknown>;
    const next = setAtPath(obj, ["spec", "replicas"], 5);
    expect(getAtPath(next, ["spec", "replicas"])).toBe(5);
    expect(getAtPath(next, ["spec", "serviceName"])).toBe("db-headless");
    const containers = getAtPath(next, ["spec", "template", "spec", "containers"]) as Record<
      string,
      unknown
    >[];
    expect(containers[0].name).toBe("db");
  });
});

describe("YAML 1.1 boolean-keyword strings — regression", () => {
  // Real bug observed in production (rc-3 follow-up): operator added a
  // ConfigMap.data key with value "off". The default yaml-package
  // schema emitted it unquoted (`fuck: off`); the K8s apiserver
  // parsed that as the boolean `false` and rejected the apply with
  // "expected string, got valueUnstructured{Value:false}". The fix
  // is `schema: "yaml-1.1"` on stringify, which forces quoting of
  // these magic keywords on emit.
  //
  // YAML 1.1 boolean-like strings: y / Y / yes / Yes / YES / n / N
  // / no / No / NO / true / True / TRUE / false / False / FALSE /
  // on / On / ON / off / Off / OFF.
  const SAMPLES = [
    "off",
    "on",
    "yes",
    "no",
    "true",
    "false",
    "Off",
    "ON",
    "Yes",
    "y",
    "n",
  ];

  for (const sample of SAMPLES) {
    it(`preserves ${JSON.stringify(sample)} as a string through round-trip`, () => {
      const yaml = `data:\n  KEY: ${JSON.stringify(sample)}\n`;
      const result = yamlRoundTrip(yaml);
      expect(result).toEqual({ data: { KEY: sample } });
    });
  }
});
