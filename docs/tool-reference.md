# Tool Reference

Palena exposes a single MCP tool: `web_search`. This page is the full reference — inputs, outputs, examples, and error handling.

> For higher-level background, see [Concepts](concepts.md). For client wiring, see [Integrations](integrations.md).

---

## Tool schema

```json
{
  "name": "web_search",
  "description": "Search the web and retrieve relevant content from result pages. Returns scraped and optionally reranked content with citations and source URLs. Content is checked for PII according to deployment policy.",
  "inputSchema": {
    "type": "object",
    "properties": {
      "query":      { "type": "string",  "description": "The search query" },
      "category":   { "type": "string",  "description": "general | news | code | science (default: general)" },
      "language":   { "type": "string",  "description": "ISO 639-1 code — en, de, fr, ja, … (default: en)" },
      "timeRange":  { "type": "string",  "description": "day | week | month | year" },
      "maxResults": { "type": "integer", "description": "1–20 (default: 5)" }
    },
    "required": ["query"]
  }
}
```

---

## Input parameters

### `query` (required)

The free-text query. Passed to SearXNG verbatim. Operators that SearXNG forwards to upstream engines (`site:`, `filetype:`, quoted phrases, `-exclusion`, etc.) work as expected; the exact subset depends on which engines the selected `category` routes to.

### `category` (optional)

Drives which upstream search engines are consulted. Defaults to `general`. Invalid values fall back to `general` at runtime.

| Category | Default engine route | When to use |
|---|---|---|
| `general` | `google`, `duckduckgo`, `brave` | Broad web queries, how-tos, explanations. |
| `news` | `google news`, `duckduckgo`, `bing news` | Current events, press releases, launches. |
| `code` | `github`, `stackoverflow`, `duckduckgo` | API references, error messages, library docs. |
| `science` | `google scholar`, `duckduckgo`, `wikipedia` | Research papers, definitions, citations. |

