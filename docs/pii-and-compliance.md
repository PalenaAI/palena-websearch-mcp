# PII & Compliance

Palena is designed for organizations that cannot send raw third-party web content directly to a foundation model. This page covers the three mechanisms that enforce that boundary:

1. **Pre-scrape policy gates** — domain filter, robots.txt, per-domain rate limits.
2. **PII detection and redaction** via Microsoft Presidio.
3. **Audit records** that prove what was found, what was changed, and what the LLM consumed.

For the content-integrity side of compliance (hash chain, ClickHouse audit export), see [Provenance](provenance.md).

---

## Policy gates

Policy is enforced **before** any page is fetched. This means a URL that is blocked never touches the scraper, and no browser cycles, proxy credits, or partial cache entries are ever spent on it.

### Domain allow / block

Two modes, configured under `policy.domains`:

- **`blocklist`** (default) — drop any URL whose hostname matches a suffix in the blocklist. Everything else is allowed.
- **`allowlist`** — drop any URL whose hostname does **not** match a suffix in the allowlist. Everything else is blocked.

Suffix matching means `github.com` in the list covers `docs.github.com`, `www.github.com`, and `api.github.com` — but not `github.com.evil.io`. Matching is case-insensitive; entries are normalized at load time.

```yaml
policy:
  domains:
    mode: "allowlist"
    allowlist:
      - "wikipedia.org"
      - "arxiv.org"
      - "nih.gov"
      - "ec.europa.eu"
```

### robots.txt

Every candidate domain's `robots.txt` is fetched on first use and cached for `policy.robots.cacheSeconds` (default 3600). Palena identifies itself as `User-agent: Palena`; the robots checker evaluates against this group if present, otherwise the default `*` group.

- A URL disallowed by robots is dropped from the result pool with an `INFO`-level log entry.
- If the robots response itself fails (timeout, 5xx, malformed), Palena **fails open** — the URL is allowed and the failure is logged at `DEBUG`.

Disable robots enforcement only in closed environments where you already have permission to scrape:

```yaml
policy:
  robots:
    enabled: false
```

### Per-domain rate limit

A token bucket per hostname, refilling every minute. Defaults to 10 requests per domain per minute. A query that pulls 10 URLs from 10 distinct domains is untouched; a query pulling 10 URLs from the same domain is clamped.

```yaml
policy:
  rateLimit:
    enabled: true
    requestsPerDomainPerMinute: 30
```

Rate-limited URLs are dropped from the current request (not queued), logged at `INFO`, and the remaining URLs continue through the pipeline.

---

## PII detection with Presidio

