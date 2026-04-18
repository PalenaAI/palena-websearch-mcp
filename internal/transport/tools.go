// Copyright (c) 2026 BITKAIO LLC. All rights reserved.
// Use of this source code is governed by the AGPL-3.0 license.

package transport

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	otelmetric "go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bitkaio/palena-websearch-mcp/internal/config"
	"github.com/bitkaio/palena-websearch-mcp/internal/injection"
	palenaOTel "github.com/bitkaio/palena-websearch-mcp/internal/otel"
	"github.com/bitkaio/palena-websearch-mcp/internal/output"
	"github.com/bitkaio/palena-websearch-mcp/internal/pii"
	"github.com/bitkaio/palena-websearch-mcp/internal/policy"
	"github.com/bitkaio/palena-websearch-mcp/internal/reranker"
	"github.com/bitkaio/palena-websearch-mcp/internal/scraper"
	"github.com/bitkaio/palena-websearch-mcp/internal/search"
)

// WebSearchInput matches the web_search tool's inputSchema from docs/MCP.md.
//
// The jsonschema tag is used as the field description by the MCP Go SDK's
// schema inference (google/jsonschema-go). That library treats the entire
// tag value as the description — no key=value syntax is supported, so enums,
// defaults, and numeric bounds are enforced at runtime in HandleWebSearch
// rather than via the schema. See docs/MCP.md for the authoritative schema.
type WebSearchInput struct {
	Query      string `json:"query" jsonschema:"The search query"`
	Category   string `json:"category,omitempty" jsonschema:"Search category — one of: general, news, code, science (default general)"`
	Language   string `json:"language,omitempty" jsonschema:"Language code for search results, e.g. en, de, fr (default en)"`
	TimeRange  string `json:"timeRange,omitempty" jsonschema:"Filter results by time range — one of: day, week, month, year"`
	MaxResults int    `json:"maxResults,omitempty" jsonschema:"Maximum number of results to return, 1-20 (default 5)"`
}

// WebSearchOutput is the structured output returned alongside the text content.
type WebSearchOutput struct {
	Query             string       `json:"query"`
	ResultCount       int          `json:"result_count"`
	SearchEngines     []string     `json:"search_engines"`
	PIIMode           string       `json:"pii_mode"`
	PIIChecked        bool         `json:"pii_checked"`
	InjectionMode     string       `json:"injection_mode"`
	InjectionChecked  bool         `json:"injection_checked"`
	InjectionBlocked  int          `json:"injection_blocked"`
	RerankerUsed      string       `json:"reranker_used"`
	TotalDuration     int64        `json:"total_duration_ms"`
	Results           []ResultMeta `json:"results"`
}

// ResultMeta holds per-result metadata for the tool response.
type ResultMeta struct {
	URL              string  `json:"url"`
	Title            string  `json:"title"`
	Score            float64 `json:"score"`
	ScraperLevel     int     `json:"scraper_level"`
	ContentHash      string  `json:"content_hash"`
	InjectionAction  string  `json:"injection_action,omitempty"`  // pass | annotated | skipped
	InjectionMaxScore float64 `json:"injection_max_score,omitempty"`
}

// ToolHandler holds the dependencies needed to execute the web_search tool.
type ToolHandler struct {
	searchClient  *search.SearXNGClient
	scraper       *scraper.Scraper
	domainFilter  *policy.DomainFilter
	robotsChecker *policy.RobotsChecker
	rateLimiter   *policy.RateLimiter
	pii           *pii.Processor
	injection     *injection.Processor
	reranker      reranker.Reranker
	cfg           *config.Config
	logger        *slog.Logger
	meters        *palenaOTel.Meters
	provExporter  *output.ClickHouseExporter
}

