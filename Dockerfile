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
FROM --platform=$BUILDPLATFORM golang:1.26-alpine@sha256:f23e8b227fb4493eabe03bede4d5a32d04092da71962f1fb79b5f7d1e6c2a17f AS go-builder
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

# ---- session-manager-plugin (SSM node shell, #105) ----
# The node-shell handler shells out to the AWS-maintained
# session-manager-plugin to drive the SSM data channel. Extract the
# binary from AWS's official .deb (the distroless runtime has no package
# manager). It is dynamically linked against glibc — which is why the
# runtime below is distroless/base (glibc), not distroless/static.
FROM debian:12-slim@sha256:0104b334637a5f19aa9c983a91b54c89887c0984081f2068983107a6f6c21eeb AS ssm-plugin
ARG TARGETARCH
RUN apt-get update \
 && apt-get install -y --no-install-recommends curl ca-certificates \
 && rm -rf /var/lib/apt/lists/*
RUN set -eux; \
    case "${TARGETARCH}" in \
      amd64) smp_arch=64bit ;; \
      arm64) smp_arch=arm64 ;; \
      *) echo "unsupported TARGETARCH: ${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    curl -fsSL "https://s3.amazonaws.com/session-manager-downloads/plugin/latest/ubuntu_${smp_arch}/session-manager-plugin.deb" -o /tmp/smp.deb; \
    dpkg-deb -x /tmp/smp.deb /tmp/smp; \
    install -D /tmp/smp/usr/local/sessionmanagerplugin/bin/session-manager-plugin /out/session-manager-plugin

# ---- runtime ----
# Distroless base (glibc) — minimal, no shell, non-root. `base` rather
# than `static` because session-manager-plugin (copied below) is
# dynamically linked against glibc.
FROM gcr.io/distroless/base-debian12:nonroot@sha256:7a75a36f4bec82a7542c64195e402907486f9a4dd2f8797a976aa0cf31cfb470 AS runtime
COPY --from=go-builder /out/periscope /periscope
# session-manager-plugin on PATH for the SSM node shell (#105). Always
# present (Docker can't conditionally COPY); the feature stays gated by
# nodeShell.enabled and the handler's exec.LookPath check.
COPY --from=ssm-plugin /out/session-manager-plugin /usr/local/bin/session-manager-plugin

# Non-root UID/GID 65532 (provided by distroless:nonroot).
USER 65532:65532

EXPOSE 8080

ENTRYPOINT ["/periscope"]
