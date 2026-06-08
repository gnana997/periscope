# POC target instance (Terraform)

Stands up one free-tier-friendly `t2.micro` (Amazon Linux 2023, SSM agent
preinstalled) in your default VPC, with the IAM instance profile the SSM
agent needs to register. No inbound rules — SSM is outbound-only.

This is a **throwaway POC target**, not the production IAM model. There is
no per-user role or OIDC trust policy here; auth is stubbed by ambient
credentials (see [`../README.md`](../README.md)).

## Use

```sh
cd hack/poc-ssm-data-channel/terraform
terraform init
terraform apply            # default region us-east-1, t2.micro
```

Then drive the probe with the outputted id — `terraform apply` prints a
ready-to-paste `run_hint`, or wire it directly:

```sh
make -C "$(git rev-parse --show-toplevel)" poc-ssm-data-channel \
  INSTANCE_ID="$(terraform output -raw instance_id)" \
  AWS_REGION="$(terraform output -raw region)" \
  ASSERT=1
```

Give the SSM agent ~1–2 minutes after `apply` to report **Online**:

```sh
aws ssm describe-instance-information \
  --filters "Key=InstanceIds,Values=$(terraform output -raw instance_id)" \
  --region "$(terraform output -raw region)"
```

## Per-user impersonation role (opt-in)

`enable_oidc=true` additionally creates the **production auth model**: an
IAM OIDC provider + a per-user `node-shell` role whose trust policy is the
source-of-truth gate. **No tenant is hardcoded** — supply your own IdP
details (copy `terraform.tfvars.example` → `terraform.tfvars`):

```sh
cp terraform.tfvars.example terraform.tfvars   # then fill in YOUR issuer/client_id
terraform apply -var enable_oidc=true
# → outputs node_shell_role_arn, oidc_provider_arn, and run_hint_oidc
```

Then drive the probe through the OIDC → STS → per-user-creds chain (see the
spike README's "Per-user impersonation" section). The trust policy gates on:

- `<issuer>:aud == oidc_client_id` — note this is the **id_token's** aud
  (the OIDC client_id), *not* the API audience/identifier (that lives on the
  access token).
- `<issuer>:<groups_claim>` contains `required_group` — **the empirical
  unknown.** If AssumeRole fails only when this condition is present, you've
  found that namespaced array claims aren't usable as IAM condition keys; set
  `-var required_group=""` to gate on `aud` alone and rely on Periscope's tier
  check for group gating. Either outcome is a documented finding (issue #105
  troubleshooting matrix).

> This is a single-instance, spike-scoped version of issue #105's
> `periscope-node-shell` role. Production scopes by instance tag, not a
> hardcoded instance id.

## Tear down

```sh
terraform destroy                          # add -var enable_oidc=true if you set it on apply
```

## Variables

| Variable | Default | Notes |
|---|---|---|
| `region` | `us-east-1` | Any region with a default VPC. |
| `instance_type` | `t2.micro` | Free Tier eligible for new accounts in most regions. |
| `name` | `periscope-ssm-poc` | Prefix for instance + IAM + SG names. |

Override with `terraform apply -var region=eu-west-1`, etc.

## Notes

- Needs a **default VPC** in the chosen region (most accounts have one). If
  yours was deleted, create one or point the config at an existing public
  subnet.
- Free Tier covers 750 hrs/mo of `t2.micro` for the first 12 months; this
  one instance fits, but **`terraform destroy` when done** regardless.