// NewToolHandler creates a handler with the given pipeline components.
func NewToolHandler(
	searchClient *search.SearXNGClient,
	sc *scraper.Scraper,
	domainFilter *policy.DomainFilter,
	robotsChecker *policy.RobotsChecker,
	rateLimiter *policy.RateLimiter,
	piiProc *pii.Processor,
	injProc *injection.Processor,
	rr reranker.Reranker,
	cfg *config.Config,
	meters *palenaOTel.Meters,
	provExporter *output.ClickHouseExporter,
	logger *slog.Logger,
) *ToolHandler {
	return &ToolHandler{
		searchClient:  searchClient,
		scraper:       sc,
		domainFilter:  domainFilter,
		robotsChecker: robotsChecker,
		rateLimiter:   rateLimiter,
		pii:           piiProc,
		injection:     injProc,
		reranker:      rr,
		cfg:           cfg,
		logger:        logger,
		meters:        meters,
		provExporter:  provExporter,
	}
}

// WebSearchTool returns the MCP tool definition for registration.
func WebSearchTool() *mcp.Tool {
	return &mcp.Tool{
		Name:        "web_search",
		Description: "Search the web and retrieve relevant content from result pages. Returns scraped and optionally reranked content with citations and source URLs. Content is checked for PII according to deployment policy.",
	}
}

// scrapedDoc holds a single scraped+converted document flowing through the pipeline.
type scrapedDoc struct {
	URL              string
	Title            string
	Markdown         string
	RawHTML          string // original HTML for provenance hashing
	Level            int
	InjectionAction  string  // pass | annotated | skipped
	InjectionMaxScore float64
}

// HandleWebSearch is the tool handler called by the MCP server when a client
// invokes the web_search tool.
func (h *ToolHandler) HandleWebSearch(
	ctx context.Context,
	req *mcp.CallToolRequest,
	input WebSearchInput,
) (*mcp.CallToolResult, WebSearchOutput, error) {
	start := time.Now()

	// Root span: palena.pipeline
	ctx, pipelineSpan := palenaOTel.StartSpan(ctx, "palena.pipeline")
	defer pipelineSpan.End()

	// Apply defaults.
	category := input.Category
	if category == "" {
		category = "general"
	}
	language := input.Language
	if language == "" {
		language = h.cfg.Search.DefaultLanguage
	}
	maxResults := input.MaxResults
	if maxResults <= 0 {
		maxResults = 5
	}
	if maxResults > 20 {
		maxResults = 20
	}

	engines := h.searchClient.EnginesForCategory(category)

	pipelineSpan.SetAttributes(
		attribute.String("palena.query", input.Query),
		attribute.String("palena.category", category),
		attribute.String("palena.language", language),
		attribute.Int("palena.max_results", maxResults),
	)

	if h.meters != nil {
		h.meters.PipelineRequests.Add(ctx, 1)
	}

	h.logger.InfoContext(ctx, "web_search tool invoked",
		"query", input.Query,
		"category", category,
		"language", language,
		"max_results", maxResults,
	)

	// --- Stage 1: Search ---
	searchStart := time.Now()
	searchCtx, searchSpan := palenaOTel.StartSpan(ctx, "palena.search")

	searchSpan.SetAttributes(
		attribute.String("search.query", input.Query),
		attribute.StringSlice("search.engines", engines),
	)
	if h.meters != nil {
		h.meters.SearchRequests.Add(searchCtx, 1)
	}

	searchResp, err := h.searchClient.Search(searchCtx, search.SearchRequest{
		Query:      input.Query,
		Engines:    engines,
		Categories: []string{category},
		Language:   language,
		TimeRange:  input.TimeRange,
		SafeSearch: h.cfg.Search.SafeSearch,
		MaxResults: h.cfg.Search.MaxResults,
	})

	searchDuration := float64(time.Since(searchStart).Milliseconds())
	if h.meters != nil {
		h.meters.SearchDuration.Record(searchCtx, searchDuration)
	}

	if err != nil {
		palenaOTel.SetSpanError(searchSpan, err)
		searchSpan.End()
		palenaOTel.SetSpanError(pipelineSpan, err)
		return nil, WebSearchOutput{}, fmt.Errorf("search failed: %w", err)
	}

	searchSpan.SetAttributes(
		attribute.Int("search.result_count", len(searchResp.Results)),
		attribute.Float64("search.duration_ms", searchDuration),
	)
	palenaOTel.SetSpanOK(searchSpan)
	searchSpan.End()

	if len(searchResp.Results) == 0 {
		return nil, WebSearchOutput{}, fmt.Errorf(
			"SearXNG returned no results for query %q. Try a different query", input.Query,
		)
	}

	// Stage 2: Deduplicate.
	deduped := search.Deduplicate(searchResp.Results)

	// Limit to maxResults for scraping.
	scrapeLimit := maxResults
	if len(deduped) < scrapeLimit {
		scrapeLimit = len(deduped)
	}
	policyResults := deduped[:scrapeLimit]

	// --- Stage 2.5: Domain Policy ---
	policyCtx, policySpan := palenaOTel.StartSpan(ctx, "palena.policy")
	policySpan.SetAttributes(attribute.Int("policy.input_count", len(policyResults)))

	// 1. Domain filter.
	if h.domainFilter != nil {
		allowed, dropped := h.domainFilter.Filter(policyResults)
		policySpan.SetAttributes(attribute.Int("policy.domain_dropped", len(dropped)))
		policyResults = allowed
	}

	// 2. Robots.txt check.
	if h.robotsChecker != nil {
		allowed, blocked := h.robotsChecker.CheckAll(policyCtx, policyResults)
		policySpan.SetAttributes(attribute.Int("policy.robots_blocked", len(blocked)))
		policyResults = allowed
	}

	// 3. Rate limit.
	if h.rateLimiter != nil {
		allowed, limited := h.rateLimiter.FilterAll(policyResults)
		policySpan.SetAttributes(attribute.Int("policy.rate_limited", len(limited)))
		policyResults = allowed
	}

	policySpan.SetAttributes(attribute.Int("policy.results_after", len(policyResults)))
	palenaOTel.SetSpanOK(policySpan)
	policySpan.End()

	if len(policyResults) == 0 {
		err := fmt.Errorf("all results filtered by policy for query %q", input.Query)
		palenaOTel.SetSpanError(pipelineSpan, err)
		return nil, WebSearchOutput{}, err
	}

	urls := make([]string, len(policyResults))
	for i, r := range policyResults {
		urls[i] = r.URL
	}

	// --- Stage 3: Scrape ---
	scrapeStart := time.Now()
	scrapeCtx, scrapeSpan := palenaOTel.StartSpan(ctx, "palena.scrape")
	scrapeSpan.SetAttributes(attribute.Int("scrape.url_count", len(urls)))

	scrapeResults := h.scraper.ScrapeAll(scrapeCtx, urls)

	var scraped []scrapedDoc
	for _, sr := range scrapeResults {
		if h.meters != nil {
			h.meters.ScrapeAttempts.Add(scrapeCtx, 1,
				otelmetric.WithAttributes(attribute.String("scrape.level", output.ExtractionMethodName(sr.Level))),
			)
		}

		if sr.Err != nil {
			if h.meters != nil {
				h.meters.ScrapeErrors.Add(scrapeCtx, 1)
			}
			h.logger.WarnContext(ctx, "scrape failed, skipping result",
				"url", sr.URL, "error", sr.Err,
			)
			continue
		}

		md, err := output.HTMLToMarkdown(sr.Content)
		if err != nil {
			h.logger.WarnContext(ctx, "markdown conversion failed, skipping",
				"url", sr.URL, "error", err,
			)
			continue
		}
		if md == "" {
			continue
		}

		if h.meters != nil {
			h.meters.ContentLength.Record(scrapeCtx, int64(len(md)))
		}

		scraped = append(scraped, scrapedDoc{
			URL:      sr.URL,
			Title:    sr.Title,
			Markdown: md,
			RawHTML:  sr.TextContent, // raw text for provenance; RawHTML not exposed on ScrapeResult
			Level:    sr.Level,
		})
	}

	scrapeDuration := float64(time.Since(scrapeStart).Milliseconds())
	if h.meters != nil {
		h.meters.ScrapeDuration.Record(scrapeCtx, scrapeDuration)
	}
	scrapeSpan.SetAttributes(
		attribute.Int("scrape.success_count", len(scraped)),
		attribute.Float64("scrape.duration_ms", scrapeDuration),
	)

	if len(scraped) == 0 {
		err := fmt.Errorf("all %d URLs failed to scrape for query %q", len(urls), input.Query)
		palenaOTel.SetSpanError(scrapeSpan, err)
		scrapeSpan.End()
		palenaOTel.SetSpanError(pipelineSpan, err)
		return nil, WebSearchOutput{}, err
	}
	palenaOTel.SetSpanOK(scrapeSpan)
	scrapeSpan.End()

	// --- Stage 3.5: PII detection/redaction ---
	requestID := trace.SpanFromContext(ctx).SpanContext().TraceID().String()
	piiMode := h.cfg.PII.Mode
	piiChecked := false
	var blockedURLs []string

	if h.pii != nil {
		piiStart := time.Now()
		piiCtx, piiSpan := palenaOTel.StartSpan(ctx, "palena.pii")
		piiSpan.SetAttributes(attribute.String("pii.mode", piiMode))

		var filtered []scrapedDoc
		for _, s := range scraped {
			result := h.pii.Process(piiCtx, &pii.Document{
				URL:      s.URL,
				Content:  s.Markdown,
				Language: language,
			}, requestID)

			piiChecked = piiChecked || result.PIIChecked

			if result.Blocked {
				blockedURLs = append(blockedURLs, s.URL)
				if h.meters != nil {
					h.meters.PIIBlocked.Add(piiCtx, 1)
				}
				h.logger.InfoContext(ctx, "document blocked by PII policy",
					"url", s.URL,
				)
				continue
			}

			if result.PIIChecked && h.meters != nil {
				h.meters.PIIEntities.Add(piiCtx, int64(result.Audit.EntityCount))
			}

			s.Markdown = result.Content
			filtered = append(filtered, s)
		}
		scraped = filtered

		piiDuration := float64(time.Since(piiStart).Milliseconds())
		if h.meters != nil {
			h.meters.PIIDuration.Record(piiCtx, piiDuration)
		}
		piiSpan.SetAttributes(
			attribute.Int("pii.input_count", len(scraped)+len(blockedURLs)),
			attribute.Int("pii.blocked_count", len(blockedURLs)),
			attribute.Float64("pii.duration_ms", piiDuration),
		)
		palenaOTel.SetSpanOK(piiSpan)
		piiSpan.End()
	}

	if len(scraped) == 0 {
		return nil, WebSearchOutput{}, fmt.Errorf(
			"all documents were blocked by PII policy for query %q", input.Query,
		)
	}

	// --- Stage 3.7: Prompt-injection scan ---
	// Optional. If injection.enabled=false the processor's Process() is a
	// no-op pass-through; if the sidecar is unreachable it logs a warning
	// and returns Checked=false without failing the document. The pipeline
	// never short-circuits on injection-stage failure unless mode=block
	// flagged a chunk above the configured threshold.
	injMode := h.cfg.Injection.Mode
	injChecked := false
	var injBlockedURLs []string

	if h.injection != nil {
		injStart := time.Now()
		injCtx, injSpan := palenaOTel.StartSpan(ctx, "palena.injection")
		injSpan.SetAttributes(
			attribute.String("injection.mode", injMode),
			attribute.Bool("injection.enabled", h.cfg.Injection.Enabled),
		)

		var filtered []scrapedDoc
		for _, s := range scraped {
			result := h.injection.Process(injCtx, &injection.Document{
				URL:     s.URL,
				Content: s.Markdown,
			}, requestID)

			injChecked = injChecked || result.Checked

			if result.Checked && h.meters != nil {
				h.meters.InjectionChunks.Add(injCtx, int64(result.Audit.ChunkCount))
				h.meters.InjectionFlagged.Add(injCtx, int64(result.Audit.OverThreshold))
			}

			if result.Blocked {
				injBlockedURLs = append(injBlockedURLs, s.URL)
				if h.meters != nil {
					h.meters.InjectionBlocked.Add(injCtx, 1)
				}
				h.logger.InfoContext(ctx, "document blocked by injection policy",
					"url", s.URL,
					"max_score", result.MaxScore,
				)
				continue
			}

			s.Markdown = result.Content
			s.InjectionAction = result.Action
			s.InjectionMaxScore = result.MaxScore
			filtered = append(filtered, s)
		}
		scraped = filtered

		injDuration := float64(time.Since(injStart).Milliseconds())
		if h.meters != nil {
			h.meters.InjectionDuration.Record(injCtx, injDuration)
		}
		injSpan.SetAttributes(
			attribute.Int("injection.input_count", len(scraped)+len(injBlockedURLs)),
			attribute.Int("injection.blocked_count", len(injBlockedURLs)),
			attribute.Float64("injection.duration_ms", injDuration),
		)
		palenaOTel.SetSpanOK(injSpan)
		injSpan.End()
	}

	if len(scraped) == 0 {
		return nil, WebSearchOutput{}, fmt.Errorf(
			"all documents were blocked by injection policy for query %q", input.Query,
		)
	}

	// --- Stage 4: Rerank ---
	rerankStart := time.Now()
	rerankCtx, rerankSpan := palenaOTel.StartSpan(ctx, "palena.rerank")
	rerankerName := h.reranker.Name()

	rerankSpan.SetAttributes(
		attribute.String("rerank.provider", rerankerName),
		attribute.Int("rerank.input_count", len(scraped)),
	)
	if h.meters != nil {
		h.meters.RerankRequests.Add(rerankCtx, 1)
	}

	docs := make([]reranker.Document, len(scraped))
	for i, s := range scraped {
		docs[i] = reranker.Document{
			Index:   i,
			URL:     s.URL,
			Title:   s.Title,
			Content: s.Markdown,
		}
	}

	ranked, err := h.reranker.Rerank(rerankCtx, input.Query, docs)
	if err != nil {
		h.logger.WarnContext(ctx, "reranker failed, using original order",
			"reranker", rerankerName, "error", err,
		)
		ranked = make([]reranker.RankedDocument, len(docs))
		for i, d := range docs {
			ranked[i] = reranker.RankedDocument{Document: d, Score: 1.0 - float64(i)*0.01, Rank: i + 1}
		}
		rerankerName = "none (fallback)"
	}

	rerankDuration := float64(time.Since(rerankStart).Milliseconds())
	if h.meters != nil {
		h.meters.RerankDuration.Record(rerankCtx, rerankDuration)
	}

	topScore := 0.0
	if len(ranked) > 0 {
		topScore = ranked[0].Score
	}
	rerankSpan.SetAttributes(
		attribute.Int("rerank.output_count", len(ranked)),
		attribute.Float64("rerank.top_score", topScore),
		attribute.Float64("rerank.duration_ms", rerankDuration),
	)
	palenaOTel.SetSpanOK(rerankSpan)
	rerankSpan.End()

	// --- Stage 5: Format response + Provenance ---
	var contentBuilder strings.Builder
	fmt.Fprintf(&contentBuilder, "# Search Results for: %q\n\n", input.Query)

	var resultMetas []ResultMeta

	for _, rd := range ranked {
		idx := rd.Document.Index
		doc := scraped[idx]
		md := doc.Markdown

		// Truncate long content.
		const maxContentLen = 5000
		if len(md) > maxContentLen {
			md = md[:maxContentLen] + "\n\n... [truncated]"
		}

		// Compute provenance hashes.
		rawHTMLHash, extractedHash, finalHash := output.ComputeProvenance(
			doc.RawHTML,  // raw content
			doc.Markdown, // extracted markdown (before truncation)
			md,           // final content as delivered
		)

		fmt.Fprintf(&contentBuilder, "## %d. %s\n", rd.Rank, rd.Document.Title)
		fmt.Fprintf(&contentBuilder, "**Source:** %s\n", rd.Document.URL)
		fmt.Fprintf(&contentBuilder, "**Relevance:** %.2f\n\n", rd.Score)
		fmt.Fprintf(&contentBuilder, "%s\n\n---\n\n", md)

		resultMetas = append(resultMetas, ResultMeta{
			URL:               rd.Document.URL,
			Title:             rd.Document.Title,
			Score:             rd.Score,
			ScraperLevel:      doc.Level,
			ContentHash:       finalHash,
			InjectionAction:   doc.InjectionAction,
			InjectionMaxScore: doc.InjectionMaxScore,
		})

		// Build and emit provenance record.
		if h.cfg.Provenance.Enabled {
			prov := &output.ProvenanceRecord{
				RequestID:        requestID,
				Timestamp:        start,
				URL:              doc.URL,
				FinalURL:         doc.URL,
				ScraperLevel:     doc.Level,
				RawHTMLHash:      rawHTMLHash,
				ExtractedHash:    extractedHash,
				FinalHash:        finalHash,
				ContentLength:    len(md),
				ExtractionMethod: output.ExtractionMethodName(doc.Level),
				PIIMode:          piiMode,
				PIIAction:        "pass",
				RerankerScore:    rd.Score,
				RerankerRank:     rd.Rank,
				TotalDurationMs:  time.Since(start).Milliseconds(),
			}

			output.LogProvenance(h.logger, prov)

			if h.provExporter != nil {
				h.provExporter.Add(prov)
			}

			// Attach provenance hashes as span attributes on per-URL child span.
			_, provSpan := palenaOTel.StartSpan(ctx, "palena.scrape.provenance")
			provSpan.SetAttributes(
				attribute.String("provenance.url", doc.URL),
				attribute.String("provenance.raw_html_hash", rawHTMLHash),
				attribute.String("provenance.extracted_hash", extractedHash),
				attribute.String("provenance.final_hash", finalHash),
				attribute.Int("provenance.scraper_level", doc.Level),
				attribute.String("provenance.extraction_method", output.ExtractionMethodName(doc.Level)),
			)
			provSpan.End()
		}
	}

	// Append blocked documents note if any.
	if len(blockedURLs) > 0 {
		fmt.Fprintf(&contentBuilder, "**Note:** %d document(s) were blocked by PII policy and excluded from results.\n\n", len(blockedURLs))
	}
	if len(injBlockedURLs) > 0 {
		fmt.Fprintf(&contentBuilder, "**Note:** %d document(s) were blocked by prompt-injection policy and excluded from results.\n\n", len(injBlockedURLs))
	}

	// Append sources footer.
	fmt.Fprintf(&contentBuilder, "**Sources:**\n")
	for i, rm := range resultMetas {
		fmt.Fprintf(&contentBuilder, "[%d] %s\n", i+1, rm.URL)
	}

	piiStatus := fmt.Sprintf("%s (checked=%t)", piiMode, piiChecked)
	injStatus := "disabled"
	if h.cfg.Injection.Enabled {
		injStatus = fmt.Sprintf("%s (checked=%t, blocked=%d)", injMode, injChecked, len(injBlockedURLs))
	}
	fmt.Fprintf(&contentBuilder, "\n**Metadata:**\n")
	fmt.Fprintf(&contentBuilder, "- Results returned: %d\n", len(resultMetas))
	fmt.Fprintf(&contentBuilder, "- PII: %s\n", piiStatus)
	fmt.Fprintf(&contentBuilder, "- Injection: %s\n", injStatus)
	fmt.Fprintf(&contentBuilder, "- Reranker: %s\n", rerankerName)
	fmt.Fprintf(&contentBuilder, "- Search engines: %s\n", strings.Join(engines, ", "))

	elapsed := time.Since(start)

	// Record pipeline duration metric.
	if h.meters != nil {
		h.meters.PipelineDuration.Record(ctx, float64(elapsed.Milliseconds()))
	}

	pipelineSpan.SetAttributes(
		attribute.Int("pipeline.result_count", len(resultMetas)),
		attribute.Int64("pipeline.duration_ms", elapsed.Milliseconds()),
	)
	palenaOTel.SetSpanOK(pipelineSpan)

	meta := WebSearchOutput{
		Query:            input.Query,
		ResultCount:      len(resultMetas),
		SearchEngines:    engines,
		PIIMode:          piiMode,
		PIIChecked:       piiChecked,
		InjectionMode:    injMode,
		InjectionChecked: injChecked,
		InjectionBlocked: len(injBlockedURLs),
		RerankerUsed:     rerankerName,
		TotalDuration:    elapsed.Milliseconds(),
		Results:          resultMetas,
	}

	h.logger.InfoContext(ctx, "web_search tool completed",
		"query", input.Query,
		"results", len(resultMetas),
		"reranker", rerankerName,
		"duration_ms", elapsed.Milliseconds(),
	)

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: contentBuilder.String()},
		},
	}, meta, nil
}
