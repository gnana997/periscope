// Wire types for the Identity page (#178). One-to-one with the Go
// shapes in internal/awseks/identity/types.go — keep them in sync;
// the JSON encoder on the backend uses the json: tags we mirror as
// field names here.

export type Source = "IRSA" | "PodIdentity" | "Both";

export type DiffSide = "aws-auth" | "access-entries" | "both";

export interface AccessPolicyAssoc {
  policyArn: string;
  accessScope?: string;
  namespaces?: string[];
  modifiedAt?: string; // ISO timestamp
}

export interface AccessEntry {
  principalArn: string;
  type?: string;
  kubernetesGroups?: string[];
  accessPolicies?: AccessPolicyAssoc[];
  modifiedAt?: string;
}

export interface AwsAuthEntry {
  principalArn: string;
  username?: string;
  kubernetesGroups?: string[];
}

export interface AwsAuthDiffEntry {
  in: DiffSide;
  principalArn: string;
  kubernetesGroups?: string[];
}

export interface AwsAuthDiffHealth {
  awsAuthOnly: number;
  dual: number;
  accessEntriesOnly: number;
}

export interface AwsAuthDiffResponse {
  entries: AwsAuthDiffEntry[];
  health: AwsAuthDiffHealth;
}

export interface SARoleBinding {
  source: Source;
  roleArn: string;
  roleExists: boolean;
  podIdentityAssociationId?: string;
  irsaAnnotationValue?: string;
}

export interface SARoleIndexEntry {
  cluster: string;
  namespace: string;
  saName: string;
  bindings: SARoleBinding[];
  dualSource: boolean;
}

export interface PodIdentityAssoc {
  associationId: string;
  roleArn: string;
  namespace: string;
  serviceAccount: string;
  clusterName?: string;
}

export interface PodIdentityResponse {
  groups: Record<string, PodIdentityAssoc[]>;
}

// ── IAM engine wire types (#187) ─────────────────────────────────
//
// Mirrors 1:1 with the Go shapes in internal/awseks/iam/types.go.
// Locked Day-1 on feat/iam-engine-187 so the SPA work for #188
// (forward view + reverse lookup) can scaffold against stubs while
// the engine is built in parallel. Bump CatalogVersion in
// sensitive.yaml when the catalog changes.

export type PermissionEffect = "Allow" | "Deny";
export type PolicySource = "inline" | "managed";
export type SensitiveCategory =
  | "privilege-escalation"
  | "data"
  | "cross-account"
  | "destructive"
  | "cluster"
  | "wildcard";

export interface Permission {
  // Identity — matcher core
  action: string;        // "s3:GetObject", "s3:*", "*"
  service: string;       // always lower-cased — used for SPA grouping
  resource: string;      // ARN or wildcard; "*" if absent
  effect: PermissionEffect;

  // Source attribution
  policyArn?: string;    // empty for inline policies
  policyName: string;
  policySource: PolicySource;
  statementSid?: string;
  statementIdx: number;

  // Pre-computed render hints
  sensitive: boolean;
  sensitiveReason?: SensitiveCategory;
  hasCondition: boolean; // presence-only flag (no eval in v1.1)
  wildcard: boolean;     // Action or Resource contains "*" or "?"
}

// RawStatement surfaces NotAction / NotResource / NotPrincipal
// statements that the v1.1 engine doesn't project to Permission
// rows. Rendered as a "complex statement — see in IAM console" chip
// with an optional deep link. consoleUrl is omitted for non-aws
// partitions.
export interface RawStatement {
  policyArn?: string;
  policyName: string;
  policySource: PolicySource;
  statementIdx: number;
  statementSid?: string;
  reason: "NotAction" | "NotResource" | "NotPrincipal";
  summary: string;
  consoleUrl?: string;
}

// Forward-view response — per-Pod / per-SA / per-Deployment AWS
// Access tab. truncated + totalCount form the soft-cap signal: when
// expansion exceeds MaxRowsCap (default 10000), the SPA renders
// "showing N of M — filter to narrow" instead of freezing the tab.
// policyFetchPartial mirrors the snapshot-with-error pattern; SPA
// shows a banner without blanking the page.
export interface RolePermissionsResponse {
  roleArn: string;
  permissions: Permission[];
  rawStatements: RawStatement[];
  fetchedAt: string;            // ISO timestamp
  policyFetchPartial: boolean;
  catalogVersion: string;       // from sensitive.yaml
  truncated: boolean;
  totalCount: number;           // matches permissions.length if !truncated
}

