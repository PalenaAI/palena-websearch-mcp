# Palena

Enterprise-grade web search MCP server for regulated industries.

Palena exposes a single MCP tool (`web_search`) that orchestrates a five-stage pipeline: search, scrape, PII detection, reranking, and formatting. Every piece of content is hashed, audited, and optionally redacted before reaching the LLM.

The name "Palena" is Hawaiian for *boundary, limit, border* — reflecting the product's focus on enforcing compliance boundaries around AI-powered web access.

## Pipeline

```
MCP Request
    |
    v
+-----------+     +----------+     +-----+     +--------+     +--------+
|  1.SEARCH | --> | 2.SCRAPE | --> | 3.PII| --> |4.RERANK| --> |5.FORMAT|
|  SearXNG  |     | L0/L1/L2 |     |Presidio|   |Pluggable|   |Markdown|
+-----------+     +----------+     +-----+     +--------+     +--------+
    |                                                              |
    v                                                              v
 query expansion                                        citations, provenance
 engine routing                                         hashes, token chunks
```

1. **Search** -- queries SearXNG for results with category-based engine routing
2. **Scrape** -- tiered extraction: HTTP + Readability (L0) -> headless Chromium (L1) -> stealth + proxy (L2)
3. **PII** -- Microsoft Presidio analysis with configurable modes: audit, redact, or block
4. **Rerank** -- pluggable cross-encoder models (KServe GPU, FlashRank CPU, LLM-as-reranker, or none)
5. **Format** -- LLM-ready markdown with inline citations, source URLs, and SHA-256 provenance hashes

## Features

- **Tiered scraping** -- always tries the cheapest method first; escalates to browser rendering only when needed
- **PII compliance** -- detect, redact, or block documents containing personal data; audit records never contain PII values
- **Content provenance** -- three-stage SHA-256 hash chain (raw HTML -> extracted markdown -> final content) for verifiable data lineage
- **Pluggable reranking** -- swap between GPU cross-encoders, CPU ONNX models, LLM-based scoring, or no reranking
- **OpenTelemetry** -- distributed traces and Prometheus metrics across all pipeline stages
- **Graceful degradation** -- optional sidecars (Presidio, Chromium, reranker) can be absent; pipeline continues with reduced capability
- **Config-driven** -- YAML config with environment variable overrides; no hardcoded behavior
- **Single binary** -- Go static binary (~15 MB), no Python/Node runtime in the core service

## Quick Start

### Full stack (all sidecars)

```bash
docker compose -f deploy/docker-compose.yml up --build
```

Services: Palena, SearXNG, Presidio Analyzer, Presidio Anonymizer, Chromium, FlashRank.

### Minimal (L0 scraping only)

```bash
docker compose -f deploy/docker-compose.minimal.yml up --build
```

Services: Palena + SearXNG only. No PII detection, no browser, no reranking.

### Test the endpoint

```bash
# Health check
curl http://localhost:8080/health

# REST API
curl -X POST http://localhost:8080/api/v1/search \
  -H "Content-Type: application/json" \
  -d '{"query": "kubernetes RBAC best practices", "maxResults": 3}'
```

## MCP Client Integration

### LibreChat

Add to `librechat.yaml`:

```yaml
mcpServers:
  palena:
    type: sse
    url: http://palena:8080/sse
```

Or with Streamable HTTP:

```yaml
mcpServers:
  palena:
    type: streamableHttp
    url: http://palena:8080/mcp
```

### Claude Desktop

Add to your MCP server configuration:

```json
{
  "mcpServers": {
    "palena": {
      "url": "http://localhost:8080/sse"
    }
  }
}
```

## MCP Tool: `web_search`

```json
{
  "name": "web_search",
  "inputSchema": {
    "type": "object",
    "properties": {
      "query":      { "type": "string", "description": "The search query" },
      "category":   { "type": "string", "enum": ["general", "news", "code", "science"], "default": "general" },
      "language":   { "type": "string", "default": "en" },
      "timeRange":  { "type": "string", "enum": ["day", "week", "month", "year"] },
      "maxResults": { "type": "integer", "default": 5, "minimum": 1, "maximum": 20 }
    },
    "required": ["query"]
  }
}
```

The response includes formatted markdown with numbered results, relevance scores, source citations, and metadata (PII status, reranker used, search engines).

## Configuration

Copy the annotated example and adjust for your deployment:

```bash
cp config/palena.example.yaml config/palena.yaml
```

Key settings:

| Section | What it controls | Key env overrides |
|---------|-----------------|-------------------|
| `search` | SearXNG URL, engines, language | `PALENA_SEARCH_SEARXNG_URL` |
| `scraper` | Concurrency, timeouts, Chromium endpoint | `PALENA_SCRAPER_CHROMIUM_ENDPOINT` |
| `pii` | Mode (audit/redact/block), Presidio URLs | `PALENA_PII_ENABLED`, `PALENA_PII_MODE` |
| `reranker` | Provider (kserve/flashrank/rankllm/none) | `PALENA_RERANKER_PROVIDER` |
| `provenance` | Hash chain, ClickHouse export | `PALENA_PROVENANCE_ENABLED` |
| `otel` | Tracing and metrics exporters | `PALENA_OTEL_ENABLED` |

