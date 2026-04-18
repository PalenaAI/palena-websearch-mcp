# Concepts

Palena turns a query from an AI agent into a cited, PII-safe, reranked answer that is cheap to feed into an LLM prompt and safe to show to a compliance officer. This page explains the moving parts.

---

## The six-stage pipeline

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

Every stage is an OpenTelemetry span. Every document gets three SHA-256 hashes (raw HTML → extracted markdown → final delivered content). A missing sidecar is survivable — the stage is skipped, the metadata records it, and the response keeps flowing.

### 1. Search

Palena queries [SearXNG](https://github.com/searxng/searxng), a self-hosted metasearch engine that aggregates Google, DuckDuckGo, Brave, Bing, GitHub, Stack Overflow, Google Scholar, Wikipedia, and dozens more. Results are deduplicated by normalized URL, merged across engines, and ranked roughly by source consensus. Which engines are consulted is driven by the `category` argument on the tool call — `general`, `news`, `code`, or `science`. See [Tool Reference](tool-reference.md#categories).

### 2. Policy

Before a single page is fetched, every candidate URL is filtered through three policy gates:

- **Domain filter** — an allowlist or blocklist of suffixes. `docs.github.com` matches `github.com`.
- **robots.txt** — fetched once per domain, cached for the configured TTL, checked with the `Palena` user agent.
- **Per-domain rate limit** — a token bucket with a per-minute refill; prevents Palena from hammering a single host across concurrent calls.

A URL dropped here never triggers a scrape, so no browser cycles or proxy credits are spent on it.

### 3. Scrape

Palena tries the cheapest extraction first and escalates only when content detection says it is necessary:

- **L0** — plain HTTP GET + Mozilla Readability. Fastest, no browser. Works for most news sites, blog posts, documentation.
- **L1** — headless Chromium via a Playwright sidecar. Used when L0 returns thin HTML (empty `#root`, heavy script tags, low text-to-markup ratio).
- **L2** — Playwright with stealth patches and optional proxy rotation. Used when L1 gets a Cloudflare challenge, CAPTCHA, or 403.

If Playwright is not running, L1 and L2 are disabled and only L0-eligible URLs return content. See [Scraping](scraping.md) for the full escalation logic.

### 4. PII

The extracted markdown is sent to Microsoft [Presidio](https://microsoft.github.io/presidio/) Analyzer, which returns a list of detected entities with confidence scores: `PERSON`, `EMAIL_ADDRESS`, `PHONE_NUMBER`, `CREDIT_CARD`, `IBAN_CODE`, `US_SSN`, `MEDICAL_LICENSE`, `LOCATION`, `IP_ADDRESS`, and others. What happens next depends on the configured mode:

- **`audit`** — log the findings, pass the content through unchanged. Default.
- **`redact`** — call Presidio Anonymizer with per-entity rules (replace `PERSON` with `<PERSON>`, mask `EMAIL_ADDRESS`, etc.), return the anonymized content.
- **`block`** — drop the page entirely if the PII density exceeds a configurable threshold.

An audit record is emitted in every mode — even `audit`. The record contains entity types, counts, and confidence scores; it never contains the actual PII text. See [PII & Compliance](pii-and-compliance.md).

### 5. Rerank

SearXNG's ranking reflects engine consensus, not semantic relevance to the specific query. Palena re-scores documents with a cross-encoder:

- **FlashRank** — an ONNX cross-encoder that runs on CPU in a small Python sidecar. Default for the full Compose profile.
- **KServe** — a GPU-served cross-encoder model (for example `mixedbread-ai/mxbai-rerank-large-v2`). For organizations with KServe infrastructure.
- **RankLLM** — any LLM behind any inference endpoint, prompted to score each document. Useful when a team already runs Qwen/Mistral/Llama.
- **None** — skip reranking; return SearXNG order with a synthetic descending score.

Only the top K documents survive to the final response. See [Reranking](reranking.md).

### 6. Format

The final stage builds the MCP `tool_result`:

- Formatted markdown with numbered results, per-result relevance score, and inline citations.
- A `meta` JSON block with the query, engines hit, reranker, PII mode, total duration, and per-result metadata (URL, score, scraper level, content hash, PII action).
- Optional chunking by token count for downstream ingestion.

The content hash in `meta` is the **final** hash — what the LLM actually received after any PII redaction. See [Provenance](provenance.md).

---

## The sidecar pattern

Palena itself is a single static Go binary. Every external capability runs in its own container:

| Sidecar | Required | Purpose |
|---|---|---|
| SearXNG | Yes | Metasearch aggregation. |
| Presidio Analyzer | No | PII entity detection. |
| Presidio Anonymizer | No | PII masking / replacement. Only used in `redact` / `block` modes. |
| Playwright | No | Headless browser for JS-rendered pages. |
| FlashRank | No | CPU cross-encoder reranker. |
| KServe InferenceService | No | GPU cross-encoder reranker. |

Palena talks to every sidecar over HTTP or WebSocket — there is no shared memory, no FFI, no plugin loading. You can swap a sidecar for your own implementation as long as it honors the same API contract.

### Graceful degradation

Every optional sidecar is gated by a startup health check. If a sidecar is unreachable, Palena logs a warning, disables the relevant stage, and continues to serve. The `meta` block of each tool response records the skipped stage so that agents (and operators) can tell the difference between "content is clean" and "we did not check."

| Missing sidecar | Behavior |
|---|---|
| SearXNG | Server fails to start — this is the one hard dependency. |
| Presidio | PII stage skipped. `meta.pii_checked` reports `false`. |
| Playwright | L1 / L2 disabled. URLs needing JS extraction are reported as failed. |
| Reranker | Results returned in SearXNG order. `meta.reranker_used` reports `none`. |

---

## Compliance-first design

Palena's value proposition is not "faster web search for AI." It is "web search for AI that your compliance team will actually approve." The design reflects that:

- **PII is scrubbed before the LLM ever sees the content**, not after. Once PII enters a prompt, it is in training logs, caches, and anyone else's retrospective RAG index.
- **Every document is hashed three times** so an auditor can prove what was fetched, what was extracted, and what the LLM actually consumed. If the raw and extracted hashes disagree, readability changed the content. If the extracted and final hashes disagree, PII redaction changed it.
- **Domain policy and robots.txt are enforced before scraping** — not after — so Palena cannot leak, even in buffers, content from a site it is not allowed to fetch.
- **The reranker is pluggable** so that regulated customers who cannot send content to external APIs can bring their own model, their own inference infrastructure, and keep every hop inside their network boundary.
- **Every stage emits an OpenTelemetry span** so that a SIEM or audit log can reconstruct the full pipeline for any request.

---

## MCP transport, at a glance

Palena speaks MCP over two HTTP transports on the same port:

- **Streamable HTTP** at `POST /mcp` — the newer, bidirectional transport. Session state is tracked via the `Mcp-Session-Id` response header.
- **SSE (Server-Sent Events)** at `GET /sse` + `POST /messages` — the legacy event-stream transport.

Both expose the same single tool: `web_search`. See [Tool Reference](tool-reference.md) for the input schema and response format, and [Integrations](integrations.md) for client-side wiring.

---

## What Palena does not do

- **It is not an LLM.** No content generation happens inside Palena. The only thing it returns is facts fetched from the web, cleaned, and scored.
- **It is not a vector database.** Rerank scores are computed per request; nothing is persisted across calls except optional provenance records.
- **It is not an agent framework.** It exposes one tool and expects the caller (LibreChat, Claude Desktop, your own agent) to decide when to call it.
- **It does not scrape authenticated content.** No cookie store, no credential manager, no session persistence. Palena is for the open web.
