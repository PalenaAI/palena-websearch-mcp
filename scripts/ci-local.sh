#!/usr/bin/env bash
# Copyright (c) 2026 bitkaio LLC. All rights reserved.
# Licensed under the Apache License, Version 2.0. See LICENSE for details.
#
# Run the same checks GitHub Actions CI runs, locally, in Docker.
# Mirrors .github/workflows/ci.yml so you can get a green-or-red signal
# before pushing.
#
# Usage:
#   scripts/ci-local.sh                 # run all checks
#   scripts/ci-local.sh lint test       # run only selected checks
#   SKIP_CONTAINER_SCAN=1 scripts/ci-local.sh   # skip the slow container build+trivy
#
# Checks (names match the CI job names as closely as possible):
#   lint            golangci-lint (Docker)
#   test            go test -race ./...
#   vulncheck       govulncheck ./...
#   semgrep         Semgrep with the same config set as CI (Docker)
#   hadolint        Hadolint on both Dockerfiles (Docker)
#   zizmor          Zizmor on .github/workflows (Docker)
#   dockerfile      Build deploy/Dockerfile (no push)
#   trivy           Trivy HIGH/CRITICAL scan of the built image (Docker)
#   markdown        markdownlint-cli2 (Docker)
#
# Exit code is non-zero if any selected check fails.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

# --- Pinned tool versions (keep in sync with .github/workflows/ci.yml) -------
SEMGREP_IMAGE="semgrep/semgrep@sha256:d8159ff400a103b21d231a9646452025769552e631df786f508448d2e4eacf86"
HADOLINT_IMAGE="ghcr.io/hadolint/hadolint:v2.14.0-debian"
GOLANGCI_IMAGE="golangci/golangci-lint:latest-alpine"
ZIZMOR_VERSION="1.17.0"
TRIVY_IMAGE="aquasec/trivy:0.70.0"
MARKDOWNLINT_IMAGE="davidanson/markdownlint-cli2:v0.18.1"
LOCAL_IMAGE_TAG="palena:ci-local"

# --- Pretty output ------------------------------------------------------------
C_GREEN=$'\033[32m'; C_RED=$'\033[31m'; C_YELLOW=$'\033[33m'; C_BOLD=$'\033[1m'; C_RESET=$'\033[0m'

FAILED=()
PASSED=()

step() { printf '\n%s==> %s%s\n' "$C_BOLD" "$1" "$C_RESET"; }
ok()   { printf '%s[PASS]%s %s\n' "$C_GREEN" "$C_RESET" "$1"; PASSED+=("$1"); }
fail() { printf '%s[FAIL]%s %s\n' "$C_RED"   "$C_RESET" "$1"; FAILED+=("$1"); }
skip() { printf '%s[SKIP]%s %s\n' "$C_YELLOW" "$C_RESET" "$1"; }

need_docker() {
  if ! command -v docker >/dev/null 2>&1; then
    echo "docker is required but not installed" >&2
    exit 2
  fi
}

# --- Individual checks --------------------------------------------------------

run_lint() {
  step "golangci-lint"
  need_docker
  if docker run --rm -v "$REPO_ROOT:/app" -w /app "$GOLANGCI_IMAGE" \
       golangci-lint run --timeout=5m; then
    ok "lint"
  else
    fail "lint"
  fi
}

run_test() {
  step "go test -race ./..."
  if ! command -v go >/dev/null 2>&1; then
    skip "test (go not installed locally)"
    return
  fi
  if go test -race -coverprofile=coverage.out -covermode=atomic ./...; then
    ok "test"
  else
    fail "test"
  fi
}

run_vulncheck() {
  step "govulncheck"
  if ! command -v go >/dev/null 2>&1; then
    skip "vulncheck (go not installed locally)"
    return
  fi
  if ! command -v govulncheck >/dev/null 2>&1; then
    go install golang.org/x/vuln/cmd/govulncheck@latest
  fi
  if govulncheck ./...; then
    ok "vulncheck"
  else
    fail "vulncheck"
  fi
}

run_semgrep() {
  step "semgrep (same config + severity as CI)"
  need_docker
  if docker run --rm -v "$REPO_ROOT:/src" -w /src "$SEMGREP_IMAGE" \
       semgrep scan \
         --config p/golang \
         --config p/security-audit \
         --config p/owasp-top-ten \
         --config p/dockerfile \
         --severity ERROR \
         --error \
         --metrics=off; then
    ok "semgrep"
  else
    fail "semgrep"
  fi
}

