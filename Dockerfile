# syntax=docker/dockerfile:1.7

# ---- web build ----
FROM --platform=$BUILDPLATFORM node:24-alpine@sha256:d1b3b4da11eefd5941e7f0b9cf17783fc99d9c6fc34884a665f40a06dbdfc94f AS web-builder
WORKDIR /web

# Copy lockfiles first for layer caching, then install. npm ci installs
# exactly what package-lock.json pins and fails on any drift.
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund

COPY web ./
RUN npm run build

# ---- go build ----
FROM --platform=$BUILDPLATFORM golang:1.26-alpine@sha256:91eda9776261207ea25fd06b5b7fed8d397dd2c0a283e77f2ab6e91bfa71079d AS go-builder
WORKDIR /src

RUN apk add --no-cache git ca-certificates

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Place the SPA bundle where //go:embed expects it. internal/spa/dist
# is the canonical location; the embed_on.go file references it via
# `//go:embed all:dist`.
RUN rm -rf internal/spa/dist
COPY --from=web-builder /web/dist /src/internal/spa/dist

# Build with the embed tag so the SPA bundle is baked into the binary.
ARG VERSION=dev
ARG COMMIT=unknown
# Critical: web-builder + go-builder use --platform=$BUILDPLATFORM
# above so they run NATIVELY on the runner arch (linux/amd64), not
# under QEMU emulation. The Go toolchain then cross-compiles to
# TARGETARCH via GOARCH below; CGO_ENABLED=0 keeps the build static
# so no per-arch C toolchain is needed. Without this, go build runs
# under emulation for arm64 and takes 30-60 minutes per arch.
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build \
    -tags embed \
    -trimpath \
    -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" \
    -o /out/periscope \
    ./cmd/periscope

# ---- runtime ----
# Distroless static — minimal base, no shell, non-root by default.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:a9329520abc449e3b14d5bc3a6ffae065bdde0f02667fa10880c49b35c109fd1 AS runtime
COPY --from=go-builder /out/periscope /periscope

# Non-root UID/GID 65532 (provided by distroless:nonroot).
USER 65532:65532

EXPOSE 8080

ENTRYPOINT ["/periscope"]
