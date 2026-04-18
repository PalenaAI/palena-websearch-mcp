# Prompt-Injection Defense

Palena ships with an optional prompt-injection classifier sidecar that screens every scraped document **before** it reaches the reranker or the downstream LLM. This page covers the threat model, the default model, the three operational modes, and how to swap in your own fine-tuned classifier without touching Palena's source.

For PII handling, see [PII & Compliance](pii-and-compliance.md). For the broader compliance picture, see [Provenance](provenance.md).

---

## Threat model: indirect prompt injection

Palena's primary risk is **indirect prompt injection** — malicious instructions hidden inside a third-party web page that, when scraped and passed to a model, attempt to override the agent's system prompt, exfiltrate data, or trigger unwanted tool calls. Direct prompt injection (the user typing `ignore previous instructions` themselves) is out of scope; that is the host application's concern.

Typical indirect-injection patterns the classifier is trained to recognize:

- Imperative phrasing addressed to "the AI", "the assistant", "the model".
- Role-reset markers such as `<|system|>`, `### System:`, or `--- new instructions ---`.
- Instructions to disregard, forget, or replace prior context.
- Hidden instructions embedded in HTML attributes, comments, or invisible CSS that survived scraping.

The classifier scores each chunk independently, so a single malicious paragraph hidden in 10 KB of legitimate content is still caught.

---

## Default model: deepset/deberta-v3-base-injection

