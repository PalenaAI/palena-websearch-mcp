# Palena

**Enterprise-grade web search for AI — with compliance boundaries built in.**

[![License: Apache 2.0](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/go-1.26%2B-00ADD8.svg)](https://go.dev/)
[![MCP](https://img.shields.io/badge/MCP-SSE%20%7C%20Streamable%20HTTP-6B46C1.svg)](https://modelcontextprotocol.io/)
[![OpenTelemetry](https://img.shields.io/badge/observability-OpenTelemetry-425CC7.svg)](https://opentelemetry.io/)

Palena is a single-binary [MCP](https://modelcontextprotocol.io/) server that turns the noisy public web into LLM-ready context — **searched, scraped, de-PII'd, reranked, and hash-chained for audit** — so your agents can browse without tripping every compliance officer on the floor.

Built for fintech, healthtech, and govtech teams who cannot send raw third-party HTML directly to a foundation model, but still need their AI to have fresh, cited, reproducible knowledge from the live internet.

> *Palena* — Hawaiian for *boundary, limit, border*.

---

## Why Palena

| Problem | What most "web search" tools do | What Palena does |
|---|---|---|
| Raw pages leak PII into prompts | Ship HTML as-is | Presidio audit/redact/block *before* the LLM sees it |
| Bot-protected sites return nothing | Fail silently | Tiered L0→L1→L2 escalation (readability → headless → stealth+proxy) |
| "We scraped this" has no receipts | No trace | Three-stage SHA-256 hash chain per document |
| Results are ranked by a generic search engine | Trust the top 10 | Pluggable reranker (KServe GPU, FlashRank CPU, RankLLM, or none) |
| Compliance only gets shown a demo | Hope for the best | Domain allow/blocklists, robots.txt enforcement, per-domain rate limits, OTel traces on every span |

---

## The Pipeline

```
                  MCP tools/call web_search
                            │
                            ▼
 ┌──────────────────────────────────────────────────────────────┐
 │  1. SEARCH      SearXNG metasearch                           │
 │                 query expansion · engine routing · dedup     │
 ├──────────────────────────────────────────────────────────────┤
 │  2. POLICY      domain allow/block · robots.txt · rate limit │
 ├──────────────────────────────────────────────────────────────┤
 │  3. SCRAPE      L0  HTTP + readability       (fast path)     │
 │                 L1  Playwright headless      (needs JS)      │
 │                 L2  Playwright + stealth/proxy (anti-bot)    │
 ├──────────────────────────────────────────────────────────────┤
 │  4. PII         Microsoft Presidio · audit / redact / block  │
 ├──────────────────────────────────────────────────────────────┤
 │  5. RERANK      FlashRank · KServe cross-encoder · RankLLM   │
 ├──────────────────────────────────────────────────────────────┤
 │  6. FORMAT      markdown · citations · SHA-256 provenance    │
 └──────────────────────────────────────────────────────────────┘
                            │
                            ▼
                     MCP tool_result
```

Every stage is an OpenTelemetry span. Every document gets three hashes (raw HTML → extracted markdown → final delivered content). The audit trail is ready to ship to ClickHouse out of the box.

---

## Live Example

Real output from the MCP server answering *"latest AI hot topics 2026"* (category: `news`):

```json
{
  "query": "latest AI hot topics 2026",
  "result_count": 3,
  "reranker_used": "flashrank",
  "pii_mode": "audit",
  "pii_checked": true,
  "search_engines": ["google news", "duckduckgo", "bing news"],
  "total_duration_ms": 4414,
  "results": [
    { "title": "OpenClaw Exposes the Real Cybersecurity Risks of Agentic AI",
      "score": 0.984, "scraper_level": 0,
      "content_hash": "c3b4d690…" },
    { "title": "Comprehensive AI Conference List for 2026: Dates, Locations, and Keynotes",
      "score": 0.973, "scraper_level": 0,
      "content_hash": "7af1205e…" },
    { "title": "Can AI infrastructure costs be a value driver? — Oracle AI World 2026",
      "score": 0.002, "scraper_level": 0,
      "content_hash": "9e48c311…" }
  ]
}
```

Note how FlashRank pushes the substantive pieces to the top (0.98 / 0.97) and demotes the vendor-conference writeup that happens to contain the right keywords (0.002) — signal the stock SearXNG ranking does not surface.

---

## Quick Start

### Full stack (all sidecars)

```bash
docker compose -f deploy/docker-compose.yml up --build
```

Brings up Palena + SearXNG + Presidio Analyzer + Presidio Anonymizer + Playwright + FlashRank.

### Minimal (L0 scraping only)

```bash
docker compose -f deploy/docker-compose.minimal.yml up --build
```

Palena + SearXNG only. ~200 MB image, no browser, no PII, no reranking.

### Smoke-test the server

```bash
# Sidecar health
curl http://localhost:8080/health
# → {"status":"ok","sidecars":{"searxng":"ok","presidio-analyzer":"ok",...}}

# List the exposed tool over MCP Streamable HTTP
SESS=$(curl -sS -X POST http://localhost:8080/mcp \
  -H 'content-type: application/json' \
  -H 'accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"curl","version":"0.1"}}}' \
  -D - -o /dev/null | awk '/Mcp-Session-Id/ {print $2}' | tr -d '\r')

curl -sS -X POST http://localhost:8080/mcp \
  -H 'content-type: application/json' \
  -H 'accept: application/json, text/event-stream' \
  -H "Mcp-Session-Id: $SESS" \
  -d '{"jsonrpc":"2.0","method":"notifications/initialized"}'

curl -sS -X POST http://localhost:8080/mcp \
  -H 'content-type: application/json' \
  -H 'accept: application/json, text/event-stream' \
  -H "Mcp-Session-Id: $SESS" \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/list"}'
```

---

## MCP Client Integration

### LibreChat

```yaml
# librechat.yaml
mcpServers:
  palena:
    type: streamableHttp      # or: sse
    url: http://palena:8080/mcp
```

### Claude Desktop

```json
{
  "mcpServers": {
    "palena": { "url": "http://localhost:8080/sse" }
  }
}
```

### Cursor / Windsurf / custom agents

Any MCP-compatible client works — connect to `/sse` (legacy event stream) or `/mcp` (Streamable HTTP).

---

## The `web_search` Tool

```json
{
  "name": "web_search",
  "inputSchema": {
    "type": "object",
    "properties": {
      "query":      { "type": "string",  "description": "The search query" },
      "category":   { "type": "string",  "description": "general | news | code | science (default general)" },
      "language":   { "type": "string",  "description": "ISO code — en, de, fr… (default en)" },
      "timeRange":  { "type": "string",  "description": "day | week | month | year" },
      "maxResults": { "type": "integer", "description": "1–20 (default 5)" }
    },
    "required": ["query"]
  }
}
```

The response is formatted markdown with numbered results, relevance scores, source citations, and structured metadata (PII status, reranker, engines hit, total latency).

---

## Configuration

Start from the annotated example:

```bash
cp config/palena.example.yaml config/palena.yaml
```

| Section | Controls | Key env override |
|---|---|---|
| `search` | SearXNG URL, engines per category, default language | `PALENA_SEARCH_SEARXNG_URL` |
| `scraper` | Concurrency, timeouts, Playwright WS endpoint, proxy pool | `PALENA_SCRAPER_PLAYWRIGHT_ENDPOINT` |
| `pii` | Mode (audit / redact / block), Presidio URLs, entities | `PALENA_PII_MODE` |
| `reranker` | Provider (`kserve` / `flashrank` / `rankllm` / `none`) | `PALENA_RERANKER_PROVIDER` |
| `policy` | Domain allow/blocklists, robots.txt cache, rate limits | `PALENA_POLICY_DOMAIN_MODE` |
| `provenance` | Hash chain, ClickHouse export | `PALENA_PROVENANCE_ENABLED` |
| `otel` | Trace + metric exporters | `PALENA_OTEL_ENABLED` |

Full annotated reference: [`config/palena.example.yaml`](config/palena.example.yaml) · deep-dive: [`docs/CONFIG.md`](docs/CONFIG.md).

---

## Sidecar Matrix

Palena is a Go orchestrator. Every external capability runs as its own container and is optional except SearXNG.

| Sidecar | Image | Protocol | Required | Purpose |
|---|---|---|---|---|
| SearXNG | `searxng/searxng` | HTTP JSON | **Yes** | Metasearch aggregation |
| Presidio Analyzer | `mcr.microsoft.com/presidio-analyzer` | HTTP JSON | No | PII entity detection |
| Presidio Anonymizer | `mcr.microsoft.com/presidio-anonymizer` | HTTP JSON | No | PII masking / replacement |
| Playwright | `mcr.microsoft.com/playwright` | Playwright WS | No | JS-rendered page extraction (L1/L2) |
| FlashRank | Flask + ONNX (bundled) | HTTP JSON | No | CPU cross-encoder reranking |
| KServe | Your own InferenceService | HTTP JSON | No | GPU cross-encoder reranking |

Missing sidecars trigger **graceful degradation**, not failure — L1/L2 disabled, PII reported as "not checked," reranker falls back to search-engine order.

### Deployment Profiles

| Profile | Components | Footprint |
|---|---|---|
| **Minimal** | Palena + SearXNG | ~200 MB |
| **Standard** | + Presidio + Playwright + FlashRank | ~2–3 GB |
| **Enterprise** | + KServe GPU reranker (e.g. `mxbai-rerank`) | Variable (GPU) |

### Helm (Kubernetes / OpenShift)

```bash
# Standard
helm install palena deploy/helm/palena/

# Minimal — disable all optional sidecars
helm install palena deploy/helm/palena/ \
  --set presidio.enabled=false \
  --set playwright.enabled=false \
  --set flashrank.enabled=false

# With your own GPU reranker
helm install palena deploy/helm/palena/ \
  --set reranker.provider=kserve \
  --set reranker.endpoint=http://mxbai-rerank.kserve.svc:8080
```

---

## HTTP Endpoints

| Method | Path | Description |
|---|---|---|
| `GET`  | `/sse`      | MCP SSE transport (legacy event stream) |
| `POST` | `/mcp`      | MCP Streamable HTTP transport |
| `GET`  | `/health`   | Sidecar reachability probe |
| `GET`  | `/metrics`  | Prometheus metrics (when OTel metrics enabled) |

---

## Project Layout

```
palena-websearch-mcp/
├── cmd/palena/           # Binary entry point, config + server wiring
├── internal/
│   ├── config/           # YAML parsing, validation, env overrides
│   ├── search/           # SearXNG client, query expansion, dedup
│   ├── scraper/          # L0 readability · L1/L2 Playwright · stealth · proxy pool
│   ├── pii/              # Presidio client, policy modes, PII-free audit records
│   ├── reranker/         # Pluggable interface · KServe · FlashRank · RankLLM · no-op
│   ├── policy/           # Domain filter, robots.txt, per-domain rate limit
│   ├── output/           # Markdown formatting, provenance hash chain
│   ├── transport/        # MCP SSE + Streamable HTTP, tool handler
│   └── otel/             # Tracing + metrics setup
├── config/               # Default + annotated example YAML
├── deploy/               # Dockerfile, Compose (full + minimal), Helm chart
└── docs/                 # Per-subsystem deep dives
```

---

## Documentation

| Document | Topic |
|---|---|
| [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) | System design, request lifecycle, concurrency model |
| [`docs/SEARCH.md`](docs/SEARCH.md) | SearXNG integration, query expansion, engine routing |
| [`docs/SCRAPER.md`](docs/SCRAPER.md) | Tiered extraction, JS detection, stealth, proxy rotation |
| [`docs/PII.md`](docs/PII.md) | Presidio setup, audit / redact / block modes |
| [`docs/RERANKER.md`](docs/RERANKER.md) | Reranker interface, model options, API contracts |
| [`docs/MCP.md`](docs/MCP.md) | MCP transport, tool schema, response format |
| [`docs/CONFIG.md`](docs/CONFIG.md) | Full configuration reference |
| [`docs/PROVENANCE.md`](docs/PROVENANCE.md) | Hash chain, audit records, ClickHouse schema |

---

## Technology Stack

- **Core:** Go 1.26+, single static binary
- **Search:** [SearXNG](https://github.com/searxng/searxng) (self-hosted metasearch)
- **Extraction:** [`go-shiori/go-readability`](https://github.com/go-shiori/go-readability) (L0), [`playwright-community/playwright-go`](https://github.com/playwright-community/playwright-go) against Microsoft's official Playwright image (L1/L2)
- **PII:** Microsoft [Presidio](https://microsoft.github.io/presidio/) Analyzer + Anonymizer
- **Reranking:** Mixedbread `mxbai-rerank` via KServe · [FlashRank](https://github.com/PrithivirajDamodaran/FlashRank) ONNX/CPU · RankLLM against any inference endpoint
- **Observability:** [OpenTelemetry](https://opentelemetry.io/) traces + Prometheus-compatible metrics
- **Transport:** MCP SSE, MCP Streamable HTTP
- **Config:** YAML + environment variable overrides
- **Deploy:** Docker Compose (dev), Helm chart (Kubernetes / OpenShift)

---

## License

Copyright © 2026 bitkaio LLC.

Licensed under the [Apache License, Version 2.0](LICENSE). You may use, modify, and redistribute Palena under the terms of that license. For commercial support, custom reranker models, or production SLAs, contact bitkaio LLC.
