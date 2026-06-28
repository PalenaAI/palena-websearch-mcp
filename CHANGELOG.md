# Changelog

All notable changes to Palena are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

## [0.9.1] - 2026-06-28

Security and dependency maintenance release. No API or behavior changes.

### Security

- Bumped the Go toolchain to **1.26.4**, picking up standard-library security fixes (CVE-2026-33811/33814/39820/39836/42499/42504 and related).
- Bumped **`golang.org/x/net` to v0.56.0**, resolving the `x/net/html` advisories flagged by both Trivy and govulncheck (GO-2026-5025/5027/5028/5029/5030; CVE-2026-25680/25681/27136/33814/39821/42502/42506).
- The runtime container image now applies the latest security updates to base Debian packages during build (openssl, libssl3, libgnutls30, libc6, libcap2), clearing the HIGH/CRITICAL OS-package findings.
- Hardened the GitHub Actions workflows (zizmor `--pedantic` is now clean): removed template-injection sinks in `run:` blocks, added workflow concurrency limits, documented every elevated permission, and narrowed the Scorecard workflow's top-level permissions.

### Fixed

- **Helm chart:** corrected the Palena image repository to `ghcr.io/palenaai/palena-websearch-mcp` (was a non-existent `ghcr.io/bitkaio/palena`) and made the image tag default to the chart `appVersion` instead of a stale hardcoded `0.1.0`. The flashrank image repository was aligned to the `palenaai` org (note: that image is not yet published by CI).
- Updated copyright headers on the Helm chart and `deploy/Dockerfile.flashrank` to `bitkaio LLC` + Apache-2.0.

## [0.9.0] - 2026-04-18

Initial public release of the Palena MCP Server.

### Added

- **MCP server** with SSE (`/sse`) and Streamable HTTP (`/mcp`) transports, plus standalone REST API (`/api/v1/search`)
- **`web_search` MCP tool** with input parameters: query, category (general/news/code/science), language, timeRange, maxResults
- **SearXNG search integration** with category-based engine routing, query expansion, and URL deduplication
- **Tiered content extraction** via Playwright
  - L0: plain HTTP GET with go-readability for server-rendered pages
  - L1: headless Chromium via Playwright for JavaScript-rendered pages
  - L2: stealth mode with navigator.webdriver override, viewport/UA randomization, and proxy rotation for bot-protected pages
  - Automatic escalation: L0 -> L1 (if content detection flags JS rendering) -> L2 (if bot-blocked)
  - Graceful degradation when the Playwright sidecar is unavailable
- **PII detection and redaction** via Microsoft Presidio
  - Three modes: audit (detect and log), redact (detect and anonymize), block (reject high-density PII documents)
  - Configurable entity types, anonymization strategies, and density thresholds
  - Audit records that never contain actual PII values
  - Graceful degradation when Presidio is unavailable
- **Prompt-injection defense** via a Hugging Face Text Embeddings Inference (TEI) sidecar serving `deepset/deberta-v3-base-injection`
  - Three modes: `audit` (detect and log), `annotate` (wrap suspicious chunks in `<untrusted-content>` markers), `block` (drop documents containing any over-threshold chunk)
  - Per-paragraph chunked scoring catches short malicious paragraphs hidden inside otherwise legitimate pages
  - Pluggable model — swap `deepset/deberta-v3-base-injection` for any HuggingFace `SequenceClassification` model (e.g. a fine-tuned successor on the same `microsoft/deberta-v3-base` backbone) by changing `injection.predictURL` and the sidecar `--model-id` only
  - Configurable injection-label name (`injection.injectionLabel`) so fine-tuned models with different label conventions work without code changes
  - Audit records that never contain chunk text — only counts, max/mean scores, and over-threshold counts
  - Graceful degradation when the TEI sidecar is unreachable
  - Documentation: [`docs/prompt-injection.md`](docs/prompt-injection.md)
- **Pluggable reranker subsystem**
  - KServe provider for GPU cross-encoder models (mxbai-rerank)
  - FlashRank provider for CPU ONNX models with Flask sidecar
  - RankLLM provider for LLM-as-reranker via any inference endpoint
  - Noop provider to skip reranking and preserve search engine order
- **Domain policy** with allow/blocklists and robots.txt enforcement, evaluated before scraping
- **Content provenance**
  - Three-stage SHA-256 hash chain: raw HTML, extracted markdown, final content
  - Structured provenance records emitted via slog
  - Optional batched ClickHouse export for audit trail storage
- **OpenTelemetry instrumentation**
  - Distributed tracing with spans for each pipeline stage (search, scrape, PII, injection, rerank, pipeline)
  - Prometheus-compatible metrics: counters (requests, errors, PII entities) and histograms (duration, content length)
  - Configurable exporters: OTLP gRPC, stdout, Prometheus, or disabled
- **Proxy pool** with round-robin rotation and cooldown-on-failure for L2 extraction
- **YAML configuration** with environment variable overrides (`PALENA_*` pattern) and built-in defaults
- **Health endpoint** (`/health`) with sidecar reachability checks
- **Docker deployment**
  - Multi-stage Dockerfile producing a runtime image that bundles the Playwright driver subprocess
  - Full-stack Docker Compose with all sidecars (SearXNG, Presidio, Playwright, injection-guard, FlashRank)
  - Minimal Docker Compose with Palena + SearXNG only
  - FlashRank sidecar Dockerfile and Flask server
  - Pre-configured SearXNG settings with JSON format enabled
- **Helm chart** for Kubernetes/OpenShift with per-sidecar toggles (presidio, playwright, injection-guard, flashrank), ConfigMap-based configuration, and health probes
- **Annotated example configuration** (`config/palena.example.yaml`) documenting every option
- **Subsystem documentation** covering architecture, search, scraper, PII, prompt-injection, reranker, MCP transport, configuration, and provenance

### Known issues

- **Injection-guard throughput on long pages is limited by an upstream TEI bug.** The released TEI v1.9 Docker image has a DeBERTa-v2/v3 batching defect — multi-input forward passes fail with `broadcast_mul` shape mismatches. Palena works around it by serializing classifier calls (one HTTP request per chunk), which keeps the classifier correct but means a 70-chunk document spends roughly a minute inside the injection stage. Upstream fix: [huggingface/text-embeddings-inference#846](https://github.com/huggingface/text-embeddings-inference/pull/846), expected in TEI v1.10.0. When the image is bumped, raise `predictConcurrency` in [`internal/injection/tei.go`](internal/injection/tei.go) to restore parallelism.
