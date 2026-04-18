# Scraping

Palena turns a URL into clean, LLM-ready markdown using a **tiered** extraction strategy. The goal is to use the cheapest method that produces usable content and only escalate when the page demands it. This page explains each tier, when Palena decides to escalate, and how to tune the behavior.

> Configuration reference: [Configuration — scraper](configuration.md#scraper).

---

## Why tiered extraction

Most of the open web can be scraped with a plain HTTP GET. About 10–20 % of pages are client-rendered single-page apps where a plain GET returns an empty shell. A smaller tail of pages sit behind Cloudflare, Akamai, or other bot-protection layers that block automated browsers unless you blend in.

Spinning up a Chromium browser takes two to three orders of magnitude more CPU and RAM than an HTTP GET. Running every URL through stealth + proxy rotation is wasteful for the 80 % of pages that render fine from a curl.

Palena solves this by **escalating on demand**. Each tier is only invoked if the previous one returned insufficient content or was blocked.

---

## The three tiers

```
URL received
   │
   ▼
L0  HTTP GET + Mozilla Readability
   │    substantial text?           → return markdown
   │    SSR framework + content?    → return markdown
   │    empty shell / heavy JS?     → escalate
   ▼
L1  Chromium headless (Playwright)
   │    rendered content?           → return markdown
   │    403 / captcha / Cloudflare? → escalate
   ▼
L2  Chromium stealth + proxy rotation
   │    content extracted?          → return markdown
   └    still blocked?              → fail (this URL only)
```

Failure at L2 is per-URL. The rest of the pipeline continues with whatever URLs did succeed.

### L0 — HTTP + Readability

- Plain `HTTP GET` with a realistic `User-Agent` header.
- Response body is run through the Go port of Mozilla's Readability — the same algorithm Firefox Reader Mode uses.
- Boilerplate (navigation, ads, sidebars, footers) is stripped. What survives is title, byline, and the article body.
- Clean HTML is converted to markdown.

Typical latency: 100–700 ms per URL depending on the network.

**Why it is the default:** it is fast, cheap, and works for the vast majority of news sites, blogs, documentation, and Wikipedia.

### L1 — Playwright headless

- Runs inside a shared Playwright sidecar image (`mcr.microsoft.com/playwright`) that ships Chromium, Firefox, and WebKit. Palena currently drives Chromium only.
- A fresh `BrowserContext` is created per URL, so cookies and storage are isolated across requests.
- Navigation waits for `domcontentloaded`, then a short settle window for late JavaScript.
- The rendered HTML is passed through the same Readability extractor as L0.

Typical latency: 1.5–4 s per URL.

**When L1 is used:** when L0 detects the page is a client-rendered SPA (empty framework root, heavy script count, low text-to-markup ratio, `<noscript>` "enable JavaScript" warnings).

### L2 — Stealth + proxy rotation

- Same Playwright browser, plus a set of init scripts that ran before page JavaScript to make automated browsing harder to fingerprint:
  - `navigator.webdriver = false`
  - Realistic `navigator.plugins`, `languages`, `platform`
  - A minimal `window.chrome` object
  - A fixed `permissions.query` for notifications
- Viewport dimensions randomized within realistic ranges.
- User-Agent rotated per context.
- Optional proxy rotation drawn from a pool configured in YAML.

Typical latency: 2.5–8 s per URL.

**When L2 is used:** when L1 hits a bot-protection response (HTTP 403, a Cloudflare / DataDome challenge, or a CAPTCHA redirect). If you need to scrape sites behind such protections regularly, see [configuring a proxy pool](configuration.md#route-through-a-proxy-pool-for-l2).

---

## Content detection

The decision to escalate from L0 to L1 is driven by a short set of heuristics that run on the raw HTML response:

| Signal | Threshold | Meaning |
|---|---|---|
| Readable text length | `< minTextLength` (default 500) | Page is empty — likely SPA. |
| Text-to-markup ratio | `< minTextRatio` (default 0.05) | Almost all HTML is script/style. |
| Script tag count | `> maxScriptTags` (default 5) combined with low text | Likely an SPA. |
| Empty framework roots | `<div id="root">`, `#app`, `#__next` with no children | SPA waiting for JS. |
| `<noscript>` "enable JavaScript" | Present | Explicit SPA hint. |

A page that **does** have substantial readable text or a detectable SSR framework (Next.js `__NEXT_DATA__`, Nuxt `__NUXT__`) with content present is returned at L0 — even if it has many script tags. This avoids escalating well-behaved server-rendered React apps unnecessarily.

Tune these thresholds under `scraper.contentDetection` in `palena.yaml`. Lowering `minTextLength` makes Palena accept thinner pages at L0 (faster, sometimes lower quality). Raising `maxScriptTags` is more permissive of JS-heavy pages at L0.

---

## Concurrency and timeouts

Scraping is concurrent but bounded. The primary knob is `scraper.maxConcurrency` — how many URLs can be scraped in parallel across all tiers.

| Setting | Default | What it controls |
|---|---|---|
| `scraper.maxConcurrency` | 5 | Global scrape workers. |
| `scraper.playwright.maxTabs` | 3 | Concurrent browser contexts for L1/L2. |
| `scraper.timeouts.httpGet` | 10 s | L0 per-request timeout. |
| `scraper.timeouts.browserPage` | 15 s | L1/L2 per-page timeout. |
| `scraper.timeouts.browserNav` | 30 s | L1/L2 navigation timeout. |

A URL that exceeds its timeout is marked failed and the pipeline continues with the rest. Per-URL failures are logged at `WARN` level with the failing tier and the error.

---

## The output shape

Regardless of tier, each scrape produces:

```go
type ScrapedDocument struct {
    URL          string
    Title        string
    Markdown     string
    ScraperLevel int     // 0, 1, or 2
    DurationMs   int64
    RawHTMLHash  string
    ExtractedHash string
}
```

Markdown is normalized — headings, paragraphs, lists, code blocks, emphasis are preserved; inline images are replaced with `[Image: alt text]` placeholders; tracking links, stylesheets, and scripts are stripped. See [Provenance](provenance.md) for how `RawHTMLHash` and `ExtractedHash` relate to the final content hash.

---

## Graceful degradation

If the Playwright sidecar is not reachable at startup:

- L1 and L2 are disabled.
- L0 still runs for every URL.
- URLs that would have needed L1 / L2 are dropped from the result set.
- The `meta.scraper_level` for surviving URLs is always `0`.
- A startup warning is logged; health check reports `playwright: unavailable`.

This is intentional. If you want to permanently run without a browser (minimal footprint, L0-only), set `scraper.playwright.endpoint: ""`.

---

## Operator guidance

- **Keep `maxConcurrency` modest** — 5 is a good default for a single-node deployment. Higher values burn RAM (each Playwright context is ~80 MB) without proportional speedup because most time is spent waiting on upstream.
- **If scrape latency is a problem** and most of your pages are news articles, you can safely drop `scraper.timeouts.httpGet` to 5–7 s. For research papers or long-form articles, keep it at 10 s.
- **Respect robots.txt** in production deployments. The default is `policy.robots.enabled: true`; robots responses are cached for one hour to avoid hammering the robots endpoint of every domain.
- **Rate limiting** is per-domain, not global. A single query that returns 10 URLs from 10 distinct domains will happily run at full concurrency; a query that returns 10 URLs from `github.com` is clamped by `policy.rateLimit.requestsPerDomainPerMinute`.

---

## What's next

- [PII & Compliance](pii-and-compliance.md) — what happens to scraped content before it reaches the LLM.
- [Configuration — scraper](configuration.md#scraper) — every tuning knob.
- [Observability](observability.md) — per-URL spans and how to trace a problematic scrape.
