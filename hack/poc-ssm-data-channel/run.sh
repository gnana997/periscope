#!/usr/bin/env bash
# SSM node-shell data-channel POC — issue #105 transport spike.
#
# Proves: ambient AWS creds → ssm:StartSession → session-manager-plugin
# → interactive byte round-trip + transcript capture + clean teardown.
# Auth/STS is deliberately stubbed by the laptop's default credential
# chain, the way poc-exec-tunnel stubbed auth with a dev cookie.
#
# Usage:
#   INSTANCE_ID=i-0abc123 ./hack/poc-ssm-data-channel/run.sh            # interactive shell
#   INSTANCE_ID=i-0abc123 ASSERT=1 ./hack/poc-ssm-data-channel/run.sh   # automated round-trip
#   INSTANCE_ID=i-0abc123 IDLE_SECONDS=30 ./hack/poc-ssm-data-channel/run.sh
#
# Env:
#   INSTANCE_ID   (required) target EC2 instance id
#   AWS_REGION    (or REGION) region; falls back to the default chain
#   ASSERT=1      run the automated echo-token assertion instead of a shell
#   IDLE_SECONDS  kill the session after N idle seconds (0 = off)
#   PROFILE       AWS profile to use (exported as AWS_PROFILE)
#
# Per-user impersonation (opt-in — needs the OIDC role from
# terraform -var enable_oidc=true and a real id_token):
#   ID_TOKEN_FILE  path to a file holding an OIDC id_token (JWT)
#   ROLE_ARN       the node-shell role to assume (terraform output node_shell_role_arn)
#   EXPECTED_AUD   optional aud pre-check (the OIDC client_id)

set -euo pipefail

ROOT="$(git rev-parse --show-toplevel)"
HACK="$ROOT/hack/poc-ssm-data-channel"
INSTANCE_ID="${INSTANCE_ID:-}"
REGION="${AWS_REGION:-${REGION:-}}"
ASSERT="${ASSERT:-}"
IDLE_SECONDS="${IDLE_SECONDS:-0}"

log()  { printf "\n▸ %s\n" "$*"; }
ok()   { printf "✓ %s\n" "$*"; }
die()  { printf "✗ %s\n" "$*" >&2; exit 1; }

# ─── 0. preflight ────────────────────────────────────────────────────
log "preflight: checking required tools"
for bin in go session-manager-plugin; do
  command -v "$bin" >/dev/null || die "$bin not found in PATH"
done
[[ -n "$INSTANCE_ID" ]] || die "INSTANCE_ID is required (e.g. INSTANCE_ID=i-0abc123 $0)"
[[ -n "${PROFILE:-}" ]] && export AWS_PROFILE="$PROFILE"
ok "tooling present; instance=$INSTANCE_ID"

# ─── 1. build probe args ─────────────────────────────────────────────
args=(--instance-id "$INSTANCE_ID" --idle-seconds "$IDLE_SECONDS")
[[ -n "$REGION" ]] && args+=(--region "$REGION")
[[ -n "$ASSERT" ]] && args+=(--assert)

# Per-user impersonation, if requested.
if [[ -n "${ID_TOKEN_FILE:-}" ]]; then
  [[ -n "${ROLE_ARN:-}" ]] || die "ROLE_ARN is required when ID_TOKEN_FILE is set"
  args+=(--id-token-file "$ID_TOKEN_FILE" --role-arn "$ROLE_ARN")
  [[ -n "${EXPECTED_AUD:-}" ]] && args+=(--expected-aud "$EXPECTED_AUD")
  [[ -n "${GROUPS_CLAIM:-}" ]] && args+=(--groups-claim "$GROUPS_CLAIM")
  log "per-user impersonation: assuming $ROLE_ARN via id_token"
fi

# ─── 2. run ──────────────────────────────────────────────────────────
if [[ -n "$ASSERT" ]]; then
  log "running automated round-trip assertion"
else
  log "opening interactive SSM session (type 'exit' or Ctrl-C to end)"
fi
( cd "$HACK" && go run . "${args[@]}" )

ok "SSM data-channel POC complete"
