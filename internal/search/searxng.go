// Copyright (c) 2026 BITKAIO LLC. All rights reserved.
// Use of this source code is governed by the AGPL-3.0 license.

package search

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/bitkaio/palena-websearch-mcp/internal/config"
)

// SearchRequest describes a query to be sent to SearXNG.
type SearchRequest struct {
	Query      string
	Engines    []string
	Categories []string
	Language   string
	TimeRange  string // day, week, month, year
	SafeSearch int    // 0=off, 1=moderate, 2=strict
	PageNo     int
	MaxResults int
}

// SearchResult is a single result returned by SearXNG.
type SearchResult struct {
	URL      string   `json:"url"`
	Title    string   `json:"title"`
	Snippet  string   `json:"content"`
	Engine   string   `json:"engine"`
	Engines  []string `json:"engines"`
	Score    float64  `json:"score"`
	Category string   `json:"category"`
}

// SearchResponse is the top-level response from SearXNG's /search?format=json.
type SearchResponse struct {
	Query           string         `json:"query"`
	Results         []SearchResult `json:"results"`
	Suggestions     []string       `json:"suggestions"`
	NumberOfResults int            `json:"number_of_results"`
}

// SearXNGClient queries a SearXNG instance over HTTP.
type SearXNGClient struct {
	baseURL    string
	httpClient *http.Client
	logger     *slog.Logger
	cfg        config.SearchConfig
}

// NewSearXNGClient creates a client for the given SearXNG base URL.
func NewSearXNGClient(cfg config.SearchConfig, logger *slog.Logger) *SearXNGClient {
	return &SearXNGClient{
		baseURL: strings.TrimRight(cfg.SearXNGURL, "/"),
		httpClient: &http.Client{
			Timeout: cfg.Timeout,
		},
		logger: logger,
		cfg:    cfg,
	}
}

// Search executes a search request against SearXNG and returns the parsed response.
func (c *SearXNGClient) Search(ctx context.Context, req SearchRequest) (*SearchResponse, error) {
	start := time.Now()

	u, err := c.buildURL(req)
	if err != nil {
		return nil, fmt.Errorf("search: build URL: %w", err)
	}

	c.logger.DebugContext(ctx, "searxng request", "url", u)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("search: create request: %w", err)
	}
	httpReq.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("search: execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("search: SearXNG returned status %d", resp.StatusCode)
	}

	var searchResp SearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&searchResp); err != nil {
		return nil, fmt.Errorf("search: decode response: %w", err)
	}

	elapsed := time.Since(start)
	c.logger.InfoContext(ctx, "searxng search completed",
		"query", req.Query,
		"results", len(searchResp.Results),
		"duration_ms", elapsed.Milliseconds(),
	)

	// Trim to MaxResults if SearXNG returned more than requested.
	max := req.MaxResults
	if max <= 0 {
		max = c.cfg.MaxResults
	}
	if len(searchResp.Results) > max {
		searchResp.Results = searchResp.Results[:max]
	}

	return &searchResp, nil
}

// EnginesForCategory returns the configured engine list for a category,
// falling back to defaultEngines if no route is configured.
func (c *SearXNGClient) EnginesForCategory(category string) []string {
	if category == "" {
		category = "general"
	}
	if engines, ok := c.cfg.EngineRoutes[category]; ok {
		return engines
	}
	return c.cfg.DefaultEngines
}

// buildURL constructs the SearXNG /search query string.
func (c *SearXNGClient) buildURL(req SearchRequest) (string, error) {
	base, err := url.Parse(c.baseURL + "/search")
	if err != nil {
		return "", err
	}

	q := base.Query()
	q.Set("q", req.Query)
	q.Set("format", "json")

	if len(req.Engines) > 0 {
		q.Set("engines", strings.Join(req.Engines, ","))
	}
	if len(req.Categories) > 0 {
		q.Set("categories", strings.Join(req.Categories, ","))
	}

	lang := req.Language
	if lang == "" {
		lang = c.cfg.DefaultLanguage
	}
	q.Set("language", lang)

	q.Set("safesearch", strconv.Itoa(req.SafeSearch))

	if req.TimeRange != "" {
		q.Set("time_range", req.TimeRange)
	}

	pageNo := req.PageNo
	if pageNo < 1 {
		pageNo = 1
	}
	q.Set("pageno", strconv.Itoa(pageNo))

	base.RawQuery = q.Encode()
	return base.String(), nil
}
