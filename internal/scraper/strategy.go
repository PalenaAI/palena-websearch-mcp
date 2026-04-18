// Copyright (c) 2026 bitkaio LLC. All rights reserved.
// Licensed under the Apache License, Version 2.0. See LICENSE for details.

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
type Scraper struct {
	l0     *L0Extractor
	l1     *L1Extractor // nil if Playwright sidecar is not configured
	l2     *L2Extractor // nil if stealth is not configured/enabled
	pw     *playwrightClient
	logger *slog.Logger
	cfg    config.ScraperConfig
}

// NewScraper creates a scraper with the configured extraction levels.
// L1 and L2 are optional — if the Playwright sidecar endpoint is empty,
// they are nil and URLs that fail L0 are reported as scrape failures.
// Returns an error if the Playwright driver fails to start or connect.
func NewScraper(cfg config.ScraperConfig, logger *slog.Logger) (*Scraper, error) {
	pw, err := newPlaywrightClient(cfg.Playwright.Endpoint, cfg.Timeouts.BrowserNav, logger)
	if err != nil {
		return nil, err
	}

	proxyPool := NewProxyPool(cfg.Proxy, logger)

	return &Scraper{
		l0:     NewL0Extractor(cfg, logger),
		l1:     NewL1Extractor(cfg, pw, logger),
		l2:     NewL2Extractor(cfg, pw, proxyPool, logger),
		pw:     pw,
		logger: logger,
		cfg:    cfg,
	}, nil
}

// Close releases the Playwright driver and disconnects from the sidecar.
// Safe to call when Playwright was never configured.
func (s *Scraper) Close() error {
	if s == nil || s.pw == nil {
		return nil
	}
	return s.pw.Close()
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

// scrapeOne runs the tiered extraction strategy for a single URL:
//
//	L0 (HTTP+readability)
//	  → if NeedsJavaScript → L1 (Playwright headless)
//	    → if bot-blocked → L2 (Playwright stealth + proxy)
//	      → if still blocked → return error
//
// If the Playwright sidecar is unavailable, L1/L2 are skipped and URLs
// that need JS rendering are reported as failures.
func (s *Scraper) scrapeOne(ctx context.Context, rawURL string) ScrapeResult {
	// --- Level 0 ---
	result := ScrapeResult{URL: rawURL, Level: 0}

	l0, err := s.l0.Extract(ctx, rawURL)
	if err != nil {
		result.Err = fmt.Errorf("scraper: L0 failed for %s: %w", rawURL, err)
		s.logger.WarnContext(ctx, "L0 extraction failed", "url", rawURL, "error", err)
		// L0 failed entirely (network error, bad status, etc.).
		// Still attempt L1 if available — the page might load in a browser.
		return s.tryEscalateFromL0Failure(ctx, result)
	}

	result.Title = l0.Title
	result.Content = l0.Content
	result.TextContent = l0.TextContent
	result.Excerpt = l0.Excerpt
	result.SiteName = l0.SiteName
	result.NeedsJS = l0.Assessment.NeedsJavaScript

	if !l0.Assessment.NeedsJavaScript {
		return result
	}

	// --- Escalate to Level 1 ---
	s.logger.InfoContext(ctx, "L0 content thin, escalating to L1",
		"url", rawURL,
		"text_length", l0.Assessment.TextLength,
	)

	return s.tryL1(ctx, rawURL, result)
}

// tryEscalateFromL0Failure attempts L1 when L0 fails entirely (e.g. HTTP
// error), since the page might work in a browser context.
func (s *Scraper) tryEscalateFromL0Failure(ctx context.Context, result ScrapeResult) ScrapeResult {
	if s.l1 == nil {
		return result // no Playwright sidecar → keep original L0 error
	}

	s.logger.InfoContext(ctx, "L0 failed, attempting L1 as fallback",
		"url", result.URL,
	)

	l1Result := s.tryL1(ctx, result.URL, result)
	if l1Result.Err != nil {
		// Preserve the original L0 error context.
		l1Result.Err = fmt.Errorf("scraper: L0 and L1 both failed for %s: L0: %w; L1: %v",
			result.URL, result.Err, l1Result.Err)
	}
	return l1Result
}

// tryL1 attempts Level 1 extraction via Playwright headless browser.
func (s *Scraper) tryL1(ctx context.Context, rawURL string, fallback ScrapeResult) ScrapeResult {
	if s.l1 == nil {
		s.logger.WarnContext(ctx, "L1 unavailable (no Playwright endpoint), returning L0 result",
			"url", rawURL,
		)
		return fallback
	}

	l1, err := s.l1.Extract(ctx, rawURL)
	if err != nil {
		s.logger.WarnContext(ctx, "L1 extraction failed",
			"url", rawURL,
			"error", err,
		)
		fallback.Err = fmt.Errorf("scraper: L1 failed for %s: %w", rawURL, err)
		return fallback
	}

	result := ScrapeResult{
		URL:         rawURL,
		Title:       l1.Title,
		Content:     l1.Content,
		TextContent: l1.TextContent,
		Excerpt:     l1.Excerpt,
		SiteName:    l1.SiteName,
		Level:       1,
		NeedsJS:     true,
	}

	// Check if bot-blocked → escalate to L2.
	if l1.Bot.Blocked {
		s.logger.InfoContext(ctx, "L1 bot-blocked, escalating to L2",
			"url", rawURL,
			"signal", l1.Bot.Signal,
		)
		return s.tryL2(ctx, rawURL, result)
	}

	return result
}

// tryL2 attempts Level 2 extraction with stealth measures and proxy rotation.
func (s *Scraper) tryL2(ctx context.Context, rawURL string, fallback ScrapeResult) ScrapeResult {
	if s.l2 == nil {
		s.logger.WarnContext(ctx, "L2 unavailable (stealth not enabled), returning L1 result",
			"url", rawURL,
		)
		fallback.Err = fmt.Errorf("scraper: bot-blocked at L1, L2 stealth not available for %s", rawURL)
		return fallback
	}

	l2, err := s.l2.Extract(ctx, rawURL)
	if err != nil {
		s.logger.WarnContext(ctx, "L2 stealth extraction failed",
			"url", rawURL,
			"error", err,
		)
		fallback.Err = fmt.Errorf("scraper: L2 failed for %s: %w", rawURL, err)
		return fallback
	}

	result := ScrapeResult{
		URL:         rawURL,
		Title:       l2.Title,
		Content:     l2.Content,
		TextContent: l2.TextContent,
		Excerpt:     l2.Excerpt,
		SiteName:    l2.SiteName,
		Level:       2,
		NeedsJS:     true,
	}

	// If still bot-blocked after stealth, report as error.
	if l2.Bot.Blocked {
		s.logger.WarnContext(ctx, "still bot-blocked after L2 stealth",
			"url", rawURL,
			"signal", l2.Bot.Signal,
		)
		result.Err = fmt.Errorf("scraper: bot-blocked at all levels for %s (signal: %s)", rawURL, l2.Bot.Signal)
	}

	return result
}
