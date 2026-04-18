# Configuration

Palena is configured by a single YAML file with environment variable overrides. The fully annotated example ships at [`config/palena.example.yaml`](../config/palena.example.yaml) — copy it to `palena.yaml` and edit from there.

---

## Loading order

1. Built-in defaults (compiled into the binary).
2. YAML file at the path in `PALENA_CONFIG_PATH` (default: `./palena.yaml`, inside the container: `/config/palena.yaml`).
3. Environment variables override any YAML value they are defined for.

Start the server with an explicit path:

```bash
PALENA_CONFIG_PATH=/etc/palena/palena.yaml /palena
```

---

## Environment variable naming

The pattern is `PALENA_<SECTION>_<KEY>`, uppercase, with underscores:

| YAML key | Env variable |
|---|---|
| `server.port` | `PALENA_SERVER_PORT` |
| `search.searxngURL` | `PALENA_SEARCH_SEARXNG_URL` |
| `scraper.playwright.endpoint` | `PALENA_SCRAPER_PLAYWRIGHT_ENDPOINT` |
| `pii.mode` | `PALENA_PII_MODE` |
| `reranker.provider` | `PALENA_RERANKER_PROVIDER` |
| `policy.domains.mode` | `PALENA_POLICY_DOMAINS_MODE` |

Env variables always win. Useful in Compose / Kubernetes where secret values (API keys for hosted rerankers, proxy credentials) should never live in YAML.

---

## Sections

### `server`

HTTP listener settings.

```yaml
server:
  host: "0.0.0.0"
  port: 8080
  readTimeout: 30s
  writeTimeout: 60s
```

### `search`

SearXNG integration and engine routing.

```yaml
search:
  searxngURL: "http://searxng:8080"        # required
  defaultEngines: ["google", "duckduckgo", "brave"]
  engineRoutes:
    general: ["google", "duckduckgo", "brave"]
    news:    ["google news", "duckduckgo", "bing news"]
    code:    ["github", "stackoverflow", "duckduckgo"]
    science: ["google scholar", "duckduckgo", "wikipedia"]
  defaultLanguage: "en"
  safeSearch: 1                            # 0=off, 1=moderate, 2=strict
  maxResults: 10                           # before reranking
  timeout: 10s
  queryExpansion:
    enabled: false
    maxVariants: 2
```

`maxResults` here is the **pre-rerank** pool. The tool's `maxResults` argument (1–20) then picks the top K from this pool. Keeping this larger than the tool's `maxResults` gives the reranker something to re-rank.

### `scraper`

Tiered extraction. Details in [Scraping](scraping.md).

```yaml
scraper:
  maxConcurrency: 5                        # parallel workers across all URLs
  timeouts:
    httpGet: 10s                           # L0
    browserPage: 15s                       # L1/L2 per-page
    browserNav: 30s                        # L1/L2 navigation
  playwright:
    endpoint: "ws://playwright:3000"       # empty string disables L1/L2
    maxTabs: 3
  stealth:
    enabled: true
    randomizeViewport: true
    randomizeUserAgent: true
  proxy:
    enabled: false
    pool: []
    cooldownSeconds: 300
  contentDetection:
    minTextLength: 500
    minTextRatio: 0.05
    maxScriptTags: 5
```

Setting `scraper.playwright.endpoint: ""` disables L1 and L2 entirely — useful for a lightweight footprint where only L0-eligible pages need to work.

### `policy`

