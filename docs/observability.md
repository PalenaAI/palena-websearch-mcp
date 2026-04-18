# Observability

Palena is built to be operable: every stage emits an OpenTelemetry span, every metric is Prometheus-compatible, health is a single HTTP call, and every log line is structured JSON. This page covers what is exposed, where to find it, and how to troubleshoot a stuck request.

---

## Endpoints

| Endpoint | Method | Purpose |
|---|---|---|
| `/health` | GET | Sidecar reachability probe. |
| `/metrics` | GET | Prometheus metrics (when `otel.metricExporter: prometheus`). |
| `/mcp` | POST | MCP Streamable HTTP transport. |
| `/sse` | GET | MCP SSE transport. |

All four run on the same Palena listener (`server.port`, default 8080).

---

## Health

```bash
curl -s http://palena:8080/health | jq
```

```json
{
  "status": "ok",
  "sidecars": {
    "searxng": "ok",
    "presidio_analyzer": "ok",
    "presidio_anonymizer": "ok",
    "playwright": "ok",
    "reranker": "ok"
  },
  "version": "0.1.0"
}
```

- `status: ok` means SearXNG (the one required sidecar) is reachable. Everything else is best-effort.
- `sidecars.<name>: unavailable` means the stage will be skipped but search can continue.
- `sidecars.<name>: ok` is a live reachability probe — `GET /healthz` on SearXNG, `GET /health` on Presidio, a WebSocket ping on Playwright, etc.

Use `/health` as a Kubernetes liveness and readiness probe for Palena:

```yaml
livenessProbe:
  httpGet:
    path: /health
    port: 8080
  initialDelaySeconds: 10
  periodSeconds: 30
readinessProbe:
  httpGet:
    path: /health
    port: 8080
  initialDelaySeconds: 5
  periodSeconds: 10
```

---

## Metrics

When `otel.metricExporter: prometheus`, metrics are exposed at `/metrics` in Prometheus exposition format. With `otel.metricExporter: otlp`, they are pushed to the OTLP endpoint configured in `otel.metricEndpoint`.

### Request-level

| Metric | Type | Labels | Description |
|---|---|---|---|
| `palena_requests_total` | counter | `status` | Total MCP tool calls. |
| `palena_request_duration_seconds` | histogram | `status` | End-to-end latency. |

### Search stage

| Metric | Type | Labels | Description |
|---|---|---|---|
| `palena_search_requests_total` | counter | `engine` | SearXNG requests by upstream engine. |
| `palena_search_duration_seconds` | histogram | — | SearXNG response time. |
| `palena_search_results_total` | histogram | `stage` (`raw`, `dedup`, `after_policy`) | Result counts at each filtering step. |

### Policy stage

| Metric | Type | Labels | Description |
|---|---|---|---|
| `palena_policy_filtered_total` | counter | `reason` (`domain_block`, `robots`, `rate_limit`) | URLs dropped by each policy gate. |
| `palena_policy_robots_fetches_total` | counter | `result` (`hit`, `miss`, `error`) | Cache behavior for robots.txt. |

### Scrape stage

| Metric | Type | Labels | Description |
|---|---|---|---|
| `palena_scrape_total` | counter | `level` (`0`, `1`, `2`), `result` (`success`, `failure`) | Scrape attempts by tier and outcome. |
| `palena_scrape_duration_seconds` | histogram | `level` | Per-URL scrape latency. |
| `palena_scrape_escalations_total` | counter | `from`, `to` | How often each escalation path was taken. |

### PII stage

| Metric | Type | Labels | Description |
|---|---|---|---|
| `palena_pii_checks_total` | counter | `mode`, `action` (`pass`, `redacted`, `blocked`) | PII actions taken. |
| `palena_pii_duration_seconds` | histogram | — | Presidio call latency. |
| `palena_pii_entities_detected` | histogram | `type` | Entities detected by entity type. |
| `palena_pii_checked_total` | counter | `checked` (`true`, `false`) | How often PII was actually verified. |

### Reranker stage

| Metric | Type | Labels | Description |
|---|---|---|---|
| `palena_reranker_requests_total` | counter | `provider`, `result` | Rerank calls by provider and outcome. |
| `palena_reranker_duration_seconds` | histogram | `provider` | Rerank call latency. |
| `palena_reranker_available` | gauge | `provider` | 1 if reachable, 0 if not. |

### Provenance stage

| Metric | Type | Labels | Description |
|---|---|---|---|
| `palena_provenance_records_total` | counter | `export` (`log`, `clickhouse`) | Records emitted by channel. |
| `palena_provenance_clickhouse_batch_seconds` | histogram | — | ClickHouse insert latency. |
| `palena_provenance_clickhouse_errors_total` | counter | — | Failed ClickHouse inserts. |

---

## OpenTelemetry traces

With `otel.enabled: true` and `otel.traceExporter: otlp`, Palena exports distributed traces to the configured OTLP collector. Each MCP tool call produces one trace with this span hierarchy:

```
palena.request                            (root span)
├── palena.search                         (SearXNG roundtrip)
├── palena.policy                         (domain + robots + rate limit)
├── palena.scrape                         (per-URL, run in parallel)
│   └── attributes: url, level, duration_ms, raw_html_hash, extracted_hash, final_hash
├── palena.pii                            (per-document)
│   └── attributes: mode, entities_detected, density, action, duration_ms
├── palena.rerank                         (one span for the batch)
│   └── attributes: provider, model, input_count, output_count, top_score
└── palena.format                         (markdown + provenance assembly)
```

Common backends:

