# AWS Access (Forward view + Reverse Lookup)

Periscope's AWS Access surface answers two operational questions about an
EKS-backed cluster without ever leaving the dashboard:

1. **Forward view** — *"What can this Pod / Deployment / ServiceAccount do
   in AWS?"* — surfaced as an **AWS access** tab on Pod, ServiceAccount,
   Deployment, StatefulSet, and DaemonSet detail panes.
2. **Reverse lookup** — *"Which Pods in this cluster can perform action X
   on resource Y?"* — surfaced as the **AWS reverse lookup** page under
   the EKS section of the cluster nav.

Both surfaces sit on top of the EKS Identity layer (#178: Access Entries,
aws-auth, Pod Identity, IRSA) and the IAM policy resolution engine (#187:
inline + attached managed policy fetch + parsing + wildcard matching).

## Forward view — AWS Access tab

For any workload of one of the supported kinds, the tab body is a single
backend call that composes:

- **Identity chain** — the resolved ServiceAccount and every IAM role
  bound to it. Each binding shows its source (`IRSA`, `PodIdentity`, or
  `Both`); when both are present Periscope renders a
  **DUAL_SOURCE_IRSA_SHADOWED** warning because Pod Identity wins at
  runtime and the IRSA annotation is dead config.
- **Service-grouped permissions** — every Statement from every attached
  policy, expanded into one row per `(action, resource)` and bucketed by
  AWS service (`s3`, `iam`, `kms`, …). Sensitive groups (any row
  flagged) sort to the top.
- **Sensitive-permission chips** against the locked v1.1 catalog (see
  *Sensitive-perms catalog* below). 18 chips total — operators cannot
  extend or suppress in v1.1.
- **Complex statements** — NotAction / NotResource / NotPrincipal cases
  render as a "see in IAM console" link rather than being silently
  mis-evaluated. The link is partition-aware (`aws`, `aws-us-gov`,
  `aws-cn`).
- **Affected pods** — for Deployment / STS / DS / ServiceAccount kinds,
  up to 5 currently-running pods using the resolved SA, with a total
  count. For Pod kind, the pod itself.

Endpoint:

```
GET /api/clusters/{cluster}/identity/workload-permissions
    ?kind=Pod|ServiceAccount|Deployment|StatefulSet|DaemonSet
    &namespace=N
    &name=X
```

Returns a `WorkloadPermissionsResponse` (see
`internal/awseks/iam/types.go`). Every join, dedup, grouping, and
classification is computed server-side so an MCP tool can wrap the
endpoint as one tool call.

## Reverse lookup

The page accepts an IAM action plus an optional resource ARN and
optional namespace filter. Sensitive-permission chips on the page
pre-fill the form. Each result row is one matched pod, with the IAM
role and the binding source attributed:

```
GET /api/clusters/{cluster}/iam/reverse-lookup
    ?action=s3:GetObject
    &resource=arn:aws:s3:::my-bucket/*
    &namespace=team-foo
```

Returns a `ReverseLookupResponse` with `rows: ReverseLookupPodRow[]`.
Sensitive-flagged rows sort first; the response carries `truncated`
and `totalPods` so the SPA renders honest "N of M" banners.

Dual-source SAs emit **one row per binding per pod** so the result
honestly reflects that the same pod has two distinct permission paths.

Wildcard support: both the action and the resource accept IAM glob
patterns (`s3:*`, `arn:aws:s3:::bucket/*`). Wildcards are matched
case-insensitively, mirroring IAM's evaluation semantics.

## Sensitive-perms catalog

Locked for v1.1. The catalog ships at `internal/awseks/iam/sensitive.yaml`
and is exposed at:

```
GET /api/identity/sensitive-catalog
```

Cluster-agnostic. Returns the catalog version, every entry's action +
category (`privilege-escalation`, `data`, `cross-account`,
`destructive`, `cluster`, `wildcard`), and a `reverseQuery` hint the
SPA fires on chip click. The literal `*` action is classified
`wildcard` by the matcher (not a YAML entry) so operators cannot
disable the wildcard chip.

