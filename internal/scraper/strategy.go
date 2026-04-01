// Copyright (c) 2026 BITKAIO LLC. All rights reserved.
// Use of this source code is governed by the AGPL-3.0 license.

package scraper

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/bitkaio/palena-websearch-mcp/internal/config"
)

// ScrapeResult holds the extracted content for a single URL.
type ScrapeResult struct {
	URL         string
	Title       string
	Content     string // clean HTML from readability
	TextContent string // plain text
	Excerpt     string
	SiteName    string
	Level       int  // extraction level used (0, 1, 2)
	NeedsJS     bool // true if L0 detected that JS rendering is needed
	Err         error
}

// Scraper orchestrates tiered extraction across a set of URLs.
// Currently only L0 is implemented; L1/L2 will be added later.
type Scraper struct {
	l0     *L0Extractor
	logger *slog.Logger
	cfg    config.ScraperConfig
}

// NewScraper creates a scraper with the configured extraction levels.
func NewScraper(cfg config.ScraperConfig, logger *slog.Logger) *Scraper {
	return &Scraper{
		l0:     NewL0Extractor(cfg, logger),
		logger: logger,
		cfg:    cfg,
	}
}

// ScrapeAll extracts content from all provided URLs concurrently, respecting
// MaxConcurrency. Results are returned in the same order as the input URLs.
func (s *Scraper) ScrapeAll(ctx context.Context, urls []string) []ScrapeResult {
	results := make([]ScrapeResult, len(urls))

	concurrency := s.cfg.MaxConcurrency
	if concurrency <= 0 {
		concurrency = 5
	}
	sem := make(chan struct{}, concurrency)

	var wg sync.WaitGroup
	for i, u := range urls {
		wg.Add(1)
		go func(idx int, rawURL string) {
			defer wg.Done()

			sem <- struct{}{}        // acquire
			defer func() { <-sem }() // release

			results[idx] = s.scrapeOne(ctx, rawURL)
		}(i, u)
	}
	wg.Wait()

	return results
}

// scrapeOne runs the tiered extraction strategy for a single URL.
// Currently L0-only; L1/L2 escalation is stubbed.
func (s *Scraper) scrapeOne(ctx context.Context, rawURL string) ScrapeResult {
	result := ScrapeResult{URL: rawURL, Level: 0}

	l0, err := s.l0.Extract(ctx, rawURL)
	if err != nil {
		result.Err = fmt.Errorf("scraper: L0 failed for %s: %w", rawURL, err)
		s.logger.WarnContext(ctx, "L0 extraction failed", "url", rawURL, "error", err)
		return result
	}

	result.Title = l0.Title
	result.Content = l0.Content
	result.TextContent = l0.TextContent
	result.Excerpt = l0.Excerpt
	result.SiteName = l0.SiteName
	result.NeedsJS = l0.Assessment.NeedsJavaScript

	if l0.Assessment.NeedsJavaScript {
		s.logger.InfoContext(ctx, "L0 content thin, L1 escalation needed (not yet implemented)",
			"url", rawURL,
			"text_length", l0.Assessment.TextLength,
		)
		// TODO: escalate to L1 (chromedp) when implemented.
		// For now, return what L0 produced.
	}

	return result
}
