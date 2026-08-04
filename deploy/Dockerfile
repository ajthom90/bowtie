# syntax=docker/dockerfile:1
# Multi-stage build for Bowtie. Build context = repository root.
#
#   docker build -f deploy/Dockerfile -t bowtie:dev .

# ---------------------------------------------------------------------------
# Stage 1: Vite / React web UI → server/internal/web/dist
# ---------------------------------------------------------------------------
FROM node:22-slim AS web

WORKDIR /src

COPY web/package.json web/package-lock.json /src/web/
WORKDIR /src/web
RUN npm ci

COPY web/ /src/web/
# vite.config.ts outDir is ../server/internal/web/dist
RUN npm run build

# ---------------------------------------------------------------------------
# Stage 2: Go server (embeds dist), CGO disabled for static binary
# ---------------------------------------------------------------------------
FROM golang:1.22 AS server

WORKDIR /src/server

COPY server/go.mod server/go.sum ./
RUN go mod download

COPY server/ ./
COPY --from=web /src/server/internal/web/dist ./internal/web/dist

ENV CGO_ENABLED=0
RUN go build -trimpath -ldflags="-s -w" -o /bowtie ./cmd/bowtie

# ---------------------------------------------------------------------------
# Stage 3: Runtime — FFmpeg + VAAPI/QSV drivers + tini
# ---------------------------------------------------------------------------
FROM debian:bookworm-slim

# Enable non-free + non-free-firmware (required for intel-media-va-driver-non-free).
RUN sed -i 's/Components: main/Components: main contrib non-free non-free-firmware/' \
      /etc/apt/sources.list.d/debian.sources

# TARGETARCH is set by BuildKit (amd64, arm64, ...). Intel QSV packages are
# amd64-only; skip them on other arches so local arm64 builds still succeed.
ARG TARGETARCH

RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      ca-certificates \
      ffmpeg \
      mesa-va-drivers \
      tini \
 && if [ "$TARGETARCH" = "amd64" ]; then \
      apt-get install -y --no-install-recommends intel-media-va-driver-non-free; \
    fi \
 && rm -rf /var/lib/apt/lists/*

COPY --from=server /bowtie /usr/local/bin/bowtie

ENV BOWTIE_DATA_DIR=/data
EXPOSE 8400
VOLUME /data

ENTRYPOINT ["tini", "--", "/usr/local/bin/bowtie"]
