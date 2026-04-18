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
# GOOS/GOARCH intentionally unset: buildx + QEMU runs this stage on the
# TARGETPLATFORM for each entry in `platforms:` (amd64, arm64), so Go picks
# up the correct target arch natively. The playwright-go driver install
# below also relies on this — it downloads /driver/node matching the arch
# of the builder process.
COPY . .
RUN CGO_ENABLED=0 GOOS=linux \
    go build -ldflags="-s -w" -trimpath -o /palena ./cmd/palena

# Download the Playwright driver into /driver via playwright-go's own CLI.
# The driver zip from Microsoft's CDN ships its own `node` binary, so the
# runtime image does not need Node.js installed.
ENV PLAYWRIGHT_DRIVER_PATH=/driver
RUN mkdir -p /driver \
    && go run github.com/playwright-community/playwright-go/cmd/playwright@v0.5700.1 install --no-browsers \
    || true
# The install command returns non-zero when browsers are skipped with no args.
# Verify the driver binary exists; fail the build if it does not.
RUN test -x /driver/node && test -f /driver/package/cli.js
# Make driver world-readable / world-executable so the non-root palena user
# (uid 10001) in the runtime image can spawn the node subprocess. The
# playwright-go installer drops /driver/node as mode 0744 by default,
# which only the owner (root) can execute.
RUN chmod -R a+rX /driver

# =============================================================================
# Stage 2: Runtime — minimal Debian (no Node needed; driver ships its own)
# =============================================================================
FROM debian:bookworm-slim

# ca-certificates so the Go binary can talk HTTPS to SearXNG, reranker, etc.
# (debian:bookworm-slim pins a single ca-certificates version per release;
# pinning inline here would have to be updated every time the base image
# bumps, which Renovate already handles at the FROM level.)
# hadolint ignore=DL3008
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --system --gid 10001 palena \
    && useradd --system --uid 10001 --gid palena --shell /usr/sbin/nologin palena

COPY --from=builder /palena /palena
COPY --from=builder /driver /driver

ENV PALENA_CONFIG_PATH=/config/palena.yaml
# playwright-go looks here for package/cli.js and the bundled node binary.
ENV PLAYWRIGHT_DRIVER_PATH=/driver

EXPOSE 8080

USER 10001:10001

ENTRYPOINT ["/palena"]