Pre-scrape filtering. Details in [PII & Compliance — policy gates](pii-and-compliance.md#policy-gates).

```yaml
policy:
  robots:
    enabled: true
    cacheSeconds: 3600
  domains:
    mode: "blocklist"                      # "allowlist" or "blocklist"
    allowlist: []                          # suffix match — "github.com" covers "docs.github.com"
    blocklist: []
  rateLimit:
    enabled: true
    requestsPerDomainPerMinute: 10
```

### `pii`

Presidio integration. Details in [PII & Compliance](pii-and-compliance.md).

```yaml
pii:
  enabled: true
  mode: "audit"                            # audit | redact | block
  analyzerURL: "http://presidio-analyzer:5002"
  anonymizerURL: "http://presidio-anonymizer:5001"
  language: "en"
  scoreThreshold: 0.5
  blockThreshold: 5.0                      # entities per 1000 chars (mode=block)
  entities:
    - PERSON
    - EMAIL_ADDRESS
    - PHONE_NUMBER
    - CREDIT_CARD
    - IBAN_CODE
    - IP_ADDRESS
    - LOCATION
    - US_SSN
    - MEDICAL_LICENSE
  anonymizers:
    DEFAULT:       { type: replace, newValue: "<REDACTED>" }
    PERSON:        { type: replace, newValue: "<PERSON>" }
    EMAIL_ADDRESS: { type: mask, maskingChar: "*", charsToMask: 100, fromEnd: false }
    PHONE_NUMBER:  { type: replace, newValue: "<PHONE>" }
  timeout: 5s
```

### `reranker`

Pluggable reranker. Details in [Reranking](reranking.md).

```yaml
reranker:
  provider: "flashrank"                    # kserve | flashrank | rankllm | none
  endpoint: "http://flashrank:8080"
  model: ""                                # provider-specific
  topK: 5
  timeout: 10s
```

### `provenance`

Hash chain and audit export. Details in [Provenance](provenance.md).

```yaml
provenance:
  enabled: true
  clickhouse:
    enabled: false
    endpoint: "http://clickhouse:8123"
    database: "palena"
    table: "palena_provenance"
    batchSize: 50
    flushInterval: 5s
```

### `otel`

Traces and metrics. Details in [Observability](observability.md).

```yaml
otel:
  enabled: true
  serviceName: "palena"
  traceExporter: "otlp"                    # otlp | stdout | none
  traceEndpoint: "otel-collector:4317"
  metricExporter: "prometheus"             # prometheus | otlp | stdout | none
  metricEndpoint: "otel-collector:4317"
  sampleRate: 1.0
  exportTimeout: 10s
```

With `metricExporter: prometheus`, metrics are served at `GET /metrics` on the main Palena port.

### `logging`

```yaml
logging:
  level: "info"                            # debug | info | warn | error
  format: "json"                           # json | text
```

Ship these to any collector (Loki, ELK, Datadog, etc.). PII audit records are emitted as structured log events at `INFO` with the fixed message `"pii: audit"`.

---

## Recipes

### Tighten PII enforcement for a healthtech deployment

```yaml
pii:
  mode: "redact"                           # no raw PII reaches the LLM
  scoreThreshold: 0.4                      # catch lower-confidence detections
  entities:
    - PERSON
    - EMAIL_ADDRESS
    - PHONE_NUMBER
    - US_SSN
    - MEDICAL_LICENSE
    - DATE_TIME                            # add if DoBs matter
    - LOCATION                             # add if addresses matter
```

### Block content-heavy PII pages entirely

```yaml
pii:
  mode: "block"
  blockThreshold: 3.0                      # > 3 entities / 1000 chars → drop
```

### Use a GPU reranker in production

```yaml
reranker:
  provider: "kserve"
  endpoint: "http://mxbai-rerank.kserve.svc.cluster.local:8080"
  model: "mixedbread-ai/mxbai-rerank-large-v2"
  topK: 5
```

### Restrict to an enterprise allowlist

```yaml
policy:
  domains:
    mode: "allowlist"
    allowlist:
      - "wikipedia.org"
      - "arxiv.org"
      - "nih.gov"
      - "ec.europa.eu"
  rateLimit:
    enabled: true
    requestsPerDomainPerMinute: 30
```

### L0-only (no browser)

```yaml
scraper:
  playwright:
    endpoint: ""                           # disables L1/L2
```

Saves about 1.5 GB of RAM and the Playwright sidecar.

### Route through a proxy pool for L2

```yaml
scraper:
  proxy:
    enabled: true
    pool:
      - url: "http://user:pass@proxy-us.example.com:8080"
        region: "us"
      - url: "socks5://user:pass@proxy-eu.example.com:1080"
        region: "eu"
    cooldownSeconds: 600
```

### Wire up the full provenance trail

```yaml
provenance:
  enabled: true
  clickhouse:
    enabled: true
    endpoint: "http://clickhouse.observability.svc.cluster.local:8123"
    database: "palena"
    table: "palena_provenance"
    batchSize: 100
    flushInterval: 10s
```

---

## Validation

Palena validates configuration at startup and refuses to start on:

- Missing `search.searxngURL`.
- `pii.mode` outside `audit` / `redact` / `block`.
- `reranker.provider` outside `kserve` / `flashrank` / `rankllm` / `none`.
- `reranker.provider != none` but `reranker.endpoint` empty.
- `policy.domains.mode` outside `allowlist` / `blocklist`.
- Negative or zero timeouts.

Validation errors are logged at startup with the specific key that failed. Fix the config and restart.

---

## Related

- [Getting Started](getting-started.md) — running the defaults.
- [Scraping](scraping.md) — tuning content detection and browser behavior.
- [Observability](observability.md) — OTel exporter setup in detail.