- **Jaeger** — `otel.traceEndpoint: jaeger-collector:4317`
- **Tempo** — `otel.traceEndpoint: tempo-distributor.observability.svc:4317`
- **Honeycomb / Datadog / New Relic** — via an OTel Collector configured for their exporter.

Each span is tagged with `request_id` (equal to the OTel trace ID) and `url` (for scrape / PII spans). This is the same `request_id` that appears in structured logs and ClickHouse provenance records — giving you a single join key across traces, logs, and audit.

---

## Structured logs

All logs are JSON (`logging.format: json`) or tab-separated human text (`logging.format: text`). Key events:

| Message | Level | When |
|---|---|---|
| `server listening` | INFO | Startup, after binding the port. |
| `sidecar: <name> reachable` | INFO | On startup health probes. |
| `sidecar: <name> unavailable` | WARN | Probe failed — graceful degradation engaged. |
| `policy: domain blocked` | INFO | A URL was filtered out pre-scrape. |
| `policy: robots.txt disallowed` | INFO | A URL was filtered out by robots. |
| `policy: rate limit exceeded` | INFO | A URL was dropped by per-domain rate limit. |
| `scrape: L0 insufficient, escalating` | INFO | Content detection triggered an escalation. |
| `scrape: failed` | WARN | Per-URL scrape error (timeout, bot detection, DNS). |
| `pii: audit` | INFO | One audit record per scraped document — **the compliance log line**. |
| `provenance` | INFO | One provenance record per scraped document. |
| `reranker: unavailable` | WARN | Reranker timed out or was unreachable. |

### Finding a specific request in logs

```bash
# Docker Compose
docker compose logs palena | grep '"request_id":"4f9a2b1c3d5e6f78"'

# Kubernetes
kubectl logs -n palena -l app=palena --tail=10000 | grep '"request_id":"4f9a2b1c3d5e6f78"'
```

Shipping logs to a collector (Loki, Elasticsearch, Datadog) makes this far easier — the `request_id` is a stable, indexed field across every log line emitted during the request.

---

## Recommended alerts

A minimal Prometheus alert pack to start with:

```yaml
groups:
- name: palena
  rules:
  - alert: PalenaSearchHighErrorRate
    expr: sum(rate(palena_requests_total{status="error"}[5m]))
          / sum(rate(palena_requests_total[5m])) > 0.05
    for: 10m
    labels: { severity: warning }
    annotations: { summary: "Palena error rate > 5% for 10m" }

  - alert: PalenaPIIChecksSkipped
    expr: rate(palena_pii_checked_total{checked="false"}[5m]) > 0
    for: 5m
    labels: { severity: warning }
    annotations: { summary: "PII enforcement bypassed — Presidio unreachable" }

  - alert: PalenaRerankerDown
    expr: max(palena_reranker_available) == 0
    for: 10m
    labels: { severity: warning }
    annotations: { summary: "All configured rerankers unreachable" }

  - alert: PalenaScrapeL2Surge
    expr: rate(palena_scrape_total{level="2"}[10m]) > 2
    for: 15m
    labels: { severity: info }
    annotations: { summary: "L2 stealth scraping spiked — possible bot-protection shift upstream" }
```

Tune thresholds to your query volume.

---

## Troubleshooting

### Queries return zero results

Check in order:

1. `curl /health` — is SearXNG reachable?
2. Is `search.searxngURL` correct from inside the Palena container (`docker compose exec palena /palena --print-config`)?
3. Is `policy.domains.mode: allowlist` set with a list that excludes the result domains? Check logs for `policy: domain blocked`.
4. Is `policy.robots.enabled: true` blocking everything? Some domains disallow all bots — check logs for `policy: robots.txt disallowed`.
5. Is the query producing zero results in SearXNG directly? Open `http://localhost:8888/search?q=<your-query>&format=json` and see what SearXNG itself returns.

### Scraping is slow

- `palena_scrape_duration_seconds{level="0"}` high → upstream sites are slow; consider lowering `scraper.timeouts.httpGet`.
- `palena_scrape_duration_seconds{level="1"}` high → Playwright contention; raise `scraper.playwright.maxTabs` or the sidecar replica count.
- Many escalations (`palena_scrape_escalations_total{from="0",to="1"}`) → tune `scraper.contentDetection` thresholds; the defaults may be too aggressive for your target domains.

### PII is not being redacted

- Is `pii.mode` set to `audit`? Audit mode intentionally passes content through.
- Is Presidio reachable? Check `/health` and `palena_pii_checked_total{checked="false"}`.
- Is the entity type enabled in `pii.entities`?
- Is the confidence score too low to meet `pii.scoreThreshold`? Lower it to catch more detections.

### Reranker appears to do nothing

- Is `reranker.provider` set to `none`?
- Did the reranker call fail? Check `palena_reranker_requests_total{result="failure"}` and WARN logs.
- Is the result set too small for rerank order to differ from SearXNG order? With `maxResults: 2`, reranker reordering is often invisible.

### Docker Compose: one sidecar refuses to start

- `searxng`: the most common failure is missing `settings.yml` in `deploy/searxng/`. The Compose file mounts `./searxng/settings.yml` into the container.
- `presidio-analyzer`: first startup downloads spaCy/transformer models — can take 60–90 seconds. Wait.
- `playwright`: image pull is ~1.5 GB first time. Subsequent starts take a few seconds.
- `flashrank`: first build installs FlashRank via pip. Rebuilds use the layer cache.

---

## What's next

- [PII & Compliance — audit records](pii-and-compliance.md#audit-records) — the specific log events a SIEM should index.
- [Provenance](provenance.md) — the hash chain and long-term audit trail.
- [Deployment — Helm chart](deployment.md#helm-chart) — `ServiceMonitor` and Prometheus operator setup.
