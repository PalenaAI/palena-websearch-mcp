// Copyright (c) 2026 BITKAIO LLC. All rights reserved.
// Use of this source code is governed by the AGPL-3.0 license.

package scraper

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
	readability "github.com/go-shiori/go-readability"

	"github.com/bitkaio/palena-websearch-mcp/internal/config"
)

// BotDetection holds signals that the page blocked automated access.
type BotDetection struct {
	Blocked    bool
	StatusCode int
	Signal     string // "cloudflare", "captcha", "403", ""
}

// L1Result holds the output of a Level 1 (headless Chromium) extraction.
type L1Result struct {
	Title       string
	Content     string // clean HTML from readability
	TextContent string
	Excerpt     string
	SiteName    string
	RawHTML     string
	Bot         BotDetection
}

// L1Extractor renders pages via headless Chromium using Chrome DevTools Protocol.
// It connects to an external Chromium container via WebSocket.
type L1Extractor struct {
	endpoint string
	tabSem   chan struct{} // semaphore limiting concurrent tabs
	logger   *slog.Logger
	cfg      config.ScraperConfig
}

// NewL1Extractor creates an L1 extractor. Returns nil if no CDP endpoint is configured.
func NewL1Extractor(cfg config.ScraperConfig, logger *slog.Logger) *L1Extractor {
	if cfg.ChromiumCDP.Endpoint == "" {
		return nil
	}

	maxTabs := cfg.ChromiumCDP.MaxTabs
	if maxTabs <= 0 {
		maxTabs = 3
	}

	return &L1Extractor{
		endpoint: cfg.ChromiumCDP.Endpoint,
		tabSem:   make(chan struct{}, maxTabs),
		logger:   logger,
		cfg:      cfg,
	}
}

// Extract navigates to the URL in a headless Chromium tab, waits for the page
// to render, and extracts content via go-readability. The returned BotDetection
// field indicates whether escalation to L2 is warranted.
func (e *L1Extractor) Extract(ctx context.Context, rawURL string) (*L1Result, error) {
	start := time.Now()

	// Acquire tab semaphore.
	select {
	case e.tabSem <- struct{}{}:
		defer func() { <-e.tabSem }()
	case <-ctx.Done():
		return nil, fmt.Errorf("scraper: L1 tab semaphore: %w", ctx.Err())
	}

	result, err := e.extractInTab(ctx, rawURL)

	elapsed := time.Since(start)
	if err != nil {
		e.logger.WarnContext(ctx, "L1 extraction failed",
			"url", rawURL,
			"duration_ms", elapsed.Milliseconds(),
			"error", err,
		)
		return nil, err
	}

	e.logger.InfoContext(ctx, "L1 extraction complete",
		"url", rawURL,
		"title", result.Title,
		"text_length", len(result.TextContent),
		"bot_blocked", result.Bot.Blocked,
		"bot_signal", result.Bot.Signal,
		"duration_ms", elapsed.Milliseconds(),
	)

	return result, nil
}

// extractInTab performs the actual CDP extraction in a single browser tab.
func (e *L1Extractor) extractInTab(ctx context.Context, rawURL string) (*L1Result, error) {
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("scraper: L1 parse URL: %w", err)
	}

	// Connect to the remote Chromium instance.
	allocCtx, allocCancel := chromedp.NewRemoteAllocator(ctx, e.endpoint)
	defer allocCancel()

	// Create a new browser context (tab) with per-page timeout.
	pageTimeout := e.cfg.Timeouts.BrowserPage
	if pageTimeout <= 0 {
		pageTimeout = 15 * time.Second
	}
	tabCtx, tabCancel := context.WithTimeout(allocCtx, pageTimeout)
	defer tabCancel()

	tabCtx, tabCancel2 := chromedp.NewContext(tabCtx)
	defer tabCancel2()

	var html string
	err = chromedp.Run(tabCtx,
		chromedp.Navigate(rawURL),
		chromedp.WaitReady("body"),
		chromedp.Sleep(500*time.Millisecond), // brief settle for late JS
		chromedp.OuterHTML("html", &html),
	)
	if err != nil {
		return nil, fmt.Errorf("scraper: L1 chromedp for %s: %w", rawURL, err)
	}

	// Check for bot detection before running readability.
	bot := detectBotBlock(html)

	// Run readability on the rendered HTML.
	article, err := readability.FromReader(strings.NewReader(html), parsedURL)
	if err != nil {
		return nil, fmt.Errorf("scraper: L1 readability for %s: %w", rawURL, err)
	}

	return &L1Result{
		Title:       article.Title,
		Content:     article.Content,
		TextContent: article.TextContent,
		Excerpt:     article.Excerpt,
		SiteName:    article.SiteName,
		RawHTML:     html,
		Bot:         bot,
	}, nil
}

var (
	cloudflareRe = regexp.MustCompile(`(?i)(cloudflare|cf-chl-bypass|ray\s*id|checking your browser)`)
	captchaRe    = regexp.MustCompile(`(?i)(captcha|recaptcha|hcaptcha|challenge-form)`)
	forbiddenRe  = regexp.MustCompile(`(?i)<title>\s*(403|forbidden|access denied|blocked)\s*</title>`)
)

// detectBotBlock checks rendered HTML for common bot-blocking signals.
func detectBotBlock(html string) BotDetection {
	if forbiddenRe.MatchString(html) {
		return BotDetection{Blocked: true, StatusCode: 403, Signal: "403"}
	}
	if cloudflareRe.MatchString(html) {
		return BotDetection{Blocked: true, Signal: "cloudflare"}
	}
	if captchaRe.MatchString(html) {
		return BotDetection{Blocked: true, Signal: "captcha"}
	}
	return BotDetection{}
}
