// Package audit defines the event shape and emission pipeline for
// Periscope's audit trail.
//
// Every privileged action — pod exec, secret reveal, resource
// apply/delete, cronjob trigger — is recorded as a single
// audit.Event flowed through an Emitter. The Emitter fans out to
// one or more Sinks (today: stdout JSON; future: SQLite, external).
//
// Why a dedicated package: prior to this refactor the same logical
// event was emitted as ad-hoc slog calls at each handler with three
// different field shapes, with no audit row at all on the failure
// path. Pinning a single shape here lets every handler emit the same
// way, lets a downstream sink rely on stable field names, and gives
// us one place to add future cross-cutting concerns (signing,
// shipping to a SIEM, redaction).
//
// Stdlib-only by design — the audit pipeline must not pull in heavy
// dependencies because every privileged code path runs through it.
package audit

import "time"

// Verb classifies what the actor did. The set is closed: a new verb
// should be added explicitly here rather than passed as a free string,
// so downstream queries (and the future SQLite schema) can index on
// it.
//
// Note that VerbApply covers create-and-update through Server-Side
// Apply — Periscope's mutation surface (PATCH with
// application/apply-patch+yaml) does not split create vs update at
// the API level, and we don't synthesize the distinction client-side.
// The forensic question "did this row create or modify the
// resource?" is answerable by joining audit rows: the first
// successful apply for a given (cluster, ns, group, version,
// resource, name) is the create; everything after is an update.
type Verb string