Palena sends every successfully scraped document through [Microsoft Presidio](https://microsoft.github.io/presidio/), which runs as two stateless sidecars: Analyzer (detection) and Anonymizer (masking). Both are Apache 2.0 and run non-root.

Presidio detects a configurable set of entity types. The defaults cover the common cases for regulated industries:

| Entity | Typical use case |
|---|---|
| `PERSON` | Any detected full name. |
| `EMAIL_ADDRESS` | RFC-style and common display variants. |
| `PHONE_NUMBER` | International and regional formats. |
| `CREDIT_CARD` | Major networks; Luhn-validated. |
| `IBAN_CODE` | Bank account numbers (EU / international). |
| `US_SSN` | US Social Security numbers. |
| `MEDICAL_LICENSE` | DEA numbers and medical license IDs. |
| `IP_ADDRESS` | IPv4 / IPv6. |
| `LOCATION` | Cities, addresses, country names. |
| `DATE_TIME` | Birthdates, appointment times (if enabled). |
| `URL` | Can be enabled in environments where shortlinks are sensitive. |
| `CRYPTO` | Wallet addresses (ETH/BTC). |

The full catalog is in [Presidio's entity reference](https://microsoft.github.io/presidio/supported_entities/). Add what you need under `pii.entities` in `palena.yaml`.

### The three modes

Every deployment picks exactly one mode. The mode is immutable for a given request — there is no per-tool-call override.

#### `audit` (default)

- Analyzer runs, detections are logged.
- Anonymizer is **not** called.
- Content passes through to the reranker unchanged.
- The audit record is emitted with entity types, counts, and confidence scores.
- `meta.pii_checked: true`, `meta.results[*].pii_action: "pass"`.

**Use case:** compliance teams want visibility into what would flow into the LLM but have not yet decided on a redaction policy. A common first step for a fintech rollout.

#### `redact`

- Analyzer runs. If any entities are detected above `scoreThreshold`:
- Anonymizer is called with the per-entity rules from `pii.anonymizers`.
- Content passed to the reranker is the anonymized version.
- The audit record captures both original findings and anonymization actions.
- `meta.results[*].pii_action: "redacted"`, `meta.results[*].content_hash` will differ from what L0/L1 produced.

**Use case:** strict environments — healthtech, insurance, defense — where raw PII must never reach the LLM or any downstream cache.

#### `block`

- Analyzer runs.
- If **PII density** (entities per 1000 characters) exceeds `blockThreshold`, the document is dropped entirely.
- If under threshold, content is passed through (optionally redacted depending on `pii.mode` interaction — blocking is additive to `audit`).
- Dropped documents do not appear in `results[]`; a note is added to the response metadata.

**Use case:** high-risk sources where even small PII content is unacceptable — e.g. litigation research against a sensitive-document corpus.

### Per-entity anonymization rules

When `mode: redact`, each entity type can be masked or replaced independently:

```yaml
pii:
  anonymizers:
    DEFAULT:
      type: "replace"
      newValue: "<REDACTED>"
    PERSON:
      type: "replace"
      newValue: "<PERSON>"
    EMAIL_ADDRESS:
      type: "mask"
      maskingChar: "*"
      charsToMask: 100
      fromEnd: false
    PHONE_NUMBER:
      type: "replace"
      newValue: "<PHONE>"
    CREDIT_CARD:
      type: "mask"
      maskingChar: "X"
      charsToMask: 12
      fromEnd: false                      # keeps last 4 digits visible
```

Two operator types are supported:

- **`replace`** — swap the matched span for a fixed string. Stable and simple.
- **`mask`** — replace a portion of the matched span with `maskingChar`. `fromEnd: true` preserves the start of the string; `fromEnd: false` preserves the end.

---

## Audit records

Every PII analysis emits an audit record, regardless of mode. Records are emitted in two places:

### Structured log

Every record is a single `slog` entry at `INFO` level with a fixed event name:

```json
{
  "time": "2026-04-18T10:23:44.112Z",
  "level": "INFO",
  "msg": "pii: audit",
  "request_id": "4f9a2b1c3d5e6f78",
  "url": "https://example.com/article",
  "mode": "audit",
  "language": "en",
  "entity_count": 3,
  "pii_density": 1.2,
  "action": "pass",
  "content_length": 2480,
  "entities": [
    { "type": "PERSON",        "score": 0.95, "count": 1 },
    { "type": "EMAIL_ADDRESS", "score": 1.00, "count": 1 },
    { "type": "PHONE_NUMBER",  "score": 0.75, "count": 1 }
  ]
}
```

Ship these logs to any collector — Loki, Elasticsearch, Datadog, Splunk — and build your PII observability dashboard there.

### ClickHouse (optional)

When `provenance.clickhouse.enabled: true`, each provenance record (which includes PII fields) is batched and inserted into ClickHouse. See [Provenance — audit export](provenance.md#clickhouse-schema) for the schema.

### What audit records never contain

> **Audit records never contain the actual PII text.** Only entity types, counts, and confidence scores are logged.

This is enforced at the point of emission — the audit serializer reads from a separate, PII-free data structure. If you are running a SOC2 or HIPAA review, this invariant is the one to validate.

---

## Graceful degradation

If Presidio is unreachable at startup or during a request:

- PII processing is skipped for this document.
- `meta.pii_checked: false` is set on the response.
- A warning is logged at `WARN` level.
- The pipeline continues — the document still reaches the reranker, unmodified.

If your deployment requires PII enforcement for correctness (not just visibility), monitor for `pii_checked: false` responses and alert on them. A sample Prometheus alert is in [Observability — alerts](observability.md#recommended-alerts).

---

## Compliance checklist

- [ ] `policy.domains` configured (allowlist preferred for regulated use cases).
- [ ] `policy.robots.enabled: true` in production.
- [ ] `policy.rateLimit.enabled: true` in production.
- [ ] `pii.mode` set to `redact` or `block` — not `audit` — once your compliance team has reviewed the entity rules.
- [ ] `pii.entities` includes every entity class relevant to your regulator (e.g. `US_SSN` for HIPAA, `IBAN_CODE` for EU fintech, `MEDICAL_LICENSE` for DEA-regulated workflows).
- [ ] Structured logs shipped to your SIEM with PII-audit records indexed.
- [ ] `provenance.enabled: true` (on by default).
- [ ] `provenance.clickhouse.enabled: true` if you need long-term queryable audit storage.
- [ ] An alert on `palena_pii_checked_total{checked="false"} > 0` in Prometheus.

---

## What's next

- [Provenance](provenance.md) — the content hash chain and long-term audit trail.
- [Configuration — pii](configuration.md#pii) — every tuning knob.
- [Observability — PII metrics](observability.md#pii-metrics) — what to monitor.
