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