const (
	VerbApply        Verb = "apply"
	VerbDelete       Verb = "delete"
	VerbTrigger      Verb = "trigger"
	VerbExecOpen     Verb = "exec_open"
	VerbExecClose    Verb = "exec_close"
	VerbSecretReveal Verb = "secret_reveal"
	VerbBulkDownload Verb = "bulk_download"
	// VerbHelmChartFetch — operator pasted a chart ref (HTTP repo or
	// OCI) and clicked Fetch to load values + schema for the install
	// dialog (issue #73). User-initiated read, worth attributing —
	// internal repo URLs end up in the audit trail too. NOT emitted
	// for the version-list endpoint (called while typing).
	VerbHelmChartFetch Verb = "helm_chart_fetch"
	// VerbHelmPreview — operator clicked "Preview" on the install
	// dialog (issue #75) and the backend ran a dry-run via the helm
	// SDK to render the manifests + (for upgrade mode) diff against
	// the live cluster state. Single verb covers both modes; the
	// `op` field in Extra distinguishes "install" vs "upgrade", same
	// pattern as VerbEKSInsightsRead's "ListInsights"/"DescribeInsight".
	// Emitted on every call regardless of outcome — failures (chart
	// fetch errors, render errors, RBAC denials) are forensically
	// interesting too.
	VerbHelmPreview Verb = "helm_preview"
	// VerbHelmInstallIntent / VerbHelmInstall — pre/post pair for the
	// helm install action (issue #76). Intent fires BEFORE the helm
	// SDK call so a partition / hung apiserver / timeout still leaves
	// a forensic trail of "operator tried to install X". Outcome
	// fires AFTER, capturing the new release revision on success or
	// the failure reason. Same pre/post discipline as
	// VerbRollbackIntent / VerbRollback.
	//
	// Extra carries: ref, version, namespace, releaseName,
	// atomic, wait, timeoutSeconds. Outcome row also carries: revision
	// (on success), rolledBack (true when atomic caught a partial
	// failure), manifestKinds (set of kinds in the rendered output —
	// for forensic queries like "show me every install that included
	// a Secret").
	VerbHelmInstallIntent Verb = "helm_install_intent"
	VerbHelmInstall       Verb = "helm_install"
	// VerbHelmUpgradeIntent / VerbHelmUpgrade — pre/post pair for the
	// helm upgrade action. Same shape as VerbHelmInstall* but op-
	// specific (target release lives in the URL path, body carries
	// the proposed ref/version/values). Outcome row carries the new
	// revision number — operators can audit "release X was at
	// revision N before this upgrade, is at N+1 after."
	VerbHelmUpgradeIntent Verb = "helm_upgrade_intent"
	VerbHelmUpgrade       Verb = "helm_upgrade"
	// VerbHelmUninstallIntent / VerbHelmUninstall — pre/post pair for
	// the helm uninstall action (issue #123). Destructive, so the
	// pre/post discipline matters more here than for read paths: an
	// uninstall that hangs mid-delete leaves resources behind, and
	// the intent row is the only forensic record that the operator
	// fired the request at all.
	//
	// Extra carries: namespace, releaseName, keepHistory, disableHooks.
	// Outcome row also carries: revisionsRemoved (count from helm SDK
	// response) so a forensic query can see "this uninstall removed
	// 8 revisions of release X" vs "release was already at 1 revision".
	VerbHelmUninstallIntent Verb = "helm_uninstall_intent"
	VerbHelmUninstall       Verb = "helm_uninstall"
	// VerbHelmRollbackIntent / VerbHelmRollback — pre/post pair for
	// the helm rollback action (issue #77). Pre-flight SAR (verb=patch)
	// runs against each kind in the TARGET revision's manifest list;
	// denial blocks before the SDK call. Intent fires before the
	// helm SDK call captures revision target + current revision so a
	// hung or partitioned rollback still leaves a forensic trail.
	// Outcome carries the new revision number on success in Extra.
	VerbHelmRollbackIntent Verb = "helm_rollback_intent"
	VerbHelmRollback       Verb = "helm_rollback"
	// VerbRollbackIntent is emitted before the apiserver patch fires —
	// captures the operator's intent (target revision, reason) even
	// when the patch later fails or the request hangs. Pair with
	// VerbRollback (post-outcome) for a complete forensics trace.
	VerbRollbackIntent Verb = "rollback_intent"
	// VerbRollback is the outcome row for a workload rollback (issue
	// #71). Carries the new revision number on success in Extra.
	VerbRollback Verb = "rollback"
	// VerbLogOpen is reserved for pod/workload log stream opens. No
	// emission site exists yet; declared so the taxonomy is visible
	// and a follow-up PR can wire it without revisiting this file.
	VerbLogOpen Verb = "log_open"
	// VerbEKSInsightsRead records a read against the EKS Upgrade
	// Insights surface (ListInsights / DescribeInsight). It is the
	// first read verb in the taxonomy — the rest of the audit trail
	// captures privileged mutations only, but compliance reviewers
	// asked specifically for a record that an operator checked
	// upgrade readiness on a cluster, since "did anyone look before
	// we shipped 1.32?" is an answerable question only if the look
	// itself is logged. The verb is scoped to this AWS-side surface;
	// other read endpoints (helm list, resource list, …) remain
	// unaudited and a separate decision is required to broaden the
	// pattern.
	VerbEKSInsightsRead Verb = "eks_insights_read"
	// VerbEKSNodegroupsRead records a read against the managed node
	// group surface (ListNodegroups / DescribeNodegroup, plus the
	// derived AMI drift). Same precedent as VerbEKSInsightsRead:
	// compliance wants a record of who checked node-pool freshness
	// before an upgrade, so the read itself is auditable. Drift
	// computation pulls in additional AWS API calls (SSM
	// GetParameter, ec2 DescribeImages) under the same row — we do
	// not split those into separate verbs because the caller's
	// intent is the same operator action.
	VerbEKSNodegroupsRead Verb = "eks_nodegroups_read"
	// VerbEKSAddonsRead records a read against the EKS managed add-on
	// surface (ListAddons / DescribeAddon, plus the shared
	// DescribeAddonVersions catalog). Same precedent as
	// VerbEKSInsightsRead and VerbEKSNodegroupsRead: compliance wants
	// a record of who checked add-on freshness — "is anything blocking
	// the next minor?" — before an upgrade. The catalog lookup
	// (DescribeAddonVersions) is rolled into the same row because the
	// caller's intent is the same operator action; `op` in Extra
	// distinguishes the read kind so a reviewer can see what was
	// touched without adding new verbs:
	//   "list"                  — installed-addons list (#117)
	//   "list:cache_hit"
	//   "detail"                — per-addon detail (#117)
	//   "detail:cache_hit"
	//   "catalog"               — full add-on catalog (#119)
	//   "catalog:cache_hit"
	//   "configuration"         — addon-version JSON Schema (#119)
	//   "configuration:cache_hit"
	VerbEKSAddonsRead Verb = "eks_addons_read"
	// VerbKarpenterRead records a read against the curated Karpenter
	// dashboard surface (#118) — NodePool / NodeClaim list, pending-pod
	// scheduling failures, and the per-NodePool cost summary computed
	// from karpenter-controller's `/metrics` exposition. Read-style
	// verb mirroring VerbEKSInsightsRead: emitted on every call
	// regardless of outcome so compliance can answer "did anyone view
	// the autoscaler dashboard before this 3am node churn?".
	VerbKarpenterRead Verb = "karpenter_read"
	// VerbAwsIdentityRead records a read against the cluster
	// Identity surface (#178): EKS Access Entries, the legacy
	// kube-system/aws-auth ConfigMap, EKS Pod Identity associations,
	// and IRSA-annotated ServiceAccounts.
	//
	// Same read-style verb as VerbEKSAddonsRead / VerbKarpenterRead
	// — one row per AWS API call so compliance can attribute each
	// Describe / List to a requesting actor. Extra carries `op`
	// to distinguish the calls inside one handler invocation:
	//
	//     "list_access_entries"        — eks:ListAccessEntries
	//     "describe_access_entry"      — eks:DescribeAccessEntry
	//     "list_associated_policies"   — eks:ListAssociatedAccessPolicies
	//     "list_pod_identity"          — eks:ListPodIdentityAssociations
	//     "describe_pod_identity"      — eks:DescribePodIdentityAssociation
	//     "read_aws_auth"              — k8s GET kube-system/aws-auth
	//     "get_role"                   — iam:GetRole (existence probe)
	//     "ensure_sa_roles"            — SA→Role index build (rollup)
	//
	// IAM-engine and AWS Access surface (#187, #188) rows emit
	// VerbAwsIAMRead instead — see that constant below.
	VerbAwsIdentityRead Verb = "aws_identity_read"
	// VerbAwsIAMRead records a read against the IAM policy
	// resolution engine (#187) and the composed AWS Access surface
	// (#188): per-Pod / per-workload IAM access tab, per-cluster
	// reverse lookup, sensitive-perms catalog, and the capabilities
	// probe.
	//
	// One row per AWS API call, mirroring VerbAwsIdentityRead, so
	// operator audit-feed filters can split "who read identity
	// surface" from "who read IAM policies" cleanly. Extra carries
	// `op` to distinguish the calls inside one handler invocation:
	//
	//   #187 (IAM engine):
	//     "list_role_policies"          — iam:ListRolePolicies (inline)
	//     "get_role_policy"             — iam:GetRolePolicy
	//     "list_attached_role_policies" — iam:ListAttachedRolePolicies (managed)
	//     "get_policy"                  — iam:GetPolicy (resolves DefaultVersionId)
	//     "get_policy_version"          — iam:GetPolicyVersion
	//     "reverse_lookup"              — engine ReverseLookup invocation (rollup)
	//     "role_permissions"            — engine RolePermissions invocation (rollup)
	//
	//   #188 (AWS Access composed surface):
	//     "workload_permissions"        — composed forward-view handler
	//     "capabilities"                — capabilities probe (cold)
	//     "capabilities:cache_hit"      — capabilities probe (cache hit)
	//     "simulate_principal_policy"   — iam:SimulatePrincipalPolicy
	//
	// The engine-level rollups (reverse_lookup / role_permissions /
	// workload_permissions) are emitted once per request alongside
	// the per-SDK-call rows so compliance reviewers can see the
	// user-facing intent without scrolling through every Describe /
	// Get.
	VerbAwsIAMRead Verb = "aws_iam_read"
	// VerbEKSAddonInstallIntent / VerbEKSAddonInstall are the paired
	// audit rows for an EKS managed add-on install (#119, PR-2).
	//
	// AWS-side mutations are async-by-design: CreateAddon returns
	// immediately with status=CREATING; the actual provisioning
	// happens server-side over 1-5 minutes. The Intent row captures
	// the operator's request before the SDK call so a hung / aborted
	// invocation still leaves a forensic trail; the outcome row
	// captures the SDK's immediate response (success → addon
	// resource created with status CREATING; failure → AWS error).
	// The status flip from CREATING → ACTIVE / CREATE_FAILED is
	// observable through subsequent eks_addons_read rows once the
	// SPA polls; we don't emit a separate row when the status flips
	// because nobody initiated that transition — AWS did.
	//
	// Same paired-intent shape as workload rollback (#71). Extra
	// carries `addonName`, `addonVersion`, and (on outcome rows) the
	// AWS request ID for AWS-side correlation.
	VerbEKSAddonInstallIntent Verb = "eks_addon_install_intent"
	VerbEKSAddonInstall       Verb = "eks_addon_install"
	// VerbEKSAddonUpgradeIntent / VerbEKSAddonUpgrade pair (#119,
	// PR-3). Same async-by-design contract and paired-intent shape
	// as install — UpdateAddon returns status=UPDATING; provisioning
	// completes AWS-side over 1-5 min. Extra carries `addonName`,
	// `addonVersion` (the *target* version), and `resolveConflicts`.
	VerbEKSAddonUpgradeIntent Verb = "eks_addon_upgrade_intent"
	VerbEKSAddonUpgrade       Verb = "eks_addon_upgrade"
	// VerbEKSAddonDeleteIntent / VerbEKSAddonDelete pair (#119,
	// PR-3). DeleteAddon returns status=DELETING. Extra carries
	// `addonName` and `preserve` — the boolean operator choice for
	// whether the underlying K8s resources stay (preserve=true) or
	// are torn down with the addon.
	VerbEKSAddonDeleteIntent Verb = "eks_addon_delete_intent"
	VerbEKSAddonDelete       Verb = "eks_addon_delete"
	// VerbCveRefresh records an operator-initiated CVE cache refresh
	// (POST /api/clusters/{cluster}/cve/refresh, #165). Reads of the
	// cache itself do NOT emit audit rows — they are internal metadata
	// reads, with AWS CloudTrail covering the underlying Inspector
	// calls under the periscope-server role. The refresh action is the
	// one operator-initiated mutation in the CVE surface, so it lands
	// in the audit log so a reviewer can see who forced a re-scan and
	// what they targeted. Extra carries `digests` and `instanceIds`.
	VerbCveRefresh Verb = "cve_refresh"
)

