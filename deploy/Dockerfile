# Copyright (c) 2026 BITKAIO LLC. All rights reserved.
# Use of this source code is governed by the AGPL-3.0 license.

# =============================================================================
# Stage 1: Build the Go binary
# =============================================================================
FROM golang:1.22-bookworm AS builder

WORKDIR /src

# Cache module downloads.
COPY go.mod go.sum ./
RUN go mod download

# Copy source and build.
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -trimpath -o /palena ./cmd/palena

# =============================================================================
# Stage 2: Minimal runtime image
# =============================================================================
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /palena /palena

# Default config path (mount your palena.yaml here).
ENV PALENA_CONFIG_PATH=/config/palena.yaml

EXPOSE 8080

ENTRYPOINT ["/palena"]