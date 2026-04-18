# Integrations

Palena is an MCP server and works with any MCP-compatible client. This page shows the wiring for the clients teams most often use, plus a minimal example for a custom agent.

> For a server that is not running yet, start with [Getting Started](getting-started.md). For the tool itself, see [Tool Reference](tool-reference.md).

---

## Transport cheat sheet

Palena exposes two MCP transports on the same port:

| Transport | Endpoint | Preferred client |
|---|---|---|
| Streamable HTTP | `POST http://<host>:8080/mcp` | Modern MCP clients, custom agents, server-to-server. |
| SSE | `GET http://<host>:8080/sse` + `POST http://<host>:8080/messages` | Claude Desktop, older LibreChat builds. |

If a client supports both, prefer Streamable HTTP — it is simpler to proxy and easier to debug.

---

## LibreChat

Add Palena to the `mcpServers` block in `librechat.yaml`:

```yaml
mcpServers:
  palena:
    type: streamableHttp
    url: http://palena:8080/mcp
```

Or, using SSE for older builds:

```yaml
mcpServers:
  palena:
    type: sse
    url: http://palena:8080/sse
```

If LibreChat and Palena are on the same Docker network or the same Kubernetes namespace, use the service name (`palena`). Restart LibreChat; `web_search` appears in the tools list automatically for any agent or model that supports tool calling.

### Full-stack deployment

Running both in Kubernetes? Point LibreChat's `mcpServers.palena.url` at the ClusterIP service the Helm chart creates:

```yaml
mcpServers:
  palena:
    type: streamableHttp
    url: http://palena.palena.svc.cluster.local:8080/mcp
```

See [Deployment](deployment.md) for the Helm chart.

---

## Claude Desktop

Edit `claude_desktop_config.json` (macOS: `~/Library/Application Support/Claude/claude_desktop_config.json`):

```json
{
  "mcpServers": {
    "palena": {
      "url": "http://localhost:8080/sse"
    }
  }
}
```

Claude Desktop currently uses the SSE transport. Restart the app; `web_search` appears in the tools tray.

If Palena is on a remote machine, put a TLS-terminating reverse proxy in front of it and point `url` at the HTTPS address. Palena itself does not terminate TLS — see [Deployment](deployment.md#tls-and-ingress).

---

## Cursor / Windsurf

Both IDEs support MCP servers via a local configuration file. The exact path differs but the shape is the same.

**Cursor** — `~/.cursor/mcp.json`:

```json
{
  "mcpServers": {
    "palena": {
      "url": "http://localhost:8080/mcp"
    }
  }
}
```

**Windsurf** — `~/.codeium/windsurf/mcp_config.json`:

```json
{
  "mcpServers": {
    "palena": {
      "url": "http://localhost:8080/mcp"
    }
  }
}
```

Once configured, the `web_search` tool appears alongside the built-in tools. Cursor shows the pipeline metadata (PII mode, reranker, engines) in its tool-use panel — useful for spot-checking compliance behavior while developing.

---

## Custom agents

Palena is MCP-compliant, so any MCP SDK works. The examples below use the official MCP SDKs from [modelcontextprotocol.io](https://modelcontextprotocol.io).

### Python

```python
import asyncio
from mcp import ClientSession
from mcp.client.streamable_http import streamablehttp_client

async def main():
    async with streamablehttp_client("http://localhost:8080/mcp") as (read, write, _):
        async with ClientSession(read, write) as session:
            await session.initialize()

            tools = await session.list_tools()
            print("available:", [t.name for t in tools.tools])

            result = await session.call_tool(
                "web_search",
                arguments={
                    "query": "langchain vs llamaindex 2026",
                    "category": "general",
                    "maxResults": 3,
                },
            )
            for block in result.content:
                print(block.text)

asyncio.run(main())
```

### TypeScript

```ts
import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { StreamableHTTPClientTransport } from "@modelcontextprotocol/sdk/client/streamableHttp.js";

const transport = new StreamableHTTPClientTransport(
  new URL("http://localhost:8080/mcp"),
);

const client = new Client({ name: "my-agent", version: "0.1.0" }, { capabilities: {} });
await client.connect(transport);

const tools = await client.listTools();
console.log("available:", tools.tools.map((t) => t.name));

const result = await client.callTool({
  name: "web_search",
  arguments: {
    query: "regulatory changes EU AI Act 2026",
    category: "news",
    timeRange: "month",
    maxResults: 5,
  },
});

for (const block of result.content) {
  if (block.type === "text") console.log(block.text);
}
```

### Bare HTTP (no SDK)

MCP is JSON-RPC 2.0 over HTTP — if you cannot use an SDK, call it directly. See the three-step example in [Getting Started](getting-started.md#run-your-first-query).

---

## Authentication

Palena does **not** ship authentication. It assumes it sits behind a network boundary:

- In a Compose stack, Palena is on a private Docker network with the other sidecars. Only the MCP client can reach it.
- In Kubernetes, the Helm chart creates a ClusterIP service (no Ingress by default).
- For public exposure, run Palena behind a reverse proxy that handles TLS, mutual TLS, OAuth, or IP allowlisting.

See [Deployment — TLS and Ingress](deployment.md#tls-and-ingress) for example NGINX and Istio configs.

---

## Troubleshooting the connection

| Symptom | Likely cause |
|---|---|
| Client reports "no tools" | Client connected but skipped the `notifications/initialized` step. Most SDKs handle this automatically; custom code may need to send it explicitly. |
| `401 Mcp-Session-Id` required | Streamable HTTP session expired or never opened. Re-run `initialize`. |
| `EHOSTUNREACH` in Docker | Client container not on the same network as Palena. Add it to the `palena_default` network or use the host IP. |
| SSE connection drops every 30 s | Reverse proxy is buffering or timing out. Disable response buffering and set a long read timeout (NGINX: `proxy_buffering off; proxy_read_timeout 1h;`). |
| `tool execution failed` with no details | Check the Palena container logs — the root-cause error is logged at `WARN` / `ERROR` level with the OTel trace ID. |

---

## What's next

- [Tool Reference](tool-reference.md) — full `web_search` parameters and response shape.
- [Deployment](deployment.md) — production deployment, TLS, Helm, sizing.
- [Observability](observability.md) — tracing a single request across every stage.
