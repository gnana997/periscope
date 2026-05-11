# CVE surfacing (Amazon Inspector v2)

Periscope shows per-pod and per-instance CVE counts inline on the
Pods / Nodes / Workloads / Karpenter pages, with a drill-down
**Security** tab on each detail pane that exposes the full finding
list (description, remediation, EPSS score, fix availability). Data
comes from Amazon Inspector v2; the SPA reads from a per-cluster
local cache the periscope-server hydrates on first request.

## Enable Inspector v2

CVE data is opt-in. Operators have to (1) turn on Inspector v2 in
the AWS account, (2) grant the periscope-server's Pod Identity /
IRSA role four `inspector2:*` permissions, and (3) flip the Helm
value:

```yaml
inspector:
  enabled: true
```

Full IAM block and the rationale for each permission are in
[cluster-rbac.md](../setup/cluster-rbac.md#aws-inspector-v2-optional-v11).

The first request after `inspector.enabled: true` blocks 10-30s
while Periscope hydrates the per-cluster cache from Inspector;
subsequent reads are served from memory at single-digit-ms latency.

When `inspector.enabled: false` (the default) or the IAM grant is
missing, every CVE-aware page shows a once-per-cluster hairline
banner — "Inspector v2 not enabled on this cluster" — and the
vulnerabilities columns render `not scanned`. Periscope never errors
out; the empty-state contract is the standard "no data" path.

## Read the row chip

The list-row chip uses a compact form: `2C · 5H · 12M`. Each letter
is the bucket: `C` critical, `H` high, `M` medium. Low and
informational are dropped from the inline view; hover the chip for
the full breakdown plus last-scanned timestamp.

States the chip can be in:

| State           | What it means                                                       |
|-----------------|---------------------------------------------------------------------|
| `2C · 5H · ...` | The entity has findings of those severities.                        |
| `clean`         | Scanned, zero findings.                                             |
| `partial scan`  | Some containers scanned, at least one not (mixed pod, see below).   |
| `non-ECR`       | The image is not in ECR. Inspector v2 doesn't cover non-ECR images. |
| `pending`       | ECR image, but the pod hasn't pulled it yet (no digest available).  |
| `not scanned`   | The cache hasn't covered this entity yet, or inspector is disabled. |

## Read the Security tab

Open any pod, node, deployment / statefulset / daemonset, or
Karpenter NodeClaim detail pane — the new `security` tab sits next
to `describe / yaml / events / logs`. A red dot on the tab label
means the entity has at least one critical finding; amber means at
least one high.

The tab content has three shapes:

- **Pod**: per-container groups. Each container shows its image,
  digest, scan state, and (when scanned ECR) an expandable finding
  list.
- **Node / NodeClaim**: flat finding list against the EC2 instance,
  with the instance id and owner badge at the top.
- **Deployment / StatefulSet / DaemonSet**: containers deduped
  across replicas (`× N pods` annotation) plus a per-pod replica
  chip list at the bottom — click a replica to jump straight to
  that pod's security tab.

Click any finding row to expand it. The expanded view shows the
prose description Inspector ships, the vendor-supplied remediation
text (and a deep link to the NVD / vendor advisory), first / last
observed timestamps, and a deep link to the AWS Inspector console
for the underlying finding.

## Refresh manually

Each Security tab carries a `↻ refresh` button at the top right.
Clicking it forces an immediate Inspector re-scan of the digests /
instance IDs in scope and writes one `cve_refresh` audit row. Use it
to verify a fix landed without waiting for the 6-hour TTL refresh.

The button is disabled if your role doesn't have the `cve_refresh`
audit verb. Pages don't carry a global refresh — that scope was
ambiguous (refresh which digests, which instances?) and the
entity-scoped button is the only refresh shape that landed in v1.1.

## What `partial scan` means

A pod with one ECR container plus a non-ECR sidecar (say
`docker.io/library/nginx`) reports `partial scan` coverage. The ECR
container is scanned and contributes to the rolled-up severity
counts; the sidecar shows `non-ECR · not scanned`. The pod-level
chip downgrades to `partial` so operators don't read the rollup as
"this pod is fully clean" when in fact one image was skipped by
Inspector entirely.

## What gets scanned, what doesn't

- ECR images Periscope can resolve to a `sha256:` digest from the
  pod's `containerStatus.imageID` field: scanned.
- ECR images not yet pulled by the pod (no digest): pending. The
  next refresh / TTL tick should resolve them.
- Non-ECR images (`docker.io`, `ghcr.io`, etc.): never scanned.
  Inspector v2's coverage is ECR-only.
- EC2 instances are scanned at the OS-package level via
  Inspector's instance coverage. Karpenter NodeClaims pre-
  `Initialized` (no providerID yet) are also `not scanned`.

## What isn't covered (yet)

- CronJob ownership chain is three-hop (Pod → Job → CronJob) and is
  not surfaced; this is tracked for v1.2.
- ReplicaSet and Job detail panes don't have a Security tab today
  because the SPA's detail-pane wiring for those resources is
  thin / non-existent. The backend already supports
  `/cve/by-workload/ReplicaSet/...` and `Job/...`; the tab will land
  if / when those panes mature.
- NodeGroup row-level chip: the SPA's nodegroup data doesn't include
  the instance ID list per group. Operators get per-node chips on
  the Nodes page and the per-instance Security tab on each node;
  the aggregate-by-group chip is a follow-up.

## Audit + cost

- Reads of any CVE endpoint do NOT emit audit rows — they're internal
  metadata reads. CloudTrail records the underlying Inspector API
  calls under the periscope-server's Pod Identity / IRSA role.
- Manual refresh emits exactly one `cve_refresh` audit row with the
  digests + instance IDs in the `Extra` field.
- Inspector v2 is **billed per scan** at the AWS account level. See
  [Inspector pricing](https://aws.amazon.com/inspector/pricing/).
  Periscope serves chip data from a 6h-TTL cache; the per-scan cost
  is the operator's call when they enable the feature.