run_hadolint() {
  step "hadolint (deploy/Dockerfile + deploy/Dockerfile.flashrank)"
  need_docker
  local rc=0
  for df in deploy/Dockerfile deploy/Dockerfile.flashrank; do
    printf -- '--- %s ---\n' "$df"
    if ! docker run --rm -i "$HADOLINT_IMAGE" hadolint - < "$df"; then
      rc=1
    fi
  done
  if [ "$rc" -eq 0 ]; then
    ok "hadolint"
  else
    fail "hadolint"
  fi
}

run_zizmor() {
  step "zizmor (fail on HIGH)"
  need_docker
  # zizmor ships an official pip package; run via python:slim to avoid
  # polluting the host Python env.
  if docker run --rm -v "$REPO_ROOT:/src" -w /src python:3.12-slim \
       sh -c "pip install --quiet --disable-pip-version-check 'zizmor==$ZIZMOR_VERSION' \
              && zizmor --min-severity high --format plain .github/workflows/"; then
    ok "zizmor"
  else
    fail "zizmor"
  fi
}

run_dockerfile() {
  step "docker build deploy/Dockerfile"
  need_docker
  if docker build -f deploy/Dockerfile -t "$LOCAL_IMAGE_TAG" .; then
    ok "dockerfile"
  else
    fail "dockerfile"
  fi
}

run_trivy() {
  step "trivy image scan (HIGH/CRITICAL, --ignore-unfixed)"
  need_docker
  if ! docker image inspect "$LOCAL_IMAGE_TAG" >/dev/null 2>&1; then
    echo "$LOCAL_IMAGE_TAG not built; running 'dockerfile' first"
    run_dockerfile
  fi
  if docker run --rm \
       -v /var/run/docker.sock:/var/run/docker.sock \
       "$TRIVY_IMAGE" image \
         --exit-code 1 \
         --severity HIGH,CRITICAL \
         --ignore-unfixed \
         "$LOCAL_IMAGE_TAG"; then
    ok "trivy"
  else
    fail "trivy"
  fi
}

run_markdown() {
  step "markdownlint-cli2"
  need_docker
  if docker run --rm -v "$REPO_ROOT:/workdir" "$MARKDOWNLINT_IMAGE" \
       "**/*.md" "#node_modules" "#tmp" "#vendor"; then
    ok "markdown"
  else
    fail "markdown"
  fi
}

# --- Dispatch -----------------------------------------------------------------

ALL_CHECKS=(lint test vulncheck semgrep hadolint zizmor dockerfile trivy markdown)

if [ "$#" -gt 0 ]; then
  SELECTED=("$@")
else
  SELECTED=("${ALL_CHECKS[@]}")
fi

for check in "${SELECTED[@]}"; do
  case "$check" in
    lint)       run_lint ;;
    test)       run_test ;;
    vulncheck)  run_vulncheck ;;
    semgrep)    run_semgrep ;;
    hadolint)   run_hadolint ;;
    zizmor)     run_zizmor ;;
    dockerfile) run_dockerfile ;;
    trivy)
      if [ "${SKIP_CONTAINER_SCAN:-0}" = "1" ]; then
        skip "trivy (SKIP_CONTAINER_SCAN=1)"
      else
        run_trivy
      fi
      ;;
    markdown)   run_markdown ;;
    *) echo "unknown check: $check" >&2; exit 2 ;;
  esac
done

printf '\n%s=== Summary ===%s\n' "$C_BOLD" "$C_RESET"
for c in ${PASSED[@]+"${PASSED[@]}"}; do printf '%s[PASS]%s %s\n' "$C_GREEN" "$C_RESET" "$c"; done
for c in ${FAILED[@]+"${FAILED[@]}"}; do printf '%s[FAIL]%s %s\n' "$C_RED"   "$C_RESET" "$c"; done

if [ "${#FAILED[@]}" -gt 0 ]; then
  printf '\n%s%d check(s) failed.%s\n' "$C_RED" "${#FAILED[@]}" "$C_RESET"
  exit 1
fi
printf '\n%sAll checks passed.%s\n' "$C_GREEN" "$C_RESET"