See [`config/palena.example.yaml`](config/palena.example.yaml) for the full annotated reference.

## Architecture

Palena is a Go orchestrator that coordinates external sidecars over HTTP and WebSocket. All sidecars are optional except SearXNG.

| Sidecar | Image | Protocol | Required | Purpose |
|---------|-------|----------|----------|---------|
| SearXNG | `searxng/searxng` | HTTP JSON | Yes | Metasearch aggregation |
| Presidio Analyzer | `mcr.microsoft.com/presidio-analyzer` | HTTP JSON | No | PII entity detection |
| Presidio Anonymizer | `mcr.microsoft.com/presidio-anonymizer` | HTTP JSON | No | PII masking/replacement |
| Chromium | `browserless/chromium` | WebSocket CDP | No | JS-rendered page extraction |
| FlashRank | Custom Flask wrapper | HTTP JSON | No | CPU-based reranking |
| KServe | KServe InferenceService | HTTP JSON | No | GPU-based reranking |

### Deployment Profiles

| Profile | Components | Footprint |
|---------|-----------|-----------|
| **Minimal** | Palena + SearXNG | ~200 MB |
| **Standard** | + Presidio + Chromium + FlashRank | ~2-3 GB |
| **Enterprise** | + KServe GPU reranker | Variable (GPU) |

### Helm

```bash
# Standard deployment
helm install palena deploy/helm/palena/

# Minimal (no sidecars)
helm install palena deploy/helm/palena/ \
  --set presidio.enabled=false \
  --set chromium.enabled=false \
  --set flashrank.enabled=false

# With FlashRank reranking
helm install palena deploy/helm/palena/ \
  --set flashrank.enabled=true
```

## Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/sse` | MCP SSE transport (event stream) |
| `POST` | `/mcp` | MCP Streamable HTTP transport |
| `POST` | `/api/v1/search` | REST API (non-MCP) |
| `GET` | `/health` | Sidecar health checks |
| `GET` | `/metrics` | Prometheus metrics (when OTel metrics enabled) |

## Project Structure

```
palena-websearch-mcp/
├── cmd/palena/              # Binary entry point
├── internal/
│   ├── config/              # YAML config parsing and validation
│   ├── search/              # SearXNG client, query expansion, dedup
│   ├── scraper/             # L0/L1/L2 extraction, proxy pool, stealth
│   ├── pii/                 # Presidio client, policy modes, audit records
│   ├── reranker/            # Pluggable reranker interface and providers
│   ├── output/              # Markdown formatting, provenance hashes
│   ├── transport/           # MCP + REST server, tool handler
│   └── otel/                # OpenTelemetry tracing and metrics
├── config/                  # Default and example YAML configs
├── deploy/                  # Dockerfiles, Compose files, Helm chart
└── docs/                    # Subsystem documentation
```

## Documentation

| Document | Description |
|----------|-------------|
| [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) | System design, component interactions, deployment |
| [`docs/SEARCH.md`](docs/SEARCH.md) | SearXNG integration, query expansion, engine routing |
| [`docs/SCRAPER.md`](docs/SCRAPER.md) | Tiered extraction (L0/L1/L2), content detection |
| [`docs/PII.md`](docs/PII.md) | Presidio integration, PII modes, audit logging |
| [`docs/RERANKER.md`](docs/RERANKER.md) | Pluggable reranker interface, model options |
| [`docs/MCP.md`](docs/MCP.md) | MCP transport, tool schema, response format |
| [`docs/CONFIG.md`](docs/CONFIG.md) | Full configuration reference |
| [`docs/PROVENANCE.md`](docs/PROVENANCE.md) | Content provenance, hash chains, audit records |

## Technology Stack

- **Language:** Go 1.22+
- **Search backend:** SearXNG (self-hosted metasearch)
- **Content extraction:** go-readability (L0), chromedp (L1/L2)
- **PII:** Microsoft Presidio Analyzer + Anonymizer
- **Reranking:** KServe cross-encoder, FlashRank ONNX, RankLLM
- **Observability:** OpenTelemetry traces + Prometheus metrics
- **Transport:** MCP SSE, Streamable HTTP, REST API
- **Configuration:** YAML + environment variable overrides
- **Deployment:** Docker Compose, Helm (Kubernetes/OpenShift)

## License

Copyright (c) 2026 BITKAIO LLC. All rights reserved.

Licensed under the [GNU Affero General Public License v3.0](LICENSE) with a commercial dual-licensing option. Contact BITKAIO LLC for commercial licensing inquiries.
