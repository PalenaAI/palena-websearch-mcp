# Provenance

Palena produces an auditable record for every piece of web content it delivers to an LLM: where it came from, when it was fetched, how it was processed, whether PII was found, and exactly what the LLM consumed. This is the feature that separates "AI with a web tool" from "AI with a web tool your compliance team can ship to production."

> For detection behavior and PII modes, see [PII & Compliance](pii-and-compliance.md). For the export endpoint, see [Configuration — provenance](configuration.md#provenance).

---

## What a provenance record contains

One record per scraped URL:

```json
{
  "request_id":          "4f9a2b1c3d5e6f78",
  "timestamp":           "2026-04-18T10:23:44.112Z",
  "url":                 "https://example.com/article",
  "final_url":           "https://example.com/article",

  "http_status":         200,
  "scraper_level":       0,
  "content_type":        "text/html; charset=utf-8",

  "raw_html_hash":       "9e48c3118f4b…",
  "extracted_hash":      "c3b4d6901a7e…",
  "final_hash":          "c3b4d6901a7e…",

  "content_length":      2480,
  "extraction_method":   "readability",
  "pii_mode":            "audit",
  "pii_entities_found":  3,
  "pii_action":          "pass",
  "reranker_score":      0.984,
  "reranker_rank":       1,

  "robots_txt_checked":  true,
  "robots_txt_allowed":  true,

  "scrape_duration_ms":  410,
  "pii_duration_ms":     82,
  "total_duration_ms":   4414
}
```

Every field is sourced from the pipeline itself — no manual annotation, no agent-supplied metadata.

---

## The three-stage hash chain

Three SHA-256 hashes are computed for each document:

```
  raw_html_hash      extracted_hash         final_hash
        │                   │                    │
        ▼                   ▼                    ▼
┌───────────────┐   ┌───────────────┐   ┌───────────────┐
│ Raw HTML      │→→→│ Clean markdown │→→→│ Content as    │
│ as received   │   │ after readability │  │ delivered to  │
│ from server   │   │ extraction     │   │ the LLM       │
│ (or browser)  │   │                │   │ (post-PII)    │
└───────────────┘   └───────────────┘   └───────────────┘
```

### `raw_html_hash`

SHA-256 of the HTML body as received — from the `net/http` response at L0, or from `page.Content()` at L1 / L2. This is what the source server actually sent, before any extraction.

### `extracted_hash`

SHA-256 of the clean markdown produced by readability extraction. A change between `raw_html_hash` and `extracted_hash` is expected and normal — it proves the extraction step ran.

### `final_hash`

SHA-256 of the content as delivered in the MCP tool response — after any PII anonymization. This is what the LLM actually consumed.

- If PII mode is `audit` and no entities were redacted: `extracted_hash == final_hash`. The content passed through.
- If PII mode is `redact` and at least one entity was anonymized: `extracted_hash != final_hash`. The content was modified.
- If PII mode is `block` and the page was dropped: no record with this URL appears in the response metadata (the audit record is still emitted for the attempted analysis).

Auditors can verify the chain end-to-end: re-run the raw HTML through readability, check against `extracted_hash`; re-run the extracted markdown through the configured anonymizer rules, check against `final_hash`. Any divergence is evidence of tampering or configuration drift.

---

## Where records land

### Structured logs (always)

Every record is emitted as a single `slog` entry at `INFO` level:

```json
{
  "time": "2026-04-18T10:23:44.112Z",
  "level": "INFO",
  "msg": "provenance",
  "request_id": "4f9a2b1c3d5e6f78",
  "url": "https://example.com/article",
  "scraper_level": 0,
  "raw_html_hash": "9e48c3118f4b…",
  "extracted_hash": "c3b4d6901a7e…",
  "final_hash": "c3b4d6901a7e…",
  "pii_entities_found": 3,
  "pii_action": "pass"
}
```

Collect these with any log aggregator (Loki, Elasticsearch, Splunk, Datadog). Index on `request_id`, `url`, and `final_hash`.

### ClickHouse (optional, recommended for regulated environments)

When `provenance.clickhouse.enabled: true`, records are batched and inserted into ClickHouse for fast long-term querying. The default schema:

```sql
CREATE TABLE palena_provenance (
    request_id          String,
    timestamp           DateTime64(3),
    url                 String,
    final_url           String,
    http_status         UInt16,
    scraper_level       UInt8,
    raw_html_hash       String,
    extracted_hash      String,
    final_hash          String,
    content_length      UInt32,
    extraction_method   String,
    pii_mode            String,
    pii_entities_found  UInt16,
    pii_action          String,
    reranker_score      Float64,
    reranker_rank       UInt16,
    robots_txt_checked  Bool,
    robots_txt_allowed  Bool,
    scrape_duration_ms  UInt32,
    pii_duration_ms     UInt32,
    total_duration_ms   UInt32
)
ENGINE = MergeTree()
ORDER BY (timestamp, request_id)
TTL timestamp + INTERVAL 90 DAY;
```

Tune the TTL to match your retention policy. Fintech regulators typically require 5–7 years; HIPAA requires 6. Adjust the `TTL` clause accordingly.

Batch configuration:

```yaml
provenance:
  enabled: true
  clickhouse:
    enabled: true
    endpoint: "http://clickhouse.observability.svc.cluster.local:8123"
    database: "palena"
    table: "palena_provenance"
    batchSize: 100                         # records per INSERT
    flushInterval: 10s                     # force flush if batch not full
```

### MCP response metadata

A subset of each provenance record is returned to the MCP caller as part of the tool response `meta` block — specifically `content_hash` (the `final_hash`), `scraper_level`, `pii_entities`, and `pii_action`. See [Tool Reference — response](tool-reference.md#meta-object). This lets downstream systems pin a reference to a specific version of the content.

---

## OpenTelemetry linkage

Provenance hashes are attached as span attributes on the per-URL `palena.scrape` span:

| Attribute | Value |
|---|---|
| `provenance.raw_html_hash` | Hash of raw HTML. |
| `provenance.extracted_hash` | Hash of readability output. |
| `provenance.final_hash` | Hash of content as delivered. |
| `scrape.url` | Source URL. |
| `scrape.level` | 0, 1, or 2. |
| `scrape.duration_ms` | Per-URL scrape duration. |

An auditor can join on the OTel trace ID (which is the `request_id` in log records) to reconstruct the full pipeline for any single scrape: search span → policy span → scrape span → PII span → rerank span → format span.

See [Observability](observability.md) for the exporter setup.

---

## Use cases

### "What did the LLM see last Tuesday for request X?"

Query by `request_id`:

```sql
SELECT url, final_hash, pii_action, total_duration_ms
FROM palena_provenance
WHERE request_id = '4f9a2b1c3d5e6f78'
ORDER BY reranker_rank ASC;
```

### "Which URLs has this agent scraped in the last 24 hours?"

```sql
SELECT url, count(*) AS n, max(timestamp) AS latest
FROM palena_provenance
WHERE timestamp > now() - INTERVAL 1 DAY
GROUP BY url
ORDER BY n DESC
LIMIT 100;
```

### "Has any page changed since last time we fetched it?"

Because `raw_html_hash` is deterministic, a new record with the same URL but a different hash proves the source page mutated:

```sql
SELECT url, count(DISTINCT raw_html_hash) AS versions
FROM palena_provenance
WHERE url = 'https://example.com/article'
GROUP BY url;
```

### "What entered the LLM prompt vs. what the source page contained?"

Compare `raw_html_hash` and `final_hash` for the record in question. If they differ, inspect the intermediate `extracted_hash` to see whether the difference came from extraction (raw → extracted) or from PII redaction (extracted → final).

---

## Retention and data minimization

Palena's provenance records intentionally contain **no page content** — only hashes, metrics, and metadata. This minimizes the audit trail's regulatory burden while still enabling full integrity verification.

If you need to retain the actual content (not just hashes), pair Palena with a content archive: for each `final_hash`, store the corresponding markdown in object storage keyed by hash. That way:

- Regulators can audit the trail without Palena itself holding sensitive content.
- Agents that need to re-consult a previously seen result can fetch it by hash.
- Retention policies for the hash-indexed content archive can be managed independently of the audit database.

A reference archive integration is not part of Palena itself — it is expected to be implemented in each customer's data platform.

---

## Disabling provenance

Set `provenance.enabled: false` to skip hash computation and record emission entirely. The hash fields in MCP response metadata will be empty strings; no log records or ClickHouse inserts are produced.

Disabling provenance defeats the primary compliance advantage of Palena. Only do this in non-regulated development environments.

---

## What's next

- [PII & Compliance](pii-and-compliance.md) — the detection and redaction side of the audit trail.
- [Observability](observability.md) — the OpenTelemetry side of the audit trail.
- [Configuration — provenance](configuration.md#provenance) — tuning knobs.
