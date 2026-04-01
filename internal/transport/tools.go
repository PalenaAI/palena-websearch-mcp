// Copyright (c) 2026 BITKAIO LLC. All rights reserved.
// Use of this source code is governed by the AGPL-3.0 license.

package transport

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bitkaio/palena-websearch-mcp/internal/config"
	"github.com/bitkaio/palena-websearch-mcp/internal/output"
	"github.com/bitkaio/palena-websearch-mcp/internal/reranker"
	"github.com/bitkaio/palena-websearch-mcp/internal/scraper"
	"github.com/bitkaio/palena-websearch-mcp/internal/search"
)

// WebSearchInput matches the web_search tool's inputSchema from docs/MCP.md.
type WebSearchInput struct {
	Query      string `json:"query" jsonschema:"description=The search query"`
	Category   string `json:"category,omitempty" jsonschema:"enum=general,enum=news,enum=code,enum=science,default=general,description=Search category — routes to different search engines"`
	Language   string `json:"language,omitempty" jsonschema:"default=en,description=Language code for search results"`
	TimeRange  string `json:"timeRange,omitempty" jsonschema:"enum=day,enum=week,enum=month,enum=year,description=Filter results by time range"`
	MaxResults int    `json:"maxResults,omitempty" jsonschema:"default=5,minimum=1,maximum=20,description=Maximum number of results to return"`
}

// WebSearchOutput is the structured output returned alongside the text content.
type WebSearchOutput struct {
	Query         string       `json:"query"`
	ResultCount   int          `json:"result_count"`
	SearchEngines []string     `json:"search_engines"`
	PIIMode       string       `json:"pii_mode"`
	PIIChecked    bool         `json:"pii_checked"`
	RerankerUsed  string       `json:"reranker_used"`
	TotalDuration int64        `json:"total_duration_ms"`
	Results       []ResultMeta `json:"results"`
}

// ResultMeta holds per-result metadata for the tool response.
type ResultMeta struct {
	URL          string  `json:"url"`
	Title        string  `json:"title"`
	Score        float64 `json:"score"`
	ScraperLevel int     `json:"scraper_level"`
	ContentHash  string  `json:"content_hash"`
}

// ToolHandler holds the dependencies needed to execute the web_search tool.
type ToolHandler struct {
	searchClient *search.SearXNGClient
	scraper      *scraper.Scraper
	reranker     reranker.Reranker
	cfg          *config.Config
	logger       *slog.Logger
}

// NewToolHandler creates a handler with the given pipeline components.
func NewToolHandler(
	searchClient *search.SearXNGClient,
	sc *scraper.Scraper,
	rr reranker.Reranker,
	cfg *config.Config,
	logger *slog.Logger,
) *ToolHandler {
	return &ToolHandler{
		searchClient: searchClient,
		scraper:      sc,
		reranker:     rr,
		cfg:          cfg,
		logger:       logger,
	}
}

// WebSearchTool returns the MCP tool definition for registration.
func WebSearchTool() *mcp.Tool {
	return &mcp.Tool{
		Name:        "web_search",
		Description: "Search the web and retrieve relevant content from result pages. Returns scraped and optionally reranked content with citations and source URLs. Content is checked for PII according to deployment policy.",
	}
}

