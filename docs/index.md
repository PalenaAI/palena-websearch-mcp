---
layout: home

title: Palena
titleTemplate: Enterprise web search for AI — with compliance boundaries built in

hero:
  name: Palena
  text: Enterprise web search for AI.
  tagline: A single-binary MCP server that searches the open web, strips PII, screens prompt-injection payloads, and hands back LLM-ready markdown with a full audit trail.
  image:
    src: /palena-icon.png
    alt: Palena
  actions:
    - theme: brand
      text: Get Started
      link: /getting-started
    - theme: alt
      text: Concepts
      link: /concepts
    - theme: alt
      text: GitHub
      link: https://github.com/PalenaAI/palena-websearch-mcp

features:
  - icon: 🔎
    title: Metasearch, not scraping-as-a-service
    details: Queries a self-hosted SearXNG instance you control. No third-party API keys, no rate-limited search vendors, no unexpected data exfiltration.
    link: /concepts
    linkText: How the pipeline works

  - icon: 🕸️
    title: Tiered content extraction
    details: Plain HTTP + readability first, headless Chromium only when needed, stealth browser with proxy rotation as the last resort. Pay for complexity only when content demands it.
    link: /scraping
    linkText: L0 / L1 / L2 extraction

  - icon: 🛡️
    title: PII redaction via Presidio
    details: Microsoft Presidio analyzes every scraped document. Choose between audit, redact, and block modes — enforced per-request, logged per-finding.
    link: /pii-and-compliance
    linkText: PII & compliance

  - icon: 🧱
    title: Prompt-injection defense
    details: A DeBERTa-v3 classifier sidecar screens scraped content before it reaches the model. Annotate suspicious chunks, block them outright, or just audit — your call.
    link: /prompt-injection
    linkText: Injection defense

  - icon: 🎯
    title: Pluggable reranker
    details: Swap between a KServe cross-encoder (mxbai-rerank), a lightweight FlashRank ONNX sidecar, an LLM-as-reranker, or no reranker at all. Config-driven, zero code changes.
    link: /reranking
    linkText: Reranking options

  - icon: 🧾
    title: Full provenance trail
    details: Every scrape emits a three-stage SHA-256 hash chain. Every PII and injection finding is logged. Auditors get deterministic replay, not hand-wavy summaries.
    link: /provenance
    linkText: Provenance details

  - icon: 📡
    title: MCP-native, SSE + Streamable HTTP
    details: Drops into Claude Desktop, Cursor, Windsurf, LibreChat, or any MCP client. Also exposes a standalone REST endpoint if you want direct access.
    link: /integrations
    linkText: Client integrations

  - icon: 📊
    title: OpenTelemetry out of the box
    details: Structured logs, Prometheus metrics, and OTLP traces cover every stage. Export to ClickHouse, Jaeger, Grafana — whatever your SRE team already runs.
    link: /observability
    linkText: Observability

  - icon: 🐳
    title: Docker Compose to Kubernetes
    details: A single compose file to run the whole stack locally. A Helm chart for OpenShift and vanilla Kubernetes when you're ready to put it in front of real traffic.
    link: /deployment
    linkText: Deployment guide
---

<div class="vp-doc" style="max-width: 960px; margin: 4rem auto 0; padding: 0 24px;">

## Why Palena exists

Foundation models cannot safely ingest raw third-party HTML. It carries PII, prompt-injection payloads, adversarial instructions, tracking artifacts, and noise that blows up token budgets.

Palena is the boundary layer — the name means _boundary_ or _limit_ in Hawaiian. It sits between your agent and the open web, enforces the policies your compliance team actually cares about, and returns clean, hashed, audited markdown your model can trust.

## What you get

- **A single MCP tool** (`web_search`) that orchestrates six stages: search → scrape → PII detection → prompt-injection screening → rerank → markdown formatting.
- **Sidecar architecture** — SearXNG, Presidio, Playwright, the injection classifier, and the reranker are all separate containers. Swap them, scale them, replace them independently.
- **Config-driven behavior** — PII mode, injection mode, reranker backend, scraper tiers, domain allow/blocklists, rate limits — all declared in `palena.yaml`, not burned into code.
- **Compliance-first defaults** — robots.txt enforcement, per-domain rate limits, content hash chains, and audit records that survive a regulator's request.

## Who it's for

Fintech, healthtech, govtech, and any team whose legal team has concerns about raw web content reaching an LLM prompt. If you've been told "we can't ship unfiltered HTML into a model," Palena is the filter.

Start with [Getting Started](/getting-started) to bring up the stack, then read [Concepts](/concepts) for the full pipeline picture.

</div>