Engine routes are configurable per deployment in `search.engineRoutes` — see [Configuration](configuration.md#search).

### `language` (optional)

ISO 639-1 code, e.g. `en`, `de`, `fr`, `ja`. Propagated to SearXNG as the `language` parameter. Most upstream engines honor this as a soft preference, not a hard filter. Default is `en` unless the deployment overrides `search.defaultLanguage`.

### `timeRange` (optional)

Limits results to a recency window: `day`, `week`, `month`, or `year`. Propagated to SearXNG as `time_range`. Honored by engines that support freshness filters (news engines, Google general). Leave unset for no recency filter.

### `maxResults` (optional)

How many results to return **after** reranking. Range `1`–`20`, default `5`. Values outside the range are clamped. Palena always fetches at least `maxResults` candidates from SearXNG (often more, configured in `search.maxResults`) so that the reranker has a real pool to re-score.

---

## Response shape

The MCP `tool_result` is a two-part payload:

- A **markdown block** suitable for embedding directly in an LLM prompt.
- A **`meta` object** with structured metadata for downstream processing, audit, and UI rendering.

### Markdown block

```
# Search Results for: "latest AI hot topics 2026"

## 1. OpenClaw Exposes the Real Cybersecurity Risks of Agentic AI
**Source:** https://example.com/openclaw-agentic-ai
**Relevance:** 0.984

OpenClaw is a new open-source framework that demonstrates how agentic AI systems
can be exploited by adversarial prompts embedded in tool outputs. The researchers
showed that a single poisoned web page could hijack …

---

## 2. Comprehensive AI Conference List for 2026
**Source:** https://example.com/ai-conferences-2026
**Relevance:** 0.973

…

---

**Sources:**
[1] https://example.com/openclaw-agentic-ai
[2] https://example.com/ai-conferences-2026
[3] https://example.com/oracle-ai-world

**Metadata:**
- Results returned: 3
- PII mode: audit
- Reranker: flashrank
- Search engines: google news, duckduckgo, bing news
```

### `meta` object

```json
{
  "query": "latest AI hot topics 2026",
  "result_count": 3,
  "search_engines": ["google news", "duckduckgo", "bing news"],
  "pii_mode": "audit",
  "pii_checked": true,
  "reranker_used": "flashrank",
  "total_duration_ms": 4414,
  "results": [
    {
      "url": "https://example.com/openclaw-agentic-ai",
      "title": "OpenClaw Exposes the Real Cybersecurity Risks of Agentic AI",
      "score": 0.984,
      "scraper_level": 0,
      "content_hash": "c3b4d690…",
      "pii_entities": 0,
      "pii_action": "pass"
    },
    {
      "url": "https://example.com/ai-conferences-2026",
      "title": "Comprehensive AI Conference List for 2026",
      "score": 0.973,
      "scraper_level": 0,
      "content_hash": "7af1205e…",
      "pii_entities": 2,
      "pii_action": "pass"
    }
  ]
}
```

#### Top-level fields

| Field | Type | Description |
|---|---|---|
| `query` | string | Query as the tool received it (after normalization). |
| `result_count` | int | Number of results in `results[]`. |
| `search_engines` | []string | Engines actually queried for this request (after category routing). |
| `pii_mode` | string | `audit`, `redact`, `block`, or `disabled`. |
| `pii_checked` | bool | `true` only if Presidio was reachable. `false` means PII was not examined. |
| `reranker_used` | string | `kserve`, `flashrank`, `rankllm`, or `none`. |
| `total_duration_ms` | int | Wall-clock time for the entire pipeline. |

#### Per-result fields

| Field | Type | Description |
|---|---|---|
| `url` | string | Final URL after any redirects. |
| `title` | string | Page title as extracted by readability. |
| `score` | float | Reranker score, higher = more relevant. If `reranker_used` is `none`, scores are synthetic descending. |
| `scraper_level` | int | `0` (HTTP + readability), `1` (Playwright headless), or `2` (Playwright stealth). |
| `content_hash` | string | SHA-256 of the content as delivered (after PII processing). See [Provenance](provenance.md). |
| `pii_entities` | int | Number of PII entities detected. `0` in `audit` mode does not imply nothing was changed — it means Presidio found nothing above the confidence threshold. |
| `pii_action` | string | `pass`, `redacted`, or `blocked`. |

---

## Examples

### General search, default parameters

```json
{
  "name": "web_search",
  "arguments": { "query": "kubernetes RBAC best practices" }
}
```

### News category with recency filter

```json
{
  "name": "web_search",
  "arguments": {
    "query": "MCP protocol specification update",
    "category": "news",
    "timeRange": "week",
    "maxResults": 5
  }
}
```

### Code category for API docs

```json
{
  "name": "web_search",
  "arguments": {
    "query": "stripe webhook signature verification go",
    "category": "code",
    "maxResults": 3
  }
}
```

### Non-English query

```json
{
  "name": "web_search",
  "arguments": {
    "query": "steuererklärung freiberufler 2026",
    "language": "de",
    "category": "general"
  }
}
```

---

## Error handling

Tool errors are returned as MCP `tool_result` with `isError: true`. The text content contains a short, human-readable explanation suitable for forwarding to the end user.

```json
{
  "content": [
    { "type": "text", "text": "Search failed: SearXNG returned no results for query 'xyzzy123'. Try a different query." }
  ],
  "isError": true
}
```

| Condition | Behavior |
|---|---|
| SearXNG unreachable | Hard error — the one required sidecar. |
| Zero results after policy filtering | Error with a message that a domain policy may be excluding everything. |
| All candidate URLs fail to scrape | Error with a summary of per-URL failure modes. |
| Some URLs fail, some succeed | Success. Failed URLs do not appear in `results[]`; `result_count` reflects what was actually returned. |
| Presidio unavailable | Success with degraded metadata: `pii_checked: false`, `pii_mode` still reports configured mode. |
| Reranker unavailable | Success with degraded metadata: `reranker_used: "none"`, scores are synthetic. |
| Playwright unavailable | Success. L0-eligible URLs return content; others are dropped from `results[]` and the total duration is shorter. |

---

## Performance characteristics

Typical latencies from the reference Compose stack on a MacBook Pro with the `latest AI hot topics 2026` query:

| Stage | p50 | p95 | Notes |
|---|---|---|---|
| Search (SearXNG) | 400 ms | 1.2 s | Dominated by the slowest upstream engine. |
| Scrape L0 | 200 ms per URL | 700 ms | Parallelized across `scraper.maxConcurrency` workers. |
| Scrape L1 | 1.5 s per URL | 4 s | Browser cold-start amortized via a shared page pool. |
| Scrape L2 | 2.5 s per URL | 8 s | Adds stealth + proxy latency. |
| PII (Presidio) | 80 ms per document | 300 ms | Scales linearly with document length. |
| Rerank (FlashRank) | 150 ms for 10 docs | 500 ms | Batched — one call for the whole result set. |
| Rerank (KServe mxbai-rerank-large-v2 on A10G) | 40 ms for 10 docs | 120 ms | GPU throughput. |

A typical end-to-end `web_search` call on the Compose stack is **2–5 seconds** for L0-heavy queries and **5–12 seconds** when L1 is involved.

---

## What's next

- [Integrations](integrations.md) — connect this tool to LibreChat, Claude Desktop, Cursor, or a custom MCP client.
- [Configuration](configuration.md) — tune engine routing, rerank provider, PII mode, and more.
- [Observability](observability.md) — trace a single call across every stage.
