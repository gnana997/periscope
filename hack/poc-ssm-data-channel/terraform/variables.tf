variable "region" {
  description = "AWS region to create the POC instance in."
  type        = string
  default     = "us-east-1"
}

variable "instance_type" {
  description = "EC2 instance type. t2.micro is AWS Free Tier eligible in most regions for new accounts."
  type        = string
  default     = "t2.micro"
}

variable "name" {
  description = "Name prefix for the instance and its IAM/SG resources."
  type        = string
  default     = "periscope-ssm-poc"
}

# ─── Per-user impersonation (opt-in) ──────────────────────────────────
# All of the below only matter when enable_oidc = true. They are NOT
# hardcoded to any tenant — every operator's IdP differs. Supply your own
# via a terraform.tfvars file (copy terraform.tfvars.example) or -var
# flags. iam.tf preconditions enforce that the required ones are set when
# enable_oidc = true.

variable "enable_oidc" {
  description = "Create the OIDC provider + per-user node-shell role (the production auth model). Off by default; not everyone has an IdP wired up."
  type        = bool
  default     = false
}

variable "oidc_issuer" {
  description = "Your OIDC issuer URL, with trailing slash (e.g. https://<tenant>.us.auth0.com/). Required when enable_oidc=true."
  type        = string
  default     = ""
}

variable "oidc_client_id" {
  description = "Your OIDC client_id — this is the id_token's aud claim (NOT the API audience). Required when enable_oidc=true."
  type        = string
  default     = ""
}

variable "groups_claim" {
  description = "The id_token claim carrying group membership (IdP-specific; Auth0 namespaces custom claims, e.g. https://<your-app>/groups). Required only when required_group is set."
  type        = string
  default     = ""
}

variable "required_group" {
  description = "Group the trust policy requires (your Periscope admin-tier group). Leave \"\" to gate on aud only — useful if the namespaced-claim condition turns out not to evaluate."
  type        = string
  default     = ""
}
