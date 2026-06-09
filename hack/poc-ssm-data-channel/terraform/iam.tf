# Per-user impersonation IAM — the PRODUCTION auth model, opt-in.
#
# Everything here is gated on var.enable_oidc (default false), because
# not everyone running the spike has Periscope / an IdP wired up. With
# enable_oidc=false you get just the target instance (the ambient-creds
# path). With enable_oidc=true you also get:
#
#   - an IAM OIDC provider registering your IdP (defaults: the local
#     Periscope Auth0 tenant),
#   - a per-user role whose TRUST POLICY is the source-of-truth gate,
#   - a permission policy letting that role open SSM sessions on the
#     POC instance.
#
# This is the "node-shell" role from issue #105, scoped down to one
# instance for the spike. The probe assumes it via
# sts:AssumeRoleWithWebIdentity using a real id_token (--id-token-file).
#
# ─────────────────────────────────────────────────────────────────────
# IMPORTANT / OPEN QUESTION the spike is meant to answer:
# AssumeRoleWithWebIdentity trust policies reliably support the `aud`
# (and `sub`) condition keys. Whether a *custom, namespaced* array claim
# (e.g. an Auth0 claim like "https://<your-app>/groups") works as a
# condition key (ForAnyValue:StringEquals on "<issuer>:<groups_claim>")
# is the thing to VERIFY empirically — set required_group="" to gate on
# aud only if the group condition turns out not to evaluate. Either
# result is a useful finding for the docs (issue #105's troubleshooting
# matrix) and the blog.
# ─────────────────────────────────────────────────────────────────────

locals {
  # IAM web-identity condition keys are "<issuer-without-scheme>:<claim>".
  # The Auth0 issuer keeps its trailing slash, so the prefix is
  # "dev-...auth0.com/".
  oidc_host = replace(replace(var.oidc_issuer, "https://", ""), "http://", "")
}

data "tls_certificate" "oidc" {
  count = var.enable_oidc ? 1 : 0
  url   = var.oidc_issuer
}

resource "aws_iam_openid_connect_provider" "idp" {
  count          = var.enable_oidc ? 1 : 0
  url            = var.oidc_issuer
  client_id_list = [var.oidc_client_id] # the id_token's aud == the OIDC client_id

  lifecycle {
    precondition {
      condition     = var.oidc_issuer != "" && var.oidc_client_id != ""
      error_message = "enable_oidc=true requires oidc_issuer and oidc_client_id (no tenant is hardcoded — set them in terraform.tfvars or via -var)."
    }
    precondition {
      condition     = var.required_group == "" || var.groups_claim != ""
      error_message = "required_group is set but groups_claim is empty — set groups_claim to the id_token claim that carries groups, or clear required_group to gate on aud only."
    }
  }
  # AWS no longer validates the thumbprint for providers backed by a
  # public CA, but the API still requires the field; fetch the leaf
  # chain's root fingerprint dynamically so this never goes stale.
  thumbprint_list = [data.tls_certificate.oidc[0].certificates[length(data.tls_certificate.oidc[0].certificates) - 1].sha1_fingerprint]
  tags            = local.tags
}

# --- Trust policy: who may assume the role (the source of truth) ---
data "aws_iam_policy_document" "trust" {
  count = var.enable_oidc ? 1 : 0

  statement {
    effect  = "Allow"
    actions = ["sts:AssumeRoleWithWebIdentity"]

    principals {
      type        = "Federated"
      identifiers = [aws_iam_openid_connect_provider.idp[0].arn]
    }

    # aud == the OIDC client_id. NOT the API audience/identifier — that
    # lives on the access token, not the id_token we exchange here.
    # Getting this wrong is the #1 failure.
    condition {
      test     = "StringEquals"
      variable = "${local.oidc_host}:aud"
      values   = [var.oidc_client_id]
    }

    # Group gate — the empirical unknown (see header). Disabled when
    # required_group="".
    dynamic "condition" {
      for_each = var.required_group == "" ? [] : [1]
      content {
        test     = "ForAnyValue:StringEquals"
        variable = "${local.oidc_host}:${var.groups_claim}"
        values   = [var.required_group]
      }
    }
  }
}

resource "aws_iam_role" "node_shell" {
  count                = var.enable_oidc ? 1 : 0
  name                 = "${var.name}-node-shell"
  description          = "Per-user role assumed via OIDC web identity to open SSM node shells (issue #105 spike)"
  assume_role_policy   = data.aws_iam_policy_document.trust[0].json
  max_session_duration = 3600
  tags                 = local.tags
}

# --- Permission policy: what the assumed role may DO ---
data "aws_iam_policy_document" "perms" {
  count = var.enable_oidc ? 1 : 0

  # ssm:StartSession authorizes against BOTH the target instance AND the
  # SSM document it runs — so the policy must grant both resources, or the
  # call is denied on the document (SSM-SessionManagerRunShell is the
  # account's default interactive-shell document). Production can swap this
  # for a locked-down custom document ARN.
  statement {
    sid     = "StartSessionOnPocInstance"
    effect  = "Allow"
    actions = ["ssm:StartSession"]
    resources = [
      "arn:aws:ec2:${var.region}:${data.aws_caller_identity.current.account_id}:instance/${aws_instance.node.id}",
      "arn:aws:ssm:${var.region}:${data.aws_caller_identity.current.account_id}:document/SSM-SessionManagerRunShell",
    ]
  }

  # Terminate/Resume own sessions. Scoped to "*" for the spike;
  # production scopes to session/$${aws:userid}-* .
  statement {
    sid       = "ManageSessions"
    effect    = "Allow"
    actions   = ["ssm:TerminateSession", "ssm:ResumeSession"]
    resources = ["*"]
  }

  # Preflight agent-health check. DescribeInstanceInformation does not
  # support resource-level scoping, so it's "*".
  statement {
    sid       = "PreflightDescribe"
    effect    = "Allow"
    actions   = ["ssm:DescribeInstanceInformation"]
    resources = ["*"]
  }
}

resource "aws_iam_role_policy" "node_shell" {
  count  = var.enable_oidc ? 1 : 0
  name   = "${var.name}-node-shell-perms"
  role   = aws_iam_role.node_shell[0].id
  policy = data.aws_iam_policy_document.perms[0].json
}
