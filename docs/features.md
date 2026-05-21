---
title: "Periscope — full feature list"
description: "The complete capability inventory for Periscope: authentication, multi-cluster management, browsing, editing, Helm, EKS-native tooling, Karpenter, CVE surfacing, AWS Access, and audit."
---

# Features

The complete capability list. For the short pitch, see the
["What makes it different"](../README.md#what-makes-it-different)
section of the README.

## Authentication & access

- Pod Identity / IRSA for cluster access (no static AWS credentials on the pod)
- OIDC user auth with IdP-group-gated authorization (shared / tier / raw modes)
- Per-cluster RBAC enforced server-side via `Impersonate-User` / `Impersonate-Group` headers
- Pre-flight RBAC checks (SAR / SSRR) so disabled buttons explain why instead of failing on click

## Multi-cluster

- Fleet view at `/` — every registered cluster as a status card with identity, hot signals, and one-click drill-in
- Switch context from the cluster rail (Slack-style left bar)
- Per-cluster scoping for every resource view
- Add managed clusters via `backend: agent` (#42) — `kubectl apply` an agent on any K8s with outbound HTTPS, no IAM trust required. Works on EKS, GKE, AKS, on-prem k3s.

## Browsing & inspection

- Common resources (pods, deployments, services, configmaps, secrets, jobs, ingresses, RBAC, …) plus full Custom Resource catalog
- Live events, describe view, logs (with follow + filtering)
- In-browser pod shell (`exec`) with reconnect on transient disconnects — works on every backend (eks, kubeconfig, in-cluster, agent)
- Cmd+K palette: search resources by name across the active cluster

## Real-time updates (watch streams)

- 21+ resource list pages stream over SSE for live updates spanning workloads, networking, storage, and cluster-scoped resources
- Per-user concurrency cap to keep apiserver watch quotas safe
- Polling fallback when the EventSource path fails (corporate proxies, etc.)
- Operator opt-out via Helm: subset, group aliases (`workloads`, `networking`, `storage`, `cluster`, `core`), or full disable

## Editing

- Inline Monaco YAML editor for any resource — built-in or CRD
- Schema-aware autocomplete and validation against the cluster's `/openapi/v3`
- Server-side apply with minimal diffs (no `last-applied` annotation churn)
- Field-ownership glyphs: see who manages each field before you edit
- Conflict resolution: per-field "keep mine / take theirs" when a controller owns the field
- Live drift detection: warns when the cluster changes underneath the editor
- Unsaved-changes guards on refresh, sidebar nav, row-click

## Helm

- Release browser per cluster: per-release values, manifest, history, NOTES.txt, and structured dyff-based diff between revisions (read paths use direct Secret/ConfigMap decoding, no Helm SDK on the read path)
- In-browser install / upgrade / uninstall with Atomic-by-default rollback on partial failure
- Per-revision rollback from the history tab: pick the target revision, see the side-by-side diff before clicking, with `wait` / `cleanup-on-fail` / `disable-hooks` knobs surfaced inline
- Schema-aware values editor: structured form when the chart ships `values.schema.json`, Monaco YAML otherwise, with binary form/YAML toggle for `$ref`-heavy schemas
- Dry-run preview pane shows the rendered manifests + RBAC pre-flight denials + (upgrade) semantic diff against the live cluster before the operator commits
- Public HTTP and OCI chart fetch with SSRF protection (IMDS / link-local always blocked, RFC1918 opt-in via env)

## EKS managed add-ons

- Browse installed add-ons with health, installed version, latest available, k8s compatibility window, and "blocks next k8s minor" warnings — feeds upgrade readiness
- Catalog browse of every AWS-published add-on for the cluster's K8s version, filterable by AWS / third-party / type
- Install / upgrade / delete actions with schema-aware configuration editor and `resolveConflicts` choice
- Right-edge detail pane with describe / config tabs: see the operator's stored `configurationValues`, IAM service account role, Pod Identity associations, and full version history with one-click "upgrade to" on newer versions
- Status-aware polling watches `CREATING` / `UPDATING` / `DELETING` flips so the UI stays in sync without manual refresh

## EKS upgrade readiness

- Upgrade Insights surface with per-issue severity, kubernetes-version-step, and remediation links
- Managed node group AMI drift detection so operators see which groups need a rotation before bumping the cluster
- Works on `in-cluster`, `agent`, `eks`, and `kubeconfig` backends as long as the cluster entry has `arn` + `region`

## Karpenter

- Curated read-only dashboard at `/clusters/{c}/karpenter`, auto-detected via the `karpenter.sh/v1` CRD probe — sidebar entry only appears on Karpenter-enabled clusters
- NodePool table with weight, disruption budgets, current/limit usage, and per-pool `$/hr` + spot-savings (controller `/metrics` scraped via apiserver service-proxy under impersonation)
- NodeClaims grouped by NodePool with `Drifted` / `Initialized` / `Launched` conditions surfaced as badges; pools with any drifted claim auto-expand
- Pending pods waiting on Karpenter with the per-NodePool incompatibility breakdown extracted from the `FailedScheduling` apiserver Event — operators no longer have to grep karpenter-controller logs to see *why* a pod isn't being scheduled
- Resizable detail pane on row click for describe / yaml / events without leaving the dashboard
- Graceful degradation: missing `/metrics` or events list failures degrade individual panels without failing the whole response

## Security & CVE surfacing

- Inline severity chips on Pods / Nodes / Karpenter list pages — at-a-glance `2C · 5H · 12M` per row, sourced from Amazon Inspector v2
- New `security` detail-pane tab on Pod / Node / Deployment / StatefulSet / DaemonSet / Karpenter NodeClaim — findings grouped **by package** server-side (a typical 200-finding container collapses to ~10 package groups with per-group "upgrade `1.16.1 → 1.26.3` fixes all" hints), pre-sorted by triage priority (exploits → severity → CVSS → EPSS)
- Filter chips on every Security tab — `critical / high / medium / low`, `exploits N`, `fixable only` with live `X / Y shown` indicator. Toggling stays client-side (no backend roundtrip)
- Per-cluster local cache, lazily hydrated on first activation; reads are O(1) thereafter, with 6h TTL background refresh and an entity-scoped manual `↻ refresh` button (one `cve_refresh` audit row per click)
- Per-finding detail surface — description, remediation text + vendor advisory link, EPSS score, exploit-availability flag, fix-availability pill, first / last observed timestamps, Inspector console deep-link
- Empty-state contract: when Inspector v2 is disabled or the IAM grant is missing, every CVE-aware page renders an unobtrusive once-per-cluster hairline banner instead of erroring out
- Same wire shape feeds the SPA and the future MCP / AI-agent tool layer (v1.2) — one source of truth for "what to fix first" prioritization
- Opt-in via Helm (`inspector.enabled: true`) — see [usage guide](usage/cve.md) for IAM, audit, cost

## AWS Access

- **Cluster Access page** — reconciles EKS Access Entries with the legacy aws-auth ConfigMap (migration-health chip: `aws-auth-only` / `entries-only` / `both`), unified SA → IAM Role index (IRSA + Pod Identity, with dual-source + orphan flags), and a role-centric Pod Identity view
- **AWS Access tab** on every workload detail pane (Pod / ServiceAccount / Deployment / StatefulSet / DaemonSet) — resolved identity chain, IAM policies grouped by AWS service, sensitive-permissions chip strip against an 18-chip catalog (17 named actions + literal `*`) with categories `privilege-escalation` / `data` / `cross-account` / `destructive` / `cluster` / `wildcard`
- **Reverse lookup** at `/clusters/{c}/reverse-lookup` — "which workloads can perform action X?" with one-row-per-matched-pod results, binding-source attribution, and one-click chip pre-fill from any sensitive chip on the AWS Access tab
- **Locked-feature pane** with structured `reason` + exact missing-permissions list (powered by `iam:SimulatePrincipalPolicy` probe, 5-min server-side cache, `Re-check` button to bypass)
- IAM probe configurable via `PERISCOPE_AWS_ACCESS_IAM_PROBE` env (default on); falls back to optimistic `available: true` + lazy 403 when the probe itself is denied
- See [usage guide](usage/aws-access.md) for Cluster Access page, per-workload tab, reverse-lookup walkthrough; IAM grant for the periscope-server role is in [cluster-rbac.md](setup/cluster-rbac.md)

## Audit & observability

- Every privileged action signed by the human user — apply, delete, exec, secret reveal, log open, cronjob trigger
- Persistent audit log: SQLite (single-replica), with retention and size caps
- First-class in-app audit view with filters by actor, verb, outcome, time range, namespace, request id
- Density timeline strip surfaces denials and failures at a glance
- Tier-mode audit-admin groups can see every actor's rows; everyone else sees their own
- Structured JSON events also stream to stdout for shipping into CloudWatch / Loki / OpenSearch / Datadog
