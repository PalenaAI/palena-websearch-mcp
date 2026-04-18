# Palena Documentation

**Enterprise-grade web search for AI — with compliance boundaries built in.**

Palena is a single-binary [MCP](https://modelcontextprotocol.io/) server that takes a query from an AI agent, searches the open web, scrapes the result pages, strips PII, reranks for relevance, and hands back LLM-ready markdown with a full audit trail. It is designed for fintech, healthtech, govtech, and other teams that cannot ship raw third-party HTML into a foundation-model prompt but still need their agents to reach the live internet.

This site is the reference documentation. If you are new, start with [Getting Started](getting-started.md) and [Concepts](concepts.md).

---

## By topic

### Start here

| Page | What's inside |
|---|---|
| [Getting Started](getting-started.md) | Bring up the full stack in five minutes and run your first query. |
| [Concepts](concepts.md) | The pipeline, sidecar model, and compliance-first philosophy. |
| [FAQ](faq.md) | Short answers to the questions most teams ask first. |

### Using the server

| Page | What's inside |
|---|---|
| [Tool Reference](tool-reference.md) | The `web_search` tool — parameters, response format, examples. |
| [Integrations](integrations.md) | Wiring Palena into LibreChat, Claude Desktop, Cursor, Windsurf, and custom MCP clients. |
| [Configuration](configuration.md) | The `palena.yaml` file, environment variable overrides, and common recipes. |

### The pipeline, explained

| Page | What's inside |
|---|---|
| [Scraping](scraping.md) | Tiered L0 → L1 → L2 extraction, content detection, stealth, and proxy rotation. |
| [PII & Compliance](pii-and-compliance.md) | PII modes (audit / redact / block), entity types, audit records, domain policy, robots.txt, rate limits. |
| [Reranking](reranking.md) | Picking between KServe, FlashRank, RankLLM, and no reranker. |
| [Provenance](provenance.md) | The three-stage SHA-256 hash chain and audit record schema. |

### Running it in production

| Page | What's inside |
|---|---|
| [Deployment](deployment.md) | Docker Compose and Helm, deployment profiles, sizing guidance, sidecar matrix. |
| [Observability](observability.md) | Health endpoint, Prometheus metrics, OpenTelemetry traces, structured logs, and troubleshooting. |

---

## Conventions

- **Sidecars** are the external processes Palena depends on — SearXNG, Presidio Analyzer, Presidio Anonymizer, Playwright, and a reranker. Each runs as its own container.
- **L0 / L1 / L2** refer to the three scraping tiers. L0 is plain HTTP + readability, L1 is a headless browser, L2 is a stealth browser with proxy rotation.
- **Modes** (`audit`, `redact`, `block`) describe what Palena does when Presidio detects PII in scraped content.
- **Graceful degradation** means that when an optional sidecar is unavailable, Palena skips that stage, notes it in the response metadata, and keeps serving.

---

## Support and licensing

Palena is published by [bitkaio LLC](https://github.com/PalenaAI) under the Apache License, Version 2.0. Commercial support, custom reranker models, and production SLAs are available — email `opensource@bitkaio.com` or open a discussion on the GitHub repository.
