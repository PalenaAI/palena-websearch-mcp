# Deployment

This page covers running Palena in production — deployment profiles, Docker Compose, the Helm chart, sizing guidance, and notes on TLS, ingress, and secrets.

> For a laptop-scale test run, start with [Getting Started](getting-started.md).

---

## Deployment profiles

Palena itself is a single ~20 MB static Go binary. The footprint of a deployment is dominated by which sidecars are running.

| Profile | Components | Memory | Typical cost / month |
|---|---|---|---|
| **Minimal** | Palena + SearXNG | ~300 MB | A single small VM / container tier. |
| **Standard** | + Presidio Analyzer + Presidio Anonymizer + Playwright + FlashRank | ~2.5 GB | A single medium VM or a ~2-pod Kubernetes deployment. |
| **Enterprise** | + KServe with GPU reranker | Variable | A Kubernetes cluster with an NVIDIA A10G / L4 / A100. |

Pick the smallest profile that satisfies your requirements:

- **Minimal** — you only need L0 scraping (no JS-rendered pages), no PII enforcement, no reranker.
- **Standard** — you want the full compliance pipeline but can run CPU-only.
- **Enterprise** — you need state-of-the-art rerank quality at scale, or multi-tenant traffic that justifies a shared GPU.

---

## Docker Compose

Two Compose files ship with Palena:

### Full stack

