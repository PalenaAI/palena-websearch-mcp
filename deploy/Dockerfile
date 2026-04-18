# Copyright (c) 2026 bitkaio LLC. All rights reserved.
# Licensed under the Apache License, Version 2.0. See LICENSE for details.

# =============================================================================
# Stage 1: Build the Go binary AND download the Playwright driver
# =============================================================================
FROM golang:1.26-bookworm AS builder

WORKDIR /src

# Cache module downloads.
COPY go.mod go.sum ./
RUN go mod download

# Copy source and build.
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -trimpath -o /palena ./cmd/palena

# Download the Playwright driver into /driver via playwright-go's own CLI.
# The driver zip from Microsoft's CDN ships its own `node` binary, so the
# runtime image does not need Node.js installed.
ENV PLAYWRIGHT_DRIVER_PATH=/driver
RUN mkdir -p /driver \
    && go run github.com/playwright-community/playwright-go/cmd/playwright install --no-browsers \
    || true
# The install command returns non-zero when browsers are skipped with no args.
# Verify the driver binary exists; fail the build if it does not.
RUN test -x /driver/node && test -f /driver/package/cli.js

# =============================================================================
# Stage 2: Runtime — minimal Debian (no Node needed; driver ships its own)
# =============================================================================
FROM debian:bookworm-slim

# ca-certificates so the Go binary can talk HTTPS to SearXNG, reranker, etc.
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /palena /palena
COPY --from=builder /driver /driver

ENV PALENA_CONFIG_PATH=/config/palena.yaml
# playwright-go looks here for package/cli.js and the bundled node binary.
ENV PLAYWRIGHT_DRIVER_PATH=/driver

EXPOSE 8080

ENTRYPOINT ["/palena"]