The default sidecar serves [`deepset/deberta-v3-base-injection`](https://huggingface.co/deepset/deberta-v3-base-injection) — a fine-tune of `microsoft/deberta-v3-base` on a corpus of injection / legit text pairs. It outputs a binary classification with labels `INJECTION` and `LEGIT`.

The model is served by [Hugging Face Text Embeddings Inference (TEI)](https://github.com/huggingface/text-embeddings-inference), a single-binary Rust server that auto-detects the model task and exposes:

```text
POST /predict
Content-Type: application/json
Body: {"inputs": ["chunk 1", "chunk 2", ...]}

200 OK
[
  [{"label":"INJECTION","score":0.998},{"label":"LEGIT","score":0.002}],
  [{"label":"INJECTION","score":0.011},{"label":"LEGIT","score":0.989}]
]
```

TEI handles batching, tokenization, and truncation. The sidecar runs on CPU by default (~200 MB RAM, ~30 ms / chunk on a modern x86 core); for high-throughput deployments switch to the GPU image.

---

## The three modes

Pick exactly one mode per deployment.

### `audit`

- Every chunk is scored.
- Findings are logged.
- Content passes through unmodified.
- `meta.injection_checked: true`, `meta.results[*].injection_action: "pass"`.

**Use case:** the first 4–6 weeks of a rollout. Lets the security team measure base rates and false-positive risk on real scraped content before flipping on enforcement.

### `annotate`

- Every chunk is scored.
- Chunks with `score > scoreThreshold` are wrapped in `<untrusted-content>` markers (configurable).
- The annotated document is what the reranker and LLM see.
- `meta.results[*].injection_action: "annotated"`.

**Use case:** balanced default once your downstream LLM is prompt-engineered to treat `<untrusted-content>` as a trust boundary. Information from suspicious chunks is preserved (the LLM still sees it for context) but flagged so it should not be acted on as instructions.

### `block`

- Every chunk is scored.
- If **any** chunk exceeds the threshold, the document is dropped entirely.
- Dropped documents do not appear in `results[]`; a note is added to the response metadata.

**Use case:** strict environments where any suspected injection is treated as a poisoned source. Pairs naturally with PII `block` mode.

---

## Configuration

```yaml
injection:
  enabled: true
  mode: "annotate"
  predictURL: "http://injection-guard:8080"
  model: "deepset/deberta-v3-base-injection"
  injectionLabel: "INJECTION"
  scoreThreshold: 0.85
  maxChunkChars: 1200
  annotateOpen: "<untrusted-content reason=\"prompt-injection-suspected\">\n"
  annotateClose: "\n</untrusted-content>"
  timeout: 5s
```

Every key has a `PALENA_INJECTION_*` environment-variable override. See [Configuration — injection](configuration.md#injection) for the full list.

### Tuning `scoreThreshold`

The default of `0.85` errs on the side of fewer false positives. Lower thresholds (`0.6`–`0.75`) catch more borderline cases but also flag legitimate content that happens to use imperative language about AI ("This guide tells your assistant to..."). Walk it down gradually after a week of `audit` data.

### Chunking

Documents are split on blank lines first, then on hard newlines, then on character boundaries as a last resort. `maxChunkChars: 1200` keeps each chunk well inside the DeBERTa-v3 512-token window after tokenization. Increase only if your model has a larger context.

---

## Bring your own model

Palena does not depend on the deepset model specifically — it depends on the TEI `/predict` contract. Any HuggingFace classifier that exposes a `INJECTION`-style label works.

### Option 1 — fine-tune deepset's model

The recommended path. Continue training the deepset checkpoint on examples drawn from your `audit`-mode logs:

1. Run for 2–4 weeks in `audit` mode. Collect the maximum-scoring chunk per document into a labeling queue.
2. A reviewer marks each chunk as `INJECTION` or `LEGIT`. False positives become `LEGIT`, missed injections become `INJECTION`.
3. Mix the labeled set with the original deepset training data (to avoid catastrophic forgetting), then fine-tune `deepset/deberta-v3-base-injection` for 1–2 epochs.
4. Push the result to HuggingFace Hub (private repo) or to `injection-guard-cache` as a local path.
5. Update the sidecar's `--model-id` argument and Palena's `injection.model` field. No Palena rebuild required.

### Option 2 — train from scratch on the same backbone

If your domain is far from web text (e.g. EHR notes), start from `microsoft/deberta-v3-base` directly and train a fresh head. As long as the final model exports the same two-label output, Palena is happy. Rename the labels via `injection.injectionLabel` if your training data used a different convention (e.g. `unsafe` / `safe`).

### Option 3 — drop in a different architecture

TEI supports any HuggingFace `SequenceClassification` model: BERT, RoBERTa, DistilBERT, XLM-R, ModernBERT, etc. Trade-offs are model-specific (latency, memory, multilingual support). Adjust `maxChunkChars` to match the new model's context window.

---

## Audit records

Every classification emits an audit record at `INFO` level with event name `injection: audit`:

```json
{
  "time": "2026-04-18T10:23:44.112Z",
  "level": "INFO",
  "msg": "injection: audit",
  "request_id": "4f9a2b1c3d5e6f78",
  "url": "https://example.com/article",
  "mode": "annotate",
  "model": "deepset/deberta-v3-base-injection",
  "chunk_count": 12,
  "over_threshold": 1,
  "max_score": 0.972,
  "mean_score": 0.083,
  "action": "annotated",
  "content_length": 14820
}
```

> **Audit records never contain chunk text.** Only counts, scores, and aggregate statistics are logged. This invariant is enforced at the serializer — review `internal/injection/audit.go` if you are running a SOC2 audit.

Field shape mirrors the PII audit record where overlap exists, so a single SIEM dashboard can join both event types on `request_id` and `url`.

---

## Graceful degradation

If the injection-guard sidecar is unreachable at startup or during a request:

- Injection scanning is skipped for that document.
- `meta.injection_checked: false` is set on the response.
- A warning is logged at `WARN` level.
- The pipeline continues — the document still reaches the reranker, unmodified.

If your deployment requires injection enforcement for correctness (not just visibility), monitor `palena_injection_checked_total{checked="false"}` and alert on it.

---

## Compliance checklist

- [ ] `injection.enabled: true` once the sidecar is reachable.
- [ ] `injection.mode` set to `annotate` or `block` after a 4–6 week audit-mode burn-in.
- [ ] `injection.scoreThreshold` tuned against measured false-positive rate, not left at the default.
- [ ] Downstream LLM system prompt teaches the model to refuse instructions inside `<untrusted-content>` markers (mode=annotate only).
- [ ] Audit-log records shipped to SIEM.
- [ ] Alert on `palena_injection_checked_total{checked="false"} > 0` if enforcement is required.

---

## What's next

- [PII & Compliance](pii-and-compliance.md) — the companion sidecar for PII redaction.
- [Configuration — injection](configuration.md#injection) — every tuning knob.
- [Observability — injection metrics](observability.md#injection-metrics) — what to monitor.