// Outcome is the result classification.
//
//   - Success: the action completed.
//   - Failure: the action errored for a non-authorization reason
//     (validation, conflict, server error, network).
//   - Denied: the action was rejected by Kubernetes RBAC (Forbidden
//     or Unauthorized). Denials are forensically the most
//     interesting class — surfacing them as a distinct outcome
//     means an operator can answer "who tried X and got blocked"
//     with a single query.
type Outcome string

const (
	OutcomeSuccess Outcome = "success"
	OutcomeFailure Outcome = "failure"
	OutcomeDenied  Outcome = "denied"
)

// Actor is the identity slice copied from credentials.Session at
// emission time. We snapshot rather than holding a pointer so a later
// sink can serialize the event without re-reading request context.
type Actor struct {
	Sub    string   `json:"sub"`
	Email  string   `json:"email,omitempty"`
	Groups []string `json:"groups,omitempty"`
}

// ResourceRef is the Kubernetes object the action targeted.
//
// All fields are optional — an exec event leaves Group/Version/Resource
// empty (the target is implicit in the verb), a cluster-scoped resource
// leaves Namespace empty, and an apply against a yet-to-be-named object
// leaves Name empty. Empty strings are written as empty strings; sinks
// don't need to special-case absence.
type ResourceRef struct {
	Group     string `json:"group,omitempty"`
	Version   string `json:"version,omitempty"`
	Resource  string `json:"resource,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name,omitempty"`
}

