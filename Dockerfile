# syntax=docker/dockerfile:1.7@sha256:a57df69d0ea827fb7266491f2813635de6f17269be881f696fbfdf2d83dda33e

FROM --platform=$BUILDPLATFORM golang:1.26.6-alpine@sha256:3889b425f035be855a72fb4755265311293b6d414521f0a519d819df32222d83 AS builder

ARG TARGETOS
ARG TARGETARCH

WORKDIR /app

COPY go.mod go.sum* ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

COPY . .

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-$(go env GOARCH)} go build -o service ./cmd/asset-optimizer

FROM node:24.19.0-alpine3.24@sha256:d32cdf619f63fe0471182d08996dd516c6275bb5fd31ae06e55a570bd9e1ad43

WORKDIR /app

RUN apk add --no-cache ca-certificates tzdata wget

COPY package.json package-lock.json ./
RUN npm ci --omit=dev \
    && ln -s /app/node_modules/.bin/gltf-transform /usr/local/bin/gltf-transform \
    && gltf-transform --version

COPY --from=builder /app/service ./service
COPY scripts/optimize-particle-mesh.mjs ./scripts/optimize-particle-mesh.mjs

RUN mkdir -p /tmp/asset-optimizer && chmod 755 /tmp/asset-optimizer

EXPOSE 3030

ENV PORT=3030 \
    LOG_LEVEL=info \
    GLTF_TRANSFORM_PATH=/usr/local/bin/gltf-transform \
    NODE_BINARY_PATH=/usr/local/bin/node \
    PARTICLE_MESH_SCRIPT_PATH=/app/scripts/optimize-particle-mesh.mjs \
    ASSET_OPTIMIZER_TEMP_DIR=/tmp/asset-optimizer \
    MAX_INPUT_BYTES=52428800 \
    WORKER_COUNT=1 \
    JOB_TIMEOUT_MINUTES=20

HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD /bin/sh -c 'wget -q --spider "http://127.0.0.1:${PORT:-3030}/health" || exit 1'

CMD ["./service"]
