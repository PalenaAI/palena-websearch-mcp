# FAQ

Short answers to the questions teams ask first. For detail, follow the links.

---

## What exactly is Palena?

An MCP server that exposes one tool — `web_search` — which runs a six-stage pipeline: search (SearXNG) → policy gates → tiered scraping → PII detection (Presidio) → rerank → markdown formatting with a full audit trail. It is written in Go and distributed as a small static binary plus a set of sidecars.

See [Concepts](concepts.md).

---

## How is this different from "just give the LLM a search API"?

Three things a bare search API does not do:

1. **It strips PII** before any content reaches the LLM prompt.
2. **It produces a verifiable audit trail** — SHA-256 hashes at three stages, structured audit records, optional ClickHouse export.
3. **It enforces policy pre-scrape** — domain allowlists, robots.txt, per-domain rate limits.

If your use case does not need any of the three, Palena is overkill; a plain search tool is simpler.

---

## Is Palena an AI product?

No. Palena does not generate text and does not include an LLM. It is a deterministic data pipeline that an LLM-driven agent calls. The reranker is a classifier (cross-encoder), not a generative model — and the RankLLM provider, which does use an LLM, calls an LLM you already run elsewhere.

---

## What licenses apply?

Palena itself is Apache 2.0. All bundled images are Apache 2.0 or compatible:

| Component | License |
|---|---|
| Palena | Apache 2.0 |
| SearXNG | AGPL-3.0 |
| Presidio Analyzer / Anonymizer | MIT |
| Playwright | Apache 2.0 |
| FlashRank | MIT |
| Recommended reranker models (mxbai-rerank-*) | Apache 2.0 |

SearXNG is AGPL-3.0 but runs as an unmodified upstream container — your integration with it via HTTP does not trigger AGPL propagation to your own code.

See the [LICENSE](../LICENSE) file for Palena's full terms.

---

## Do I need a GPU?

No. The full standard profile runs on CPU with FlashRank as the reranker. GPU only enters the picture if you deploy the enterprise profile with a KServe-hosted cross-encoder for best-quality reranking.

See [Reranking — picking a provider](reranking.md#picking-a-provider).

---

## How much does it cost to run?

Cost depends almost entirely on scrape volume and rerank choice:

- **Minimal** (Palena + SearXNG): < $20/mo on a single small VM.
- **Standard** (full Compose): ~$50–$150/mo on a medium VM or a small Kubernetes namespace — dominated by Playwright memory.
- **Enterprise** (GPU reranker): GPU lease is the dominant cost — e.g., ~$300–$700/mo for an L4 on a hyperscaler.

Upstream search (Google, Bing, etc.) is free — SearXNG scrapes the public results pages and incurs no per-query fee.

---

## Does it work with LibreChat / Claude Desktop / Cursor / Windsurf?

Yes — it is MCP-compatible. See [Integrations](integrations.md) for client-specific wiring.

---

## What MCP transport should I use?

If you can pick: **Streamable HTTP** at `POST /mcp`. It is simpler to proxy and easier to debug. Fall back to SSE at `GET /sse` for clients that only support the legacy event-stream transport (Claude Desktop today).

---

## Can I restrict which sites Palena is allowed to scrape?

Yes. Two mechanisms:

- `policy.domains.mode: allowlist` with an explicit list of domain suffixes — only those domains (and their subdomains) are scraped.
- `policy.robots.enabled: true` — robots.txt is fetched per domain and enforced before scraping.

Both are on by default except the allowlist is empty. See [PII & Compliance — policy gates](pii-and-compliance.md#policy-gates).

---

## How does Palena handle authenticated or paywalled content?

It does not. Palena has no cookie store, no credential manager, and no login flow. It is designed for the open web. If you need to reach authenticated sources, place a proxy in front that handles auth and hands Palena pre-authenticated URLs — but understand that the provenance trail ends at your proxy, not at the upstream origin.

---

## Can Palena log the actual PII it detects?

**No — and this is by design.** Audit records contain only entity type, count, and confidence score. The actual PII text never appears in logs or in the ClickHouse audit table. This is a hard invariant enforced at the point of emission so that the audit trail itself does not become a compliance liability.

---

## How do I switch from `audit` to `redact` mode?

Change `pii.mode: redact` in `palena.yaml` (or set `PALENA_PII_MODE=redact`) and restart. No code change required. See [PII & Compliance — the three modes](pii-and-compliance.md#the-three-modes).

---

## What happens if SearXNG goes down?

Palena cannot serve search requests — SearXNG is the one hard dependency. `/health` reports `searxng: unavailable`, and tool calls fail with a clear error.

---

## What happens if Presidio goes down?

Palena continues to serve. The PII stage is skipped and `meta.pii_checked: false` is set on the response. A `WARN` is logged; the `palena_pii_checked_total{checked="false"}` counter increments. If your deployment requires PII enforcement for correctness, alert on this — see [Observability — recommended alerts](observability.md#recommended-alerts).

---

## What happens if Playwright goes down?

L0 still works. URLs that would need L1 or L2 are dropped from the result set. If most of your target pages need JavaScript, this will noticeably reduce the number of results.

---

## What happens if the reranker goes down?

Documents are returned in SearXNG's aggregated order. `meta.reranker_used: "none"` in the response metadata.

---

## How is Palena different from tools like Perplexity, Exa, Tavily, or Brave Search API?

Those are hosted services. Palena is self-hosted and open source. The distinction that usually matters for regulated customers:

- **Network boundary** — content never leaves your network.
- **PII redaction before the LLM** — hosted services return content as-is; you add PII tooling downstream.
- **Provenance hashes** — hosted services return results but not integrity proofs.
- **Pluggable reranker** — bring your own model (including GPU-hosted models inside your tenancy).

For unregulated use cases where hosted is acceptable, those services can be simpler.

---

## Can I run this on-prem / in a private Kubernetes cluster?

Yes. The full stack runs without reaching external services except SearXNG itself hitting upstream search engines. OpenShift works with the bundled Helm chart. Air-gapped deployments are possible by mirroring the container images to an internal registry.

---

## Can I use a different search backend instead of SearXNG?

Not out of the box. The search client is SearXNG-specific. That said, the `internal/search` package is cleanly separated and a custom backend would be roughly a day's work.

---

## Can I use a custom PII detector instead of Presidio?

Presidio is called via its public REST API. Any system exposing a compatible `/analyze` endpoint (entity detection) and `/anonymize` endpoint (masking) will work. Reshape the response to match Presidio's schema and Palena will talk to it without changes.

---

## How do I contribute?

See [CONTRIBUTING.md](../CONTRIBUTING.md) in the repository root.

---

## Where do I report bugs or ask questions?

Open an issue or a discussion at the [bitkaio/palena-websearch-mcp](https://github.com/bitkaio/palena-websearch-mcp) GitHub repository. For commercial support, custom models, or production SLAs, email `hello@bitkaio.com`.