// One row of the reverse-lookup result table — one row per matched
// pod (#188 wire shape; the older one-row-per-SA `matches` shape
// is gone). The same data drives an MCP tool's "which pods can do
// X" answer without a client-side join.
export interface ReverseLookupPodRow {
  pod: PodRef;
  saName: string;
  namespace: string;
  roleArn: string;
  permission: Permission;
  source: Source | "";  // empty if index moved between snapshot + lookup
}

export interface PodRef {
  namespace: string;
  name: string;
  nodeName?: string;
}

export interface ReverseLookupScope {
  cluster?: string;
  namespace?: string;
}

export interface ReverseLookupResponse {
  action: string;
  resource?: string;
  scope?: ReverseLookupScope;
  rows: ReverseLookupPodRow[];
  truncated: boolean;
  totalPods: number;
}

// ── Composed AWS Access surface (#188) ────────────────────────────

// Server-grouped permissions for the AWS Access tab. The SPA
// renders one accordion per ServiceGroup and never re-buckets.
export interface ServiceGroup {
  service: string;       // lower-cased, "*" for wildcard-action statements
  sensitive: boolean;    // any perm in the group is sensitive
  count: number;
  permissions: Permission[];
}

// Mirrors identity.SARoleBinding; copied into the AWS Access tab
// response so the SPA doesn't need a join to render the chain.
export interface IdentityChainBinding {
  source: Source;
  roleArn: string;
  roleExists: boolean;
  podIdentityAssociationId?: string;
  irsaAnnotationValue?: string;
}

export interface IdentityChain {
  serviceAccount: string;
  bindings: IdentityChainBinding[];
  dualSource: boolean;
}

export type AwsAccessWarningCode =
  | "DUAL_SOURCE_IRSA_SHADOWED"
  | "ROLE_NOT_FOUND"
  | "POLICY_FETCH_PARTIAL"
  | "NO_BINDINGS";

export interface AwsAccessWarning {
  code: AwsAccessWarningCode;
  message: string;
  roleArn?: string;
}

export type WorkloadKind =
  | "Pod"
  | "ServiceAccount"
  | "Deployment"
  | "StatefulSet"
  | "DaemonSet";

// Composed forward-view response — one round-trip = full tab body.
export interface WorkloadPermissionsResponse {
  cluster: string;
  kind: WorkloadKind;
  namespace: string;
  name: string;
  identityChain: IdentityChain;
  groups: ServiceGroup[];
  rawStatements: RawStatement[];
  warnings: AwsAccessWarning[];
  affectedPods: PodRef[];
  affectedPodCount: number;
  policyFetchPartial: boolean;
  truncated: boolean;
  totalCount: number;
  catalogVersion: string;
  fetchedAt: string;
}

// ── Sensitive catalog (#188) ──────────────────────────────────────

export interface ReverseQueryHint {
  action: string;
  resource?: string;
}

export interface SensitiveCatalogEntry {
  action: string;
  category: SensitiveCategory;
  pattern: boolean;
  reverseQuery: ReverseQueryHint;
}

export interface SensitiveCatalogResponse {
  version: string;
  entries: SensitiveCatalogEntry[];
}

// ── Capabilities (#188 paywall) ───────────────────────────────────

export type CapabilityReason =
  | "NOT_EKS"
  | "RBAC_DENIED"
  | "MISSING_IAM_PERMS"
  | "NO_IDENTITY_CONFIGURED"
  | "INFORMER_WARMING"
  | "IAM_PROBE_DISABLED";

export interface FeatureCapability {
  available: boolean;
  reason?: CapabilityReason;
  message?: string;
  missing?: string[];
  docsUrl?: string;
  consoleUrl?: string;
  note?: string;
}

// Stable feature keys — match the Go constants in
// internal/awseks/iam/types.go (FeatureAwsAccessTab etc.).
export interface CapabilitiesFeatures {
  awsAccessTab: FeatureCapability;
  reverseLookup: FeatureCapability;
  sensitiveCatalog: FeatureCapability;
}

export interface CapabilitiesResponse {
  cluster: string;
  features: CapabilitiesFeatures;
  fetchedAt: string;
  note?: string;
}