[`deploy/docker-compose.yml`](https://github.com/PalenaAI/palena-websearch-mcp/blob/main/deploy/docker-compose.yml) — Palena plus every sidecar.

```bash
docker compose -f deploy/docker-compose.yml up --build
```

Services:

| Service | Image | Port (host) |
|---|---|---|
| `palena` | built from `deploy/Dockerfile` | 8080 |
| `searxng` | `searxng/searxng:latest` | 8888 |
| `presidio-analyzer` | `mcr.microsoft.com/presidio-analyzer:latest` | 5002 |
| `presidio-anonymizer` | `mcr.microsoft.com/presidio-anonymizer:latest` | 5001 |
| `playwright` | `mcr.microsoft.com/playwright:v1.57.0-jammy` | 3000 |
| `flashrank` | built from `deploy/Dockerfile.flashrank` | 8081 |

All sidecars are on a shared internal network (`palena_default`). Palena uses service names (`searxng`, `presidio-analyzer`, etc.) for inter-container calls. Host ports are exposed primarily for debugging — production deployments would typically keep sidecars internal.

### Minimal stack

[`deploy/docker-compose.minimal.yml`](https://github.com/PalenaAI/palena-websearch-mcp/blob/main/deploy/docker-compose.minimal.yml) — Palena + SearXNG only.

```bash
docker compose -f deploy/docker-compose.minimal.yml up --build
```

Good for local development, lightweight internal tools, and any environment where JavaScript-rendered pages and PII enforcement are not required.

### Bringing up a subset

The full Compose file can be trimmed at runtime by selecting a subset of services — for example, everything except the GPU reranker:

```bash
docker compose -f deploy/docker-compose.yml up \
  palena searxng presidio-analyzer presidio-anonymizer playwright flashrank
```

Remember to update `palena.yaml` (or the environment variable equivalents) to reflect what is actually available. Sidecars that are configured but unreachable trigger graceful degradation — see [Concepts — graceful degradation](concepts.md#graceful-degradation).

---

## Helm chart

The chart lives at [`deploy/helm/palena/`](https://github.com/PalenaAI/palena-websearch-mcp/tree/main/deploy/helm/palena). It bundles Palena and every sidecar; each optional sidecar is toggleable via `values.yaml`.

### Install

```bash
helm install palena deploy/helm/palena/ \
  --namespace palena --create-namespace
```

This deploys the full standard profile — Palena, SearXNG, Presidio (analyzer + anonymizer), Playwright, and FlashRank — each as a Deployment with a ClusterIP Service in front. No Ingress is created by default.

### Minimal profile

```bash
helm install palena deploy/helm/palena/ \
  --namespace palena --create-namespace \
  --set presidio.enabled=false \
  --set playwright.enabled=false \
  --set flashrank.enabled=false
```

### Enterprise profile (GPU reranker)

Assumes you already have a KServe `InferenceService` for a cross-encoder model running in another namespace. Disable the bundled FlashRank sidecar and point Palena at KServe:

```bash
helm install palena deploy/helm/palena/ \
  --namespace palena --create-namespace \
  --set flashrank.enabled=false \
  --set palena.config.reranker.provider=kserve \
  --set palena.config.reranker.endpoint=http://mxbai-rerank.kserve.svc.cluster.local:8080 \
  --set palena.config.reranker.model=mixedbread-ai/mxbai-rerank-large-v2
```

See [Reranking — KServe](reranking.md#kserve--gpu-cross-encoder) for model and InferenceService configuration.

### Values overview

| Key | Default | Purpose |
|---|---|---|
| `palena.image.repository` | `ghcr.io/bitkaio/palena` | Container image. |
| `palena.image.tag` | Chart version | Pinned tag. |
| `palena.replicas` | 2 | Horizontal scale. |
| `palena.resources.requests.cpu` | `200m` | Baseline per pod. |
| `palena.resources.limits.memory` | `512Mi` | Upper bound per pod. |
| `palena.config.*` | — | Full `palena.yaml` embedded in a ConfigMap. |
| `palena.env` | — | Environment overrides (`PALENA_*`). |
| `searxng.enabled` | `true` | Include SearXNG. |
| `presidio.enabled` | `true` | Include Presidio Analyzer + Anonymizer. |
| `playwright.enabled` | `true` | Include Playwright sidecar. |
| `flashrank.enabled` | `true` | Include FlashRank sidecar. |
| `serviceMonitor.enabled` | `false` | Create a Prometheus `ServiceMonitor` for `/metrics`. |

See [`deploy/helm/palena/values.yaml`](https://github.com/PalenaAI/palena-websearch-mcp/blob/main/deploy/helm/palena/values.yaml) for the complete annotated set.

### OpenShift

The chart works on OpenShift without changes. All containers run as non-root with a numeric UID so that OpenShift's default `restricted-v2` SCC is satisfied. If your cluster uses a custom SCC that blocks the Playwright sidecar's Chromium launch, add a custom SCC or fall back to L0-only by disabling `playwright`.

---

## Sizing guidance

### CPU

Palena itself is essentially free — under 1 % of a single core at 100 queries/minute. The workload lives in the sidecars.

| Sidecar | CPU at 100 QPM |
|---|---|
| Palena | <50 m |
| SearXNG | 100 m |
| Presidio Analyzer | 200–500 m (scales with document length) |
| Presidio Anonymizer | <100 m |
| Playwright | 500 m–2 vCPU (each active browser context) |
| FlashRank | 500 m–1 vCPU |

Plan for bursts — Playwright and FlashRank spike under concurrent load. Horizontal scaling of these sidecars is straightforward (stateless, behind ClusterIP); they scale independently of Palena.

### Memory

| Sidecar | Memory at steady state |
|---|---|
| Palena | 80–150 MB |
| SearXNG | 150–300 MB |
| Presidio Analyzer | 600–900 MB (loads NLP models at startup) |
| Presidio Anonymizer | 200 MB |
| Playwright | 400 MB + ~80 MB per active browser context |
| FlashRank | 300–500 MB |

A conservative Kubernetes starting point:

```yaml
palena:             { requests: { cpu: 200m, memory: 256Mi }, limits: { memory: 512Mi } }
searxng:            { requests: { cpu: 200m, memory: 256Mi }, limits: { memory: 512Mi } }
presidio-analyzer:  { requests: { cpu: 500m, memory: 1Gi },   limits: { memory: 2Gi  } }
presidio-anonymizer:{ requests: { cpu: 100m, memory: 256Mi }, limits: { memory: 512Mi } }
playwright:         { requests: { cpu: 1,    memory: 1Gi },   limits: { memory: 2Gi  } }
flashrank:          { requests: { cpu: 500m, memory: 512Mi }, limits: { memory: 1Gi  } }
```

Measure, then tune. `/metrics` exposes request latency histograms per stage; scale the sidecar whose p95 is closest to the configured timeout.

### GPU sizing (KServe)

For the recommended `mxbai-rerank-large-v2` (1.5 B parameters):

| GPU | Concurrent queries (rerank 10 docs each) | Notes |
|---|---|---|
| NVIDIA L4 (24 GB) | ~40 / s | Great price / perf for reranking. |
| NVIDIA A10G (24 GB) | ~60 / s | Workhorse for reranking. |
| NVIDIA A100 (40 / 80 GB) | 150–200 / s | Overkill for a single tenant; good for shared platforms. |

A single A10G usually suffices for a deployment serving up to ~1,000 active chat sessions.

---

## TLS and ingress

Palena does not terminate TLS. Put a reverse proxy in front of it in any public deployment.

### NGINX example

```nginx
server {
    listen 443 ssl http2;
    server_name palena.example.com;

    ssl_certificate     /etc/ssl/palena.crt;
    ssl_certificate_key /etc/ssl/palena.key;

    location / {
        proxy_pass         http://palena-upstream;
        proxy_http_version 1.1;

        # MCP streaming needs these
        proxy_buffering   off;
        proxy_read_timeout 1h;
        proxy_send_timeout 1h;

        proxy_set_header Host              $host;
        proxy_set_header X-Real-IP         $remote_addr;
        proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}

upstream palena-upstream {
    server palena:8080;
}
```

`proxy_buffering off` and a long `proxy_read_timeout` are important — SSE and Streamable HTTP are long-lived.

### Kubernetes Ingress

The Helm chart does not create an Ingress by default because the right gateway (NGINX, Istio, Traefik, Contour) depends on cluster policy. A minimal NGINX-ingress example:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: palena
  namespace: palena
  annotations:
    nginx.ingress.kubernetes.io/proxy-buffering: "off"
    nginx.ingress.kubernetes.io/proxy-read-timeout: "3600"
    cert-manager.io/cluster-issuer: "letsencrypt-prod"
spec:
  tls:
    - hosts: [palena.example.com]
      secretName: palena-tls
  rules:
    - host: palena.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: palena
                port:
                  number: 8080
```

---

## Secrets

Palena has no built-in secrets today. The fields that will typically hold sensitive values:

| Setting | Type | Recommendation |
|---|---|---|
| `scraper.proxy.pool[*].url` | Credential-bearing URL | Mount from a Kubernetes Secret via env var, e.g. `PALENA_SCRAPER_PROXY_POOL_0_URL`. |
| `reranker.endpoint` (when KServe is in another cluster) | Sometimes needs an API key | Use a sidecar auth proxy or bearer-token header. |
| `provenance.clickhouse.endpoint` | May include credentials | Env var override. |

Do not bake credentials into the ConfigMap that holds `palena.yaml`. Use env-var overrides from a Kubernetes Secret.

---

## Upgrade strategy

Palena stages are stateless except for:

- robots.txt cache (in-memory, per-pod, repopulates on demand).
- Per-domain rate-limit buckets (in-memory, per-pod).

You can rolling-update Palena pods freely. Sidecar caches warm quickly in practice — expect slightly elevated robots fetches for the first minute after a rollout.

ClickHouse provenance export uses batched inserts with a flush interval. On graceful shutdown, in-flight batches are flushed; on a hard kill, the most recent batch (up to `batchSize` or `flushInterval` seconds of records) can be lost.

---

## What's next

- [Observability](observability.md) — `/metrics`, traces, and alerts.
- [Configuration](configuration.md) — every key in `palena.yaml` and its env-var override.
- [Integrations](integrations.md) — connecting your MCP clients once Palena is up.