// Event is the single shape every audit emission produces.
//
// Top-level fields are the ones every event carries. Verb-specific
// fields (exec byte counts, secret key name, jobName, dryRun) ride
// in Extra so adding a new field for one verb doesn't churn the
// struct or every sink.
type Event struct {
	// Timestamp is set by the Emitter if zero — handlers should not
	// need to remember to fill this.
	Timestamp time.Time

	// RequestID is chi's per-request ID, populated by httpx.RequestID.
	// Lets a single audit row tie back to access logs and
	// reverse-proxy logs.
	RequestID string

	// Route is the chi route pattern (e.g. /api/clusters/{cluster}/...).
	// Useful for grouping audit rows by endpoint without parsing the
	// path.
	Route string

	Actor    Actor
	Verb     Verb
	Outcome  Outcome
	Cluster  string
	Resource ResourceRef

	// Reason is the human-readable explanation. For OutcomeFailure /
	// OutcomeDenied this is err.Error(); for OutcomeSuccess on an
	// exec_close it is the close reason ("completed" / "idle_timeout"
	// / "abort"). Empty for plain successes.
	Reason string

	// Extra carries verb-specific fields. Sinks flatten this into
	// their output (StdoutSink lifts each entry to a top-level slog
	// key). Keep keys lowercase_snake_case for consistency with the
	// existing log shape.
	Extra map[string]any
}
