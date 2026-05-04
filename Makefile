.PHONY: build backend frontend frontend-build tidy clean dev help image kind-load helm-lint helm-template test

# Image / kind defaults — override on the CLI: `make image TAG=v0.2`
IMAGE     ?= periscope
TAG       ?= dev
KIND_NAME ?= certwatch
COMMIT    := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
VERSION   ?= $(TAG)

# Production: build frontend, then Go binary that embeds web/dist via
# the `embed` build tag (internal/spa/embed_on.go).
build: frontend-build
	rm -rf internal/spa/dist
	cp -r web/dist internal/spa/dist
	go build -tags embed -o bin/periscope ./cmd/periscope

# Run the Go backend (port 8080)
backend:
	go run ./cmd/periscope

# Run the Vite dev server (port 5173, proxies /api -> backend)
frontend:
	cd web && npm run dev

# Build the frontend bundle (consumed by `build` for embedding)
frontend-build:
	cd web && npm run build

test:
	go test ./...

image:
	docker build \
	  --build-arg VERSION=$(VERSION) \
	  --build-arg COMMIT=$(COMMIT) \
	  -t $(IMAGE):$(TAG) \
	  .

kind-load: image
	kind load docker-image $(IMAGE):$(TAG) --name $(KIND_NAME)

helm-lint:
	helm lint ./deploy/helm/periscope

# Render the chart with the local image tag + a stub Auth0 config so
# you can eyeball the output without filling in real values.
helm-template:
	helm template periscope ./deploy/helm/periscope \
	  --namespace periscope \
	  --set image.repository=$(IMAGE) \
	  --set image.tag=$(TAG) \
	  --set image.pullPolicy=IfNotPresent \
	  --set auth.oidc.issuer=https://example.auth0.com/ \
	  --set auth.oidc.clientID=test \
	  --set auth.oidc.redirectURL=http://localhost:5173/api/auth/callback \
	  --set auth.oidc.postLogoutRedirect=http://localhost:5173/api/auth/loggedout

tidy:
	go mod tidy
	cd web && npm install

clean:
	rm -rf bin/ web/dist/ web/node_modules/ internal/spa/dist/*

help:
	@echo "Run 'make backend' and 'make frontend' in separate terminals during dev."
	@echo "Run 'make build' to produce the single embedded production binary."

dev: help

# ─── RFC 0004 Tier 2 e2e harness ──────────────────────────────────────
# Bring up a kind cluster running both periscope server and periscope-
# agent, run an exec probe through the agent tunnel, and assert the
# round-trip succeeds. See hack/poc-exec-tunnel/README.md for details.
.PHONY: poc-exec-tunnel poc-exec-tunnel-clean

poc-exec-tunnel:
	./hack/poc-exec-tunnel/run.sh

poc-exec-tunnel-clean:
	kind delete cluster --name periscope-poc 2>/dev/null || true
