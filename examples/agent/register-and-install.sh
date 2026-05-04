#!/usr/bin/env bash
# register-and-install.sh — reference script for onboarding a managed
# cluster via the periscope-agent (#42).
#
# Two-step flow:
#   1. POST /api/agents/tokens on the central server (admin tier
#      session required) → bootstrap token + 15-min TTL
#   2. helm upgrade --install periscope-agent on the managed cluster
#      with the token wired into the values
#
# This script is illustrative — adapt the auth method (curl cookie,
# bearer token, kubectl proxy) to whatever your central server's
# session model is.

set -euo pipefail

# ─── inputs ──────────────────────────────────────────────────────────
PERISCOPE_URL="${PERISCOPE_URL:-https://periscope.example.com}"
CLUSTER_NAME="${CLUSTER_NAME:-}"
AGENT_SERVER_URL="${AGENT_SERVER_URL:-wss://agents.periscope.example.com:8443}"
AGENT_NAMESPACE="${AGENT_NAMESPACE:-periscope}"
CHART_VERSION="${CHART_VERSION:-1.0.0}"
SESSION_COOKIE="${SESSION_COOKIE:-}"   # contents of periscope_session cookie

if [[ -z "$CLUSTER_NAME" ]]; then
  echo "CLUSTER_NAME is required (DNS-1123: lowercase + digits + dashes, 1-63 chars)" >&2
  exit 1
fi
if [[ -z "$SESSION_COOKIE" ]]; then
  cat >&2 <<EOF
SESSION_COOKIE is required.

Get yours by signing into the SPA in a browser, opening DevTools →
Application → Cookies → periscope_session, copying the value.

Or, if Periscope is in dev mode (no OIDC), set:
  SESSION_COOKIE=dev   # any non-empty value works in dev mode
EOF
  exit 1
fi

# ─── 1. mint token ──────────────────────────────────────────────────
echo "▸ minting bootstrap token for ${CLUSTER_NAME}..."
mint_response=$(curl -fsSX POST "${PERISCOPE_URL}/api/agents/tokens" \
  -H "Cookie: periscope_session=${SESSION_COOKIE}" \
  -H "Content-Type: application/json" \
  -d "{\"cluster\":\"${CLUSTER_NAME}\"}")

token=$(echo "$mint_response" | jq -r .token)
expires_at=$(echo "$mint_response" | jq -r .expiresAt)

if [[ -z "$token" || "$token" == "null" ]]; then
  echo "✗ failed to mint token; response: $mint_response" >&2
  exit 1
fi

echo "✓ token minted (expires ${expires_at})"

# ─── 2. install agent on the current kubectl context ────────────────
echo ""
echo "▸ current kubectl context: $(kubectl config current-context)"
echo "▸ installing periscope-agent (chart ${CHART_VERSION}) into ${AGENT_NAMESPACE}..."
echo ""

helm upgrade --install periscope-agent \
  oci://ghcr.io/gnana997/charts/periscope-agent \
  --version "${CHART_VERSION}" \
  --namespace "${AGENT_NAMESPACE}" \
  --create-namespace \
  --set agent.serverURL="${AGENT_SERVER_URL}" \
  --set agent.clusterName="${CLUSTER_NAME}" \
  --set agent.registrationToken="${token}"

echo ""
echo "✓ helm install complete"
echo ""
echo "▸ watching agent log (Ctrl-C to detach)..."
echo "  expected: 'tunnel.client_connected' within ~10s"
echo ""
kubectl -n "${AGENT_NAMESPACE}" logs -l app.kubernetes.io/name=periscope-agent -f --tail=20
