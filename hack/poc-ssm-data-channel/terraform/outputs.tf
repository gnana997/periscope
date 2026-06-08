output "instance_id" {
  description = "The POC instance id — feed straight into the probe."
  value       = aws_instance.node.id
}

output "region" {
  description = "Region the instance lives in."
  value       = var.region
}

output "run_hint" {
  description = "Ready-to-paste command to drive the probe against this instance (ambient-creds path)."
  value       = "make -C $(git rev-parse --show-toplevel) poc-ssm-data-channel INSTANCE_ID=${aws_instance.node.id} AWS_REGION=${var.region} ASSERT=1"
}

output "node_shell_role_arn" {
  description = "The per-user node-shell role ARN (null unless enable_oidc=true). Pass to the probe as --role-arn."
  value       = var.enable_oidc ? aws_iam_role.node_shell[0].arn : null
}

output "oidc_provider_arn" {
  description = "The IAM OIDC provider ARN (null unless enable_oidc=true)."
  value       = var.enable_oidc ? aws_iam_openid_connect_provider.idp[0].arn : null
}

output "run_hint_oidc" {
  description = "Per-user impersonation run (requires enable_oidc=true and an id_token in id-token.txt)."
  value = var.enable_oidc ? join(" ", compact([
    "cd hack/poc-ssm-data-channel &&",
    "go run . --instance-id ${aws_instance.node.id} --region ${var.region}",
    "--id-token-file id-token.txt --role-arn ${aws_iam_role.node_shell[0].arn}",
    "--expected-aud ${var.oidc_client_id}",
    var.groups_claim == "" ? "" : "--groups-claim ${var.groups_claim}",
    "--assert",
  ])) : "set -var enable_oidc=true (and your IdP vars) to enable the per-user impersonation path"
}
