#!/usr/bin/env bash
# RFC 0004 Tier 2 — end-to-end exec-over-agent-tunnel harness.
#
# Modes:
#   - Default (KIND_NAME unset): uses current kubectl context. Caller
#     is responsible for the cluster + having images available
#     (registry pull, manual `kind load`, etc.). Simplest when you've
#     already got something running.
#   - KIND_NAME set: assumes a kind cluster of that name. Creates it
#     if missing. Builds + loads images via `kind load`.
#
# Idempotent: helm upgrade --install everywhere, port-forward auto-
# kills on exit, kind create is skipped when the cluster exists.
#
# Usage:
#   ./hack/poc-exec-tunnel/run.sh                       # use current context
#   KIND_NAME=periscope-poc ./hack/poc-exec-tunnel/run.sh
#   SKIP_BUILD=1 ./hack/poc-exec-tunnel/run.sh          # skip image build
#
# Cold ≈ 3-5 min (with image build), warm ≈ 60-90 s.

set -euo pipefail

ROOT="$(git rev-parse --show-toplevel)"
HACK="$ROOT/hack/poc-exec-tunnel"
KIND_NAME="${KIND_NAME:-}"
NAMESPACE="${NAMESPACE:-periscope}"
CLUSTER_NAME="${CLUSTER_NAME:-kind-periscope-poc}"
PROBE_POD="${PROBE_POD:-busybox-probe}"
SKIP_BUILD="${SKIP_BUILD:-}"

log()  { printf "\n▸ %s\n" "$*"; }
ok()   { printf "✓ %s\n" "$*"; }
warn() { printf "⚠ %s\n" "$*"; }
die()  { printf "✗ %s\n" "$*" >&2; exit 1; }

# ─── 0. preflight ────────────────────────────────────────────────────
log "preflight: checking required tools"
required=(kubectl helm go jq)
[[ -n "$KIND_NAME" ]] && required+=(kind docker)
[[ -z "$SKIP_BUILD" && -n "$KIND_NAME" ]] && required+=(docker)
for bin in "${required[@]}"; do
  command -v "$bin" >/dev/null || die "$bin not found in PATH"
done
ok "tooling present"

# ─── 1. cluster ──────────────────────────────────────────────────────
if [[ -n "$KIND_NAME" ]]; then
  log "kind: ensuring cluster '$KIND_NAME' exists"
  if kind get clusters 2>/dev/null | grep -q "^$KIND_NAME\$"; then
    ok "kind cluster '$KIND_NAME' already up"
  else
    kind create cluster --name "$KIND_NAME" --config "$HACK/kind.yaml" \
      || die "kind create failed — try running on an existing context with KIND_NAME unset"
    ok "kind cluster created"
  fi
  kubectl config use-context "kind-$KIND_NAME" >/dev/null
else
  current_ctx="$(kubectl config current-context 2>/dev/null || true)"
  [[ -n "$current_ctx" ]] || die "no current kubectl context (set KIND_NAME or run 'kubectl config use-context ...')"
  ok "using current context: $current_ctx"
fi

# ─── 2. images ───────────────────────────────────────────────────────
if [[ -z "$SKIP_BUILD" ]]; then
  if [[ -n "$KIND_NAME" ]]; then
    log "images: building + loading periscope and periscope-agent into kind '$KIND_NAME'"
    ( cd "$ROOT" && make image kind-load IMAGE=periscope TAG=dev KIND_NAME="$KIND_NAME" )
    docker build -f "$ROOT/Dockerfile.agent" -t periscope-agent:dev "$ROOT"
    kind load docker-image periscope-agent:dev --name "$KIND_NAME"
    ok "images loaded into kind"
  else
    warn "non-kind context — skipping image build/load."
    warn "  Ensure 'periscope:dev' and 'periscope-agent:dev' are reachable from your cluster"
    warn "  (image registry, pre-loaded, or matching pullPolicy: Never with locally-tagged images)."
  fi
else
  warn "SKIP_BUILD=1 — assuming images already available"
fi

# ─── 3. namespace + server install ───────────────────────────────────
log "namespace: ensuring '$NAMESPACE'"
kubectl create namespace "$NAMESPACE" --dry-run=client -o yaml | kubectl apply -f -

log "helm: upgrade-or-install periscope server"
helm upgrade --install periscope "$ROOT/deploy/helm/periscope" \
  --namespace "$NAMESPACE" \
  --values "$HACK/server-values.yaml" \
  --wait --timeout 3m
ok "server up"

# ─── 4. busybox probe pod (the exec target) ─────────────────────────
log "applying busybox pod '$PROBE_POD' as the exec target"
kubectl apply -n default -f - <<YAML
apiVersion: v1
kind: Pod
metadata:
  name: $PROBE_POD
spec:
  containers:
    - name: shell
      image: busybox:1.36
      command: ["sleep", "infinity"]
  restartPolicy: Always
YAML
kubectl -n default wait --for=condition=Ready pod/"$PROBE_POD" --timeout=120s
ok "busybox ready"

# ─── 5. mint bootstrap token via dev session cookie ─────────────────
log "port-forward: server SPA → host :18080 (background)"
( kubectl -n "$NAMESPACE" port-forward svc/periscope 18080:8080 >/tmp/pf-server.log 2>&1 ) &
PF_PID=$!
trap 'kill $PF_PID 2>/dev/null || true' EXIT
# Wait for port-forward to bind.
for i in $(seq 1 30); do
  if curl -fs http://127.0.0.1:18080/api/whoami -o /dev/null 2>&1; then break; fi
  sleep 1
done

log "minting bootstrap token via dev cookie"
mint_response=$(curl -fsSX POST http://127.0.0.1:18080/api/agents/tokens \
  -H "Cookie: periscope_session=dev" \
  -H "Content-Type: application/json" \
  -d "{\"cluster\":\"$CLUSTER_NAME\"}")
token=$(echo "$mint_response" | jq -r .token)
[[ -n "$token" && "$token" != "null" ]] || die "mint failed: $mint_response"
ok "token minted"

# ─── 6. agent install ───────────────────────────────────────────────
log "helm: upgrade-or-install periscope-agent"
helm upgrade --install periscope-agent "$ROOT/deploy/helm/periscope-agent" \
  --namespace "$NAMESPACE" \
  --values "$HACK/agent-values.yaml" \
  --set agent.registrationToken="$token" \
  --wait --timeout 3m
ok "agent up"

log "waiting for agent registration (looking for 'tunnel.agent_connected' in server logs)"
status=0
for i in $(seq 1 30); do
  status=$(kubectl -n "$NAMESPACE" logs deploy/periscope --tail=200 2>/dev/null | grep -c "tunnel.agent_connected" || true)
  [[ "$status" -ge 1 ]] && break
  sleep 2
done
[[ "$status" -ge 1 ]] || die "agent never connected — check 'kubectl -n $NAMESPACE logs deploy/periscope' and 'kubectl -n $NAMESPACE logs deploy/periscope-agent'"
ok "agent registered with server"

# ─── 7. probe ────────────────────────────────────────────────────────
log "running exec probe (host → cluster via port-forward)"
( cd "$HACK" && go run ./probe.go \
    --server http://127.0.0.1:18080 \
    --cluster "$CLUSTER_NAME" \
    --namespace default \
    --pod "$PROBE_POD" \
    --cookie dev )

ok "TIER 2 e2e PASS — exec round-trips through the agent tunnel"
