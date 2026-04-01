// Copyright (c) 2026 BITKAIO LLC. All rights reserved.
// Use of this source code is governed by the AGPL-3.0 license.

package scraper

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strings"
	"time"

	readability "github.com/go-shiori/go-readability"

	"github.com/bitkaio/palena-websearch-mcp/internal/config"
)

// userAgents is a small pool of realistic browser User-Agent strings
// rotated per request.
var userAgents = []string{
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
	"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.2 Safari/605.1.15",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:133.0) Gecko/20100101 Firefox/133.0",
}

// L0Result holds the output of a successful L0 (HTTP + readability) extraction.
type L0Result struct {
	Title       string
	Content     string // clean HTML from readability
	TextContent string // plain text from readability
	Excerpt     string
	SiteName    string
	RawHTML     string // original HTML before readability
	Assessment  ContentAssessment
}

// L0Extractor performs Level-0 extraction: plain HTTP GET + go-readability.
type L0Extractor struct {
	httpClient *http.Client
	logger     *slog.Logger
	cfg        config.ScraperConfig
}

// NewL0Extractor creates an L0 extractor with the given config.
func NewL0Extractor(cfg config.ScraperConfig, logger *slog.Logger) *L0Extractor {
	return &L0Extractor{
		httpClient: &http.Client{
			Timeout: cfg.Timeouts.HTTPGet,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 3 {
					return fmt.Errorf("scraper: too many redirects (max 3)")
				}
				return nil
			},
		},
		logger: logger,
		cfg:    cfg,
	}
}

// Extract fetches the URL via HTTP GET, runs content detection, and if content
// looks sufficient, extracts the main article with go-readability.
// Returns the result and whether escalation to L1 is recommended.
func (e *L0Extractor) Extract(ctx context.Context, rawURL string) (*L0Result, error) {
	start := time.Now()

	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("scraper: parse URL: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("scraper: create request: %w", err)
	}
	req.Header.Set("User-Agent", randomUA())
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("scraper: fetch %s: %w", rawURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("scraper: %s returned status %d", rawURL, resp.StatusCode)
	}

	// Read entire body (bounded to 5 MB to avoid decompression bombs).
	const maxBody = 5 * 1024 * 1024
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("scraper: read body: %w", err)
	}
	rawHTML := string(body)

	// Run content detection heuristics.
	assessment := AssessContent(rawHTML, e.cfg.ContentDetection)

	e.logger.DebugContext(ctx, "L0 content assessment",
		"url", rawURL,
		"text_length", assessment.TextLength,
		"text_ratio", fmt.Sprintf("%.3f", assessment.TextToMarkupRatio),
		"script_tags", assessment.ScriptTagCount,
		"needs_js", assessment.NeedsJavaScript,
		"ssr_framework", assessment.SSRFramework,
	)

	// Run readability even if content seems thin — caller decides whether
	// to use the result or escalate based on assessment.NeedsJavaScript.
	article, err := readability.FromReader(strings.NewReader(rawHTML), parsedURL)
	if err != nil {
		return nil, fmt.Errorf("scraper: readability extraction for %s: %w", rawURL, err)
	}

	elapsed := time.Since(start)
	e.logger.InfoContext(ctx, "L0 extraction complete",
		"url", rawURL,
		"title", article.Title,
		"text_length", len(article.TextContent),
		"needs_js", assessment.NeedsJavaScript,
		"duration_ms", elapsed.Milliseconds(),
	)

	return &L0Result{
		Title:       article.Title,
		Content:     article.Content,
		TextContent: article.TextContent,
		Excerpt:     article.Excerpt,
		SiteName:    article.SiteName,
		RawHTML:     rawHTML,
		Assessment:  assessment,
	}, nil
}

func randomUA() string {
	return userAgents[rand.IntN(len(userAgents))]
}
