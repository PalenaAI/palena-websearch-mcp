# Getting Started

This page gets a working Palena stack running on your laptop and walks through a first `web_search` call. It assumes you have Docker, `curl`, and roughly 4 GB of free RAM.

> Want the conceptual picture first? Read [Concepts](concepts.md).

---

## Prerequisites

| Requirement | Why |
|---|---|
| Docker 24+ with Compose v2 | Palena and its sidecars are distributed as container images. |
| 4 GB free RAM | Playwright's Chromium sidecar is the heaviest piece. |
| Internet access | SearXNG needs to reach the public search engines. |
| Ports free locally | `8080` (Palena), `8888` (SearXNG), `5001`–`5002` (Presidio), `3000` (Playwright), `8081` (FlashRank). |

You do **not** need a GPU. You do **not** need to install Go, Node, or Python — everything runs in containers.

---

## Bring up the full stack

Clone the repository and start the full Compose file. It pulls the upstream SearXNG, Presidio, and Playwright images, then builds Palena and a small FlashRank sidecar on first run.

```bash
git clone https://github.com/bitkaio/palena-websearch-mcp.git
cd palena-websearch-mcp
docker compose -f deploy/docker-compose.yml up --build
```

Expected output, abbreviated:

```
palena-1                | level=INFO msg="sidecar: searxng reachable"
palena-1                | level=INFO msg="sidecar: presidio-analyzer reachable"
palena-1                | level=INFO msg="sidecar: presidio-anonymizer reachable"
palena-1                | level=INFO msg="sidecar: playwright reachable"
palena-1                | level=INFO msg="sidecar: reranker reachable" provider=flashrank
palena-1                | level=INFO msg="server listening" addr=0.0.0.0:8080
```

If you would rather run Palena alone with only SearXNG (no browser, no PII, no reranker), use the minimal profile instead:

```bash
docker compose -f deploy/docker-compose.minimal.yml up --build
```

See [Deployment](deployment.md) for production profiles, Helm, and sizing.

---

## Verify the stack is healthy

```bash
curl -s http://localhost:8080/health | jq
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

A sidecar reporting `unavailable` here does not fail the server — Palena runs in degraded mode and reports what was skipped in each search response.

---

## Run your first query

Palena speaks MCP over two transports: **Streamable HTTP** at `POST /mcp` and **SSE** at `GET /sse`. The example below uses Streamable HTTP because it works well with `curl`.

MCP requires a three-step handshake: open a session, acknowledge, then call the tool.

### 1. Open a session

```bash
SESS=$(curl -sS -X POST http://localhost:8080/mcp \
  -H 'content-type: application/json' \
  -H 'accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"curl","version":"0.1"}}}' \
  -D - -o /dev/null | awk '/Mcp-Session-Id/ {print $2}' | tr -d '\r')

echo "session: $SESS"
```

### 2. Tell the server the client is initialized

```bash
curl -sS -X POST http://localhost:8080/mcp \
  -H 'content-type: application/json' \
  -H 'accept: application/json, text/event-stream' \
  -H "Mcp-Session-Id: $SESS" \
  -d '{"jsonrpc":"2.0","method":"notifications/initialized"}'
```

### 3. Call `web_search`

```bash
curl -sS -X POST http://localhost:8080/mcp \
  -H 'content-type: application/json' \
  -H 'accept: application/json, text/event-stream' \
  -H "Mcp-Session-Id: $SESS" \
  -d '{
    "jsonrpc":"2.0","id":2,"method":"tools/call",
    "params":{
      "name":"web_search",
      "arguments":{"query":"open source MCP servers 2026","category":"news","maxResults":3}
    }
  }'
```

The response is an MCP `tool_result` — a formatted markdown block plus a `meta` object with per-result scores, scraper level, content hash, and PII action. See [Tool Reference](tool-reference.md) for the full response shape.

---

## Next: connect an MCP client

With the server healthy, wire Palena into the client your agents actually use:

- **LibreChat** — [`librechat.yaml` snippet](integrations.md#librechat)
- **Claude Desktop** — [`claude_desktop_config.json` snippet](integrations.md#claude-desktop)
- **Cursor / Windsurf** — [local MCP server entry](integrations.md#cursor-windsurf)
- **Custom agent via the MCP SDK** — [Python and TypeScript examples](integrations.md#custom-agents)

---

## What to read next

- [Concepts](concepts.md) — how the six-stage pipeline fits together.
- [Configuration](configuration.md) — `palena.yaml`, env overrides, and recipes.
- [PII & Compliance](pii-and-compliance.md) — switching from `audit` to `redact` or `block` mode.
- [Deployment](deployment.md) — Helm chart, production sizing, GPU reranker.
