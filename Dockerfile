# syntax=docker/dockerfile:1

# ---- build stage ----
# Always build on the native runner platform; use Go cross-compilation
# to target the requested architecture. This avoids QEMU emulation for
# the compile step, which can be 10-20x slower on arm64.
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS builder

ARG TARGETARCH

# modernc.org/sqlite is pure Go; no CGO or sqlite-dev headers required.
ENV GONOSUMDB=*
ENV GOFLAGS=-mod=mod
ENV CGO_ENABLED=0
ENV GOOS=linux

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" \
    -o /out/xolu ./cmd/xolu

RUN GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" \
    -o /out/iolu ./cmd/iolu

# ---- runtime stage ----
FROM alpine:3.19

RUN apk add --no-cache ca-certificates tzdata \
    && adduser -D -H -u 1001 xolu

# Data directory — mount a volume here for persistent storage.
RUN mkdir -p /data && chown xolu:xolu /data

COPY --from=builder /out/xolu  /usr/local/bin/xolu
COPY --from=builder /out/iolu /usr/local/bin/iolu

USER xolu
WORKDIR /data

# ---- configuration via environment variables ----
#
# Primary variables (xolu's canonical names):
#   XOLU_HOST              Listen host              (default: 0.0.0.0)
#   XOLU_PORT              Listen port              (default: 9090)
#   XOLU_STORAGE_TYPE      Storage backend          (sqlite|jsonfile, default: sqlite)
#   XOLU_DB_PATH           SQLite database path     (default: xolu.db)
#   XOLU_AUTH_TYPE         Auth mechanism           (none|jwt|apikey|bearertoken)
#   XOLU_INTERNAL_TOKEN    Bearer token secret      (or /run/secrets/xolu_internal_token)
#   XOLU_JWT_SECRET        JWT HMAC secret          (or /run/secrets/xolu_jwt_secret)
#   XOLU_LOG_LEVEL         Log level                (debug|info|warn|error, default: info)
#   XOLU_METRICS_ENABLED   Enable Prometheus metrics (default: false)
#   XOLU_METRICS_HOST      Metrics listen host      (default: 0.0.0.0)
#   XOLU_METRICS_PORT      Metrics listen port      (default: 9091)
#
# Convenience aliases:
#   XOLU_ADDR              host:port  →  XOLU_HOST + XOLU_PORT
#   XOLU_METRICS_ADDR      host:port  →  XOLU_METRICS_HOST + XOLU_METRICS_PORT
#   XOLU_SQLITE_PATH       path       →  XOLU_DB_PATH
#
# Docker secret mounts (used when the corresponding env var is not set):
#   /run/secrets/xolu_internal_token
#   /run/secrets/xolu_jwt_secret

EXPOSE 9090

ENTRYPOINT ["/usr/local/bin/xolu"]