// HandleWebSearch is the tool handler called by the MCP server when a client
// invokes the web_search tool.
func (h *ToolHandler) HandleWebSearch(
	ctx context.Context,
	req *mcp.CallToolRequest,
	input WebSearchInput,
) (*mcp.CallToolResult, WebSearchOutput, error) {
	start := time.Now()

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

	h.logger.InfoContext(ctx, "web_search tool invoked",
		"query", input.Query,
		"category", category,
		"language", language,
		"max_results", maxResults,
	)

	// Stage 1: Search.
	searchResp, err := h.searchClient.Search(ctx, search.SearchRequest{
		Query:      input.Query,
		Engines:    engines,
		Categories: []string{category},
		Language:   language,
		TimeRange:  input.TimeRange,
		SafeSearch: h.cfg.Search.SafeSearch,
		MaxResults: h.cfg.Search.MaxResults,
	})
	if err != nil {
		return nil, WebSearchOutput{}, fmt.Errorf("search failed: %w", err)
	}

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

	urls := make([]string, scrapeLimit)
	scores := make(map[string]float64, scrapeLimit)
	for i := 0; i < scrapeLimit; i++ {
		urls[i] = deduped[i].URL
		scores[deduped[i].URL] = deduped[i].Score
	}

	// Stage 3: Scrape (L0 only for now).
	scrapeResults := h.scraper.ScrapeAll(ctx, urls)

	// Convert scrape results to markdown and build reranker documents.
	type scrapedDoc struct {
		URL      string
		Title    string
		Markdown string
		Level    int
	}
	var scraped []scrapedDoc
	for _, sr := range scrapeResults {
		if sr.Err != nil {
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

		scraped = append(scraped, scrapedDoc{
			URL:      sr.URL,
			Title:    sr.Title,
			Markdown: md,
			Level:    sr.Level,
		})
	}

	if len(scraped) == 0 {
		return nil, WebSearchOutput{}, fmt.Errorf(
			"all %d URLs failed to scrape for query %q", len(urls), input.Query,
		)
	}

	// Stage 4: Rerank.
	rerankerName := h.reranker.Name()
	docs := make([]reranker.Document, len(scraped))
	for i, s := range scraped {
		docs[i] = reranker.Document{
			Index:   i,
			URL:     s.URL,
			Title:   s.Title,
			Content: s.Markdown,
		}
	}

	ranked, err := h.reranker.Rerank(ctx, input.Query, docs)
	if err != nil {
		h.logger.WarnContext(ctx, "reranker failed, using original order",
			"reranker", rerankerName, "error", err,
		)
		// Fallback: use original order with synthetic scores.
		ranked = make([]reranker.RankedDocument, len(docs))
		for i, d := range docs {
			ranked[i] = reranker.RankedDocument{Document: d, Score: 1.0 - float64(i)*0.01, Rank: i + 1}
		}
		rerankerName = "none (fallback)"
	}

	// Stage 5: Format response.
	var contentBuilder strings.Builder
	fmt.Fprintf(&contentBuilder, "# Search Results for: %q\n\n", input.Query)

	var resultMetas []ResultMeta

	for _, rd := range ranked {
		idx := rd.Document.Index
		md := scraped[idx].Markdown

		// Truncate long content.
		const maxContentLen = 5000
		if len(md) > maxContentLen {
			md = md[:maxContentLen] + "\n\n... [truncated]"
		}

		contentHash := fmt.Sprintf("%x", sha256.Sum256([]byte(md)))

		fmt.Fprintf(&contentBuilder, "## %d. %s\n", rd.Rank, rd.Document.Title)
		fmt.Fprintf(&contentBuilder, "**Source:** %s\n", rd.Document.URL)
		fmt.Fprintf(&contentBuilder, "**Relevance:** %.2f\n\n", rd.Score)
		fmt.Fprintf(&contentBuilder, "%s\n\n---\n\n", md)

		resultMetas = append(resultMetas, ResultMeta{
			URL:          rd.Document.URL,
			Title:        rd.Document.Title,
			Score:        rd.Score,
			ScraperLevel: scraped[idx].Level,
			ContentHash:  contentHash,
		})
	}

	// Append sources footer.
	fmt.Fprintf(&contentBuilder, "**Sources:**\n")
	for i, rm := range resultMetas {
		fmt.Fprintf(&contentBuilder, "[%d] %s\n", i+1, rm.URL)
	}

	fmt.Fprintf(&contentBuilder, "\n**Metadata:**\n")
	fmt.Fprintf(&contentBuilder, "- Results returned: %d\n", len(resultMetas))
	fmt.Fprintf(&contentBuilder, "- PII mode: none (not yet enabled)\n")
	fmt.Fprintf(&contentBuilder, "- Reranker: %s\n", rerankerName)
	fmt.Fprintf(&contentBuilder, "- Search engines: %s\n", strings.Join(engines, ", "))

	elapsed := time.Since(start)

	meta := WebSearchOutput{
		Query:         input.Query,
		ResultCount:   len(resultMetas),
		SearchEngines: engines,
		PIIMode:       "none",
		PIIChecked:    false,
		RerankerUsed:  rerankerName,
		TotalDuration: elapsed.Milliseconds(),
		Results:       resultMetas,
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