## Paywall pane (capabilities)

Periscope **does not hide** AWS Access surfaces from users whose
clusters or roles don't yet support them. The tab is always present;
when unavailable, it renders a paywall pane with a structured reason
and the exact list of missing permissions:

```
GET /api/clusters/{cluster}/identity/capabilities
```

Per-feature response (`features.awsAccessTab`, `features.reverseLookup`,
`features.sensitiveCatalog`), each with `available`, a reason code
(`NOT_EKS`, `RBAC_DENIED`, `MISSING_IAM_PERMS`,
`NO_IDENTITY_CONFIGURED`, `INFORMER_WARMING`, `IAM_PROBE_DISABLED`),
a human message, an array of missing permissions / RBAC verbs, and a
docs link. Cached server-side for 5 minutes per `(cluster, actor)`;
the locked pane's **Re-check** button sends `Cache-Control: no-cache`
to force a fresh probe after a permission grant.

### IAM probe configuration

The capabilities probe optionally calls `iam:SimulatePrincipalPolicy`
against Periscope's own caller identity to populate the exact
`Missing[]` list for `MISSING_IAM_PERMS`. Controlled by:

```
PERISCOPE_AWS_ACCESS_IAM_PROBE=true|false
```

Default: `true`. When disabled (or when
`iam:SimulatePrincipalPolicy` itself is denied to Periscope's role),
the capabilities response stays optimistically `available: true` with
a `note` explaining the limitation; the first real call surfaces the
403 with an error chip. Set to `false` on locked-down accounts where
adding `iam:SimulatePrincipalPolicy` to the periscope-server role is
not desirable.

## Honest limits (v1.1)

These are explicitly **out of scope** for v1.1; the SPA flags them
clearly so operators don't misread the output:

- **Conditions are not evaluated.** Statements with a `Condition` clause
  render with a `condition (not evaluated)` chip. The matcher only
  surfaces `hasCondition: true`.
- **SCPs and permission boundaries are ignored.** Periscope shows what
  the role's identity-based policies *grant*, not what an SCP /
  permission boundary may further restrict.
- **Resource-based policies are ignored.** S3 bucket policies, KMS key
  policies, etc. are not consulted.
- **No `sts:AssumeRole` chain expansion.** If a role's policy grants
  `sts:AssumeRole`, the chip fires but the downstream role's policies
  are not pulled in.
- **NotAction / NotResource statements are not evaluated.** They
  render as a console link, not as evaluated rows. Half-implementing
  these was deemed dangerous; full support lands in v1.2.

## Required IAM + RBAC

Periscope's server role needs the policy reads to compute the IAM
side of the response. These are additive to the #178 (identity) read
set:

```
"iam:ListRolePolicies"
"iam:GetRolePolicy"
"iam:ListAttachedRolePolicies"
"iam:GetPolicy"
"iam:GetPolicyVersion"
```

Optional, only when `PERISCOPE_AWS_ACCESS_IAM_PROBE=true` (default):

```
"iam:SimulatePrincipalPolicy"
```

Kubernetes RBAC (cluster-wide reads — the reverse-lookup page
iterates across namespaces):

```
serviceaccounts: get, list
pods: get, list
```

See `docs/setup/cluster-rbac.md` for the full RBAC + IAM template.

## Audit

Every call to the workload-permissions, reverse-lookup, sensitive-
catalog, and capabilities endpoints emits an audit row with verb
`aws_iam_read`. Internal SDK fan-outs (each `iam:Get*` / `iam:List*`)
also emit `aws_iam_read` rows with a finer-grained `op` field — see
`internal/audit/event.go`'s `VerbAwsIAMRead` docblock for the full op
list.

The four cluster-identity endpoints (#178) continue to emit
`aws_identity_read`; operator audit-feed filters that previously
captured both surfaces under one verb should now include both.
