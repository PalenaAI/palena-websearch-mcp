# Changelog

All notable changes to Palena are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added

- **Prompt-injection defense** via a Hugging Face Text Embeddings Inference (TEI) sidecar serving `deepset/deberta-v3-base-injection`
  - Three modes: `audit` (detect and log), `annotate` (wrap suspicious chunks in `<untrusted-content>` markers), `block` (drop documents containing any over-threshold chunk)
  - Per-paragraph chunked scoring catches short malicious paragraphs hidden inside otherwise legitimate pages
  - Pluggable model — swap `deepset/deberta-v3-base-injection` for any HuggingFace `SequenceClassification` model (e.g. a fine-tuned successor on the same `microsoft/deberta-v3-base` backbone) by changing `injection.predictURL` and the sidecar `--model-id` only
  - Configurable injection-label name (`injection.injectionLabel`) so fine-tuned models with different label conventions work without code changes
  - Audit records that never contain chunk text — only counts, max/mean scores, and over-threshold counts
  - Graceful degradation when the TEI sidecar is unreachable
  - New `injection-guard` service in `deploy/docker-compose.yml`, disabled-by-default `injection:` config block in `palena.yaml`, and `PALENA_INJECTION_*` env overrides
  - Documentation: [`docs/prompt-injection.md`](docs/prompt-injection.md)

## [0.1.0] - 2026-04-01

Initial implementation of the Palena MCP Server.

### Added

- **MCP server** with SSE (`/sse`) and Streamable HTTP (`/mcp`) transports, plus standalone REST API (`/api/v1/search`)
- **`web_search` MCP tool** with input parameters: query, category (general/news/code/science), language, timeRange, maxResults
- **SearXNG search integration** with category-based engine routing, query expansion, and URL deduplication
- **Tiered content extraction**
  - L0: plain HTTP GET with go-readability for server-rendered pages
  - L1: headless Chromium via Chrome DevTools Protocol for JavaScript-rendered pages
  - L2: stealth mode with navigator.webdriver override, viewport/UA randomization, and proxy rotation for bot-protected pages
  - Automatic escalation: L0 -> L1 (if content detection flags JS rendering) -> L2 (if bot-blocked)
  - Graceful degradation when Chromium sidecar is unavailable
- **PII detection and redaction** via Microsoft Presidio
  - Three modes: audit (detect and log), redact (detect and anonymize), block (reject high-density PII documents)
  - Configurable entity types, anonymization strategies, and density thresholds
  - Audit records that never contain actual PII values
  - Graceful degradation when Presidio is unavailable
- **Pluggable reranker subsystem**
  - KServe provider for GPU cross-encoder models (mxbai-rerank)
  - FlashRank provider for CPU ONNX models with Flask sidecar
  - RankLLM provider for LLM-as-reranker via any inference endpoint
  - Noop provider to skip reranking and preserve search engine order
- **Content provenance**
  - Three-stage SHA-256 hash chain: raw HTML, extracted markdown, final content
  - Structured provenance records emitted via slog
  - Optional batched ClickHouse export for audit trail storage
- **OpenTelemetry instrumentation**
  - Distributed tracing with spans for each pipeline stage (search, scrape, PII, rerank, pipeline)
  - Prometheus-compatible metrics: counters (requests, errors, PII entities) and histograms (duration, content length)
  - Configurable exporters: OTLP gRPC, stdout, Prometheus, or disabled
- **Proxy pool** with round-robin rotation and cooldown-on-failure for L2 extraction
- **YAML configuration** with environment variable overrides (`PALENA_*` pattern) and built-in defaults
- **Health endpoint** (`/health`) with sidecar reachability checks
- **Docker deployment**
  - Multi-stage Dockerfile producing a distroless image under 50 MB
  - Full-stack Docker Compose with all sidecars (SearXNG, Presidio, Chromium, FlashRank)
  - Minimal Docker Compose with Palena + SearXNG only
  - FlashRank sidecar Dockerfile and Flask server
  - Pre-configured SearXNG settings with JSON format enabled
- **Helm chart** for Kubernetes/OpenShift with per-sidecar toggles (presidio, chromium, flashrank), ConfigMap-based configuration, and health probes
- **Annotated example configuration** (`config/palena.example.yaml`) documenting every option
- **Subsystem documentation** covering architecture, search, scraper, PII, reranker, MCP transport, configuration, and provenance
