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

// One hit from a reverse lookup: a (Pod, SA, Role, Permission)
// tuple. podRefs is truncated server-side to PodRefsLimit (default
// 5); podCount is the untruncated total so the SPA can render
// "5 of 50".
export interface ReverseLookupMatch {
  saName: string;
  namespace: string;
  roleArn: string;
  permission: Permission;
  podRefs: string[];
  podCount: number;
}

export interface ReverseLookupScope {
  cluster?: string;
  namespace?: string;
}

export interface ReverseLookupResponse {
  action: string;
  resource?: string;
  scope?: ReverseLookupScope;
  matches: ReverseLookupMatch[];
}
