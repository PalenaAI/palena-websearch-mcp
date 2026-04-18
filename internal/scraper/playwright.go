// Copyright (c) 2026 bitkaio LLC. All rights reserved.
// Licensed under the Apache License, Version 2.0. See LICENSE for details.

package scraper

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"strings"
	"time"

	readability "github.com/go-shiori/go-readability"
	"github.com/playwright-community/playwright-go"

	"github.com/bitkaio/palena-websearch-mcp/internal/config"
)

// BotDetection holds signals that the page blocked automated access.
type BotDetection struct {
	Blocked    bool
	StatusCode int
	Signal     string // "cloudflare", "captcha", "403", ""
}

// L1Result holds the output of a Level 1 (Playwright) extraction.
type L1Result struct {
	Title       string
	Content     string // clean HTML from readability
	TextContent string
	Excerpt     string
	SiteName    string
	RawHTML     string
	Bot         BotDetection
}

// playwrightClient wraps a running Playwright driver + a connected remote browser
// shared by the L1 and L2 extractors. The driver is a local Node subprocess
// managed by playwright-go; the browser is the external Playwright sidecar.
type playwrightClient struct {
	pw      *playwright.Playwright
	browser playwright.Browser
}

// newPlaywrightClient starts the local Playwright driver and connects to the
// remote sidecar at endpoint. Returns nil, nil when endpoint is empty —
// callers treat that as "browser scraping disabled".
func newPlaywrightClient(endpoint string, connectTimeout time.Duration, logger *slog.Logger) (*playwrightClient, error) {
	if endpoint == "" {
		return nil, nil
	}

	pw, err := playwright.Run()
	if err != nil {
		return nil, fmt.Errorf("scraper: start playwright driver: %w", err)
	}

	connectOpts := playwright.BrowserTypeConnectOptions{}
	if connectTimeout > 0 {
		connectOpts.Timeout = playwright.Float(float64(connectTimeout.Milliseconds()))
	}

	browser, err := pw.Chromium.Connect(endpoint, connectOpts)
	if err != nil {
		_ = pw.Stop()
		return nil, fmt.Errorf("scraper: connect playwright sidecar %s: %w", endpoint, err)
	}

	logger.Info("playwright client connected", "endpoint", endpoint)
	return &playwrightClient{pw: pw, browser: browser}, nil
}

// Close disconnects from the sidecar and stops the local driver subprocess.
func (c *playwrightClient) Close() error {
	if c == nil {
		return nil
	}
	var firstErr error
	if c.browser != nil {
		if err := c.browser.Close(); err != nil {
			firstErr = err
		}
	}
	if c.pw != nil {
		if err := c.pw.Stop(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// L1Extractor renders pages via Playwright using the shared client.
// No stealth measures or proxying — the cheapest browser-based tier.
type L1Extractor struct {
	client *playwrightClient
	tabSem chan struct{} // semaphore limiting concurrent browser contexts
	logger *slog.Logger
	cfg    config.ScraperConfig
}

// NewL1Extractor creates an L1 extractor. Returns nil if client is nil
// (Playwright disabled) or configuration is incomplete.
func NewL1Extractor(cfg config.ScraperConfig, client *playwrightClient, logger *slog.Logger) *L1Extractor {
	if client == nil {
		return nil
	}
	maxTabs := cfg.Playwright.MaxTabs
	if maxTabs <= 0 {
		maxTabs = 3
	}
	return &L1Extractor{
		client: client,
		tabSem: make(chan struct{}, maxTabs),
		logger: logger,
		cfg:    cfg,
	}
}

// Extract navigates to the URL in a fresh Playwright browser context,
// waits for the page to render, and extracts content via go-readability.
// A non-zero BotDetection field indicates escalation to L2 is warranted.
func (e *L1Extractor) Extract(ctx context.Context, rawURL string) (*L1Result, error) {
	start := time.Now()

	select {
	case e.tabSem <- struct{}{}:
		defer func() { <-e.tabSem }()
	case <-ctx.Done():
		return nil, fmt.Errorf("scraper: L1 tab semaphore: %w", ctx.Err())
	}

	result, err := e.extractInContext(ctx, rawURL)

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

// extractInContext performs the actual Playwright extraction in a fresh context.
func (e *L1Extractor) extractInContext(ctx context.Context, rawURL string) (*L1Result, error) {
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("scraper: L1 parse URL: %w", err)
	}

	browserCtx, err := e.client.browser.NewContext()
	if err != nil {
		return nil, fmt.Errorf("scraper: L1 new context: %w", err)
	}
	defer func() { _ = browserCtx.Close() }()

	pageTimeout := e.cfg.Timeouts.BrowserPage
	if pageTimeout <= 0 {
		pageTimeout = 15 * time.Second
	}
	navTimeout := e.cfg.Timeouts.BrowserNav
	if navTimeout <= 0 {
		navTimeout = 30 * time.Second
	}
	browserCtx.SetDefaultTimeout(float64(pageTimeout.Milliseconds()))
	browserCtx.SetDefaultNavigationTimeout(float64(navTimeout.Milliseconds()))

	page, err := browserCtx.NewPage()
	if err != nil {
		return nil, fmt.Errorf("scraper: L1 new page: %w", err)
	}

	if _, err := page.Goto(rawURL, playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
	}); err != nil {
		return nil, fmt.Errorf("scraper: L1 goto %s: %w", rawURL, err)
	}

	// Brief settle for late JS.
	page.WaitForTimeout(500)

	html, err := page.Content()
	if err != nil {
		return nil, fmt.Errorf("scraper: L1 content for %s: %w", rawURL, err)
	}

	bot := detectBotBlock(html)

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
