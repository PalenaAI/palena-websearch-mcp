# Reranking

Search engines rank by engine-specific popularity, freshness, and click signals — not by semantic relevance to the exact query your agent asked. Palena re-scores the post-scrape document set with a dedicated reranker so the LLM sees the most query-relevant content first, not the most SEO-optimized.

This page explains how cross-encoder reranking works, the four providers Palena supports, and how to pick one.

> Configuration reference: [Configuration — reranker](configuration.md#reranker).

---

## Why reranking matters

A realistic example: on the query `"open source MCP servers 2026"`, SearXNG's aggregated top-three usually includes a handful of SEO-heavy vendor blog posts that contain the right keywords but little substantive content. A cross-encoder reranker — even a small CPU model — pushes the genuinely useful posts (project launches, technical comparisons) ahead of the noise.

A run with the reference FlashRank model against that query produces scores like:

| Result | Reranker score |
|---|---|
| Technical MCP server comparison blog post | 0.984 |
| New MCP server project launch | 0.973 |
| Vendor conference page mentioning MCP as a keyword | 0.002 |

The difference between the top-2 and the tail is almost three orders of magnitude. Without reranking, the vendor page might have been in the top five of SearXNG's consensus ranking.

---

## How cross-encoder rerankers work

Cross-encoders are **classification models**, not generative models. They do not produce text. The API contract is:

```
Input:  query (string) + documents (list of strings)
Output: one relevance score per document
```

No system prompt, no chat template, no tokenizer configuration beyond the model's own defaults. The reranker scores each `(query, document)` pair independently; Palena sorts by descending score and keeps the top K.

Because both the query and document go through the model together (as opposed to encoding them separately and comparing vectors), cross-encoders are substantially more accurate than bi-encoder embedding similarity for reranking tasks. They are slower per call, which is why they are used only after an initial search, not as the search mechanism itself.

---

## Providers

### FlashRank — CPU cross-encoder

Default for the reference Compose stack. FlashRank is a Python library that runs ONNX-optimized cross-encoder models on CPU with no GPU requirement. Palena wraps it in a small Flask sidecar.

| Attribute | Value |
|---|---|
| Model | `ms-marco-MiniLM-L-12-v2` (default), swappable |
| Hardware | CPU |
| Image size | ~200 MB |
| Latency for 10 docs | 150–500 ms |
| Quality | Good — adequate for most general queries |

**When to pick FlashRank:** you want reranker-quality results without standing up a GPU. Ideal for single-node Compose deployments, internal tools, and proof-of-concept stacks.

### KServe — GPU cross-encoder

For production deployments with Kubernetes + GPU infrastructure. KServe exposes any Hugging Face or custom model as an `InferenceService` over HTTP. Palena calls the `/v1/models/<model>:predict` endpoint.

Recommended models (all Apache 2.0):

| Model | Parameters | Context | Best for |
|---|---|---|---|
| `mixedbread-ai/mxbai-rerank-base-v2` | 0.5 B | 8 K (32 K with YaRN) | Best balance of speed and quality. |
| `mixedbread-ai/mxbai-rerank-large-v2` | 1.5 B | 8 K (32 K with YaRN) | Maximum accuracy. |
| `mixedbread-ai/mxbai-rerank-base-v1` | ~0.3 B | 512 | Lightweight BERT-based. |
| `mixedbread-ai/mxbai-rerank-large-v1` | ~0.6 B | 512 | Strong BERT-based. |

These are standalone models. They are fine-tuned from Qwen-2.5 (v2) or BERT/RoBERTa (v1), but they run independently — no dependency on an upstream Qwen, Llama, or Mistral deployment.

| Attribute | Value |
|---|---|
| Hardware | NVIDIA GPU (A10G, L4, A100, H100) |
| Latency for 10 docs | 40–120 ms |
| Quality | Excellent — state-of-the-art for reranking benchmarks |

**When to pick KServe:** you already have a KServe platform, you have a GPU budget, and query quality is a differentiator. A single A10G handles thousands of rerank calls per minute.

Example InferenceService:

```yaml
apiVersion: serving.kserve.io/v1beta1
kind: InferenceService
metadata:
  name: mxbai-rerank
  namespace: palena
spec:
  predictor:
    model:
      modelFormat:
        name: huggingface
      resources:
        limits:
          nvidia.com/gpu: "1"
      storageUri: "hf://mixedbread-ai/mxbai-rerank-large-v2"
```

Then point Palena at it:

```yaml
reranker:
  provider: "kserve"
  endpoint: "http://mxbai-rerank.palena.svc.cluster.local:8080"
  model: "mixedbread-ai/mxbai-rerank-large-v2"
```

### RankLLM — LLM-as-reranker

Instead of a dedicated reranker model, Palena prompts any general-purpose LLM to score documents. Useful when a team already has Qwen, Mistral, or Llama running on KServe (or any OpenAI-compatible endpoint) and wants to avoid adding another model.

The prompt sent to the LLM looks roughly like:

```
Given the query: "{query}"

Score the relevance of each document on a scale of 0.0 to 1.0.
Respond ONLY with a JSON array of scores in the same order as the documents.

Document 1: {content_1}
Document 2: {content_2}
...

Response format: [0.95, 0.23, 0.87, ...]
```

| Attribute | Value |
|---|---|
| Hardware | Whatever the underlying LLM uses |
| Latency for 10 docs | 0.5–2 s+ |
| Quality | Varies by LLM — typically between FlashRank and dedicated cross-encoders |
| Cost | Full LLM inference per call — more expensive than a cross-encoder |

**When to pick RankLLM:** you cannot deploy another model (regulated environment, locked-down infra), you already have an LLM endpoint, and the additional latency / token cost is acceptable.

RankLLM handles malformed LLM output gracefully — if the response cannot be parsed as a JSON array, the fallback is to treat the SearXNG order as authoritative and log a warning.

### None — passthrough

The documents are returned in SearXNG's aggregated order with a synthetic descending score. No rerank API call is made.

| Attribute | Value |
|---|---|
| Latency | 0 ms |
| Quality | Whatever SearXNG returned |

**When to pick None:** you are running minimal footprint, you trust the upstream engines enough for your use case, or you want to eliminate another moving part while debugging the rest of the pipeline.

---

## Picking a provider

```
Do you have a Kubernetes cluster with spare GPU?
├── Yes → KServe with mxbai-rerank-large-v2.
└── No, but you have CPU to spare
    ├── Yes → FlashRank.
    └── No
        ├── You already run an LLM somewhere → RankLLM.
        └── Minimal deployment / debugging → None.
```

Quality roughly orders as:

```
KServe (mxbai-rerank-large-v2)  >  KServe (mxbai-rerank-base-v2)
                                >  RankLLM (GPT-4 class)
                                >  FlashRank (ms-marco-MiniLM-L-12-v2)
                                >  RankLLM (7-13B models)
                                >  None
```

Latency orders opposite:

```
None (0ms)
  < KServe (40–120ms)
  < FlashRank (150–500ms)
  < RankLLM (500–2000ms)
```

---

## topK behavior

The tool input's `maxResults` parameter (1–20) controls how many reranked results are returned to the caller. It is clamped against `reranker.topK` in config — whichever is smaller wins.

Palena always sends the full document pool (post-scrape, post-PII) to the reranker, so the reranker has a real selection to choose from. If SearXNG returned 10 documents and the caller requested `maxResults: 3`, the reranker scores all 10 and returns the top 3.

---

## Graceful degradation

If the configured reranker is unreachable:

- Documents are returned in SearXNG's original order.
- `meta.reranker_used` reports `none`.
- A `WARN` log is emitted.
- The pipeline succeeds — no error is returned to the caller.

This keeps search working through transient reranker outages. If your service tier depends on reranker quality, monitor `palena_reranker_available{provider="..."}` in Prometheus.

---

## Swapping the reranker model

### FlashRank

Change the model inside the sidecar by overriding the `FLASHRANK_MODEL` environment variable:

```yaml
environment:
  FLASHRANK_MODEL: "ms-marco-MiniLM-L-6-v2"     # smaller, faster
```

Available models: `ms-marco-TinyBERT-L-2`, `ms-marco-MiniLM-L-6-v2`, `ms-marco-MiniLM-L-12-v2` (default), `rank-T5-flan`, `ms-marco-MultiBERT-L-12`, and more — see the [FlashRank model list](https://github.com/PrithivirajDamodaran/FlashRank#supported-models).

### KServe

Point the InferenceService at a different `storageUri`. Palena only needs `reranker.model` updated to match:

```yaml
reranker:
  model: "mixedbread-ai/mxbai-rerank-base-v2"
```

### RankLLM

Change the LLM endpoint. Palena does not care which LLM is behind it as long as the endpoint speaks a supported protocol. Models that are better at instruction-following produce cleaner rerank output.

---

## What's next

- [Tool Reference — response shape](tool-reference.md#meta-object) — where the rerank score appears in `meta`.
- [Deployment — GPU sizing](deployment.md#gpu-sizing) — hardware recommendations for KServe.
- [Observability — reranker metrics](observability.md#reranker-metrics) — latency and error budgets.
