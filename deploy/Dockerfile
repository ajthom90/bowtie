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
# Release builds pass the tag (sans "v"); local builds keep the dev default.
ARG VERSION=0.1.0-dev
RUN go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /bowtie ./cmd/bowtie

# ---------------------------------------------------------------------------
# Stage 3: Comskip (commercial detection) from a pinned commit, linked against
# the same Debian FFmpeg libraries the runtime's ffmpeg package installs.
# ---------------------------------------------------------------------------
FROM debian:bookworm-slim AS comskip

ARG COMSKIP_COMMIT=a140b6ac8bc8f596729e9052819affc779c3b377

RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      ca-certificates git build-essential autoconf automake libtool pkg-config \
      libargtable2-dev libavformat-dev libavcodec-dev libavutil-dev libswscale-dev \
 && rm -rf /var/lib/apt/lists/*

WORKDIR /src/comskip
RUN git init -q . \
 && git remote add origin https://github.com/erikkaashoek/Comskip.git \
 && git fetch -q --depth 1 origin "$COMSKIP_COMMIT" \
 && git checkout -q FETCH_HEAD \
 && ./autogen.sh \
 && ./configure \
 && make -j"$(nproc)" comskip \
 && install -m 0755 comskip /usr/local/bin/comskip

# ---------------------------------------------------------------------------
# Stage 4: Runtime — FFmpeg + VAAPI/QSV drivers + Comskip + tini
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
      libargtable2-0 \
      mesa-va-drivers \
      tini \
 && if [ "$TARGETARCH" = "amd64" ]; then \
      # iHD driver for VAAPI + oneVPL GPU runtime so FFmpeg's QSV path works on
      # Gen12+ iGPUs (Alder/Raptor Lake etc.) — without libmfx-gen1.2 the QSV
      # probe fails on those chips even though the hardware is present.
      apt-get install -y --no-install-recommends \
        intel-media-va-driver-non-free \
        libmfx-gen1.2 \
        libvpl2; \
    fi \
 && rm -rf /var/lib/apt/lists/*

COPY --from=server /bowtie /usr/local/bin/bowtie
COPY --from=comskip /usr/local/bin/comskip /usr/local/bin/comskip

ENV BOWTIE_DATA_DIR=/data
ENV BOWTIE_COMSKIP_PATH=/usr/local/bin/comskip
EXPOSE 8400
VOLUME /data

ENTRYPOINT ["tini", "--", "/usr/local/bin/bowtie"]
