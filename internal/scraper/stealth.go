// Copyright (c) 2026 bitkaio LLC. All rights reserved.
// Licensed under the Apache License, Version 2.0. See LICENSE for details.

package scraper

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/url"
	"strings"
	"time"

	readability "github.com/go-shiori/go-readability"
	"github.com/playwright-community/playwright-go"

	"github.com/bitkaio/palena-websearch-mcp/internal/config"
)

// L2Extractor applies stealth anti-detection measures and optional proxy
// rotation on top of Playwright browser extraction.
type L2Extractor struct {
	client    *playwrightClient
	tabSem    chan struct{}
	proxyPool *ProxyPool
	logger    *slog.Logger
	cfg       config.ScraperConfig
}

// NewL2Extractor creates an L2 extractor. Returns nil if the Playwright
// client is nil or stealth is disabled.
func NewL2Extractor(cfg config.ScraperConfig, client *playwrightClient, proxyPool *ProxyPool, logger *slog.Logger) *L2Extractor {
	if client == nil || !cfg.Stealth.Enabled {
		return nil
	}

	maxTabs := cfg.Playwright.MaxTabs
	if maxTabs <= 0 {
		maxTabs = 3
	}

	return &L2Extractor{
		client:    client,
		tabSem:    make(chan struct{}, maxTabs),
		proxyPool: proxyPool,
		logger:    logger,
		cfg:       cfg,
	}
}

// Extract navigates to the URL with stealth measures and optional proxy rotation.
func (e *L2Extractor) Extract(ctx context.Context, rawURL string) (*L1Result, error) {
	start := time.Now()

	select {
	case e.tabSem <- struct{}{}:
		defer func() { <-e.tabSem }()
	case <-ctx.Done():
		return nil, fmt.Errorf("scraper: L2 tab semaphore: %w", ctx.Err())
	}

	proxyURL := ""
	if e.proxyPool != nil {
		proxyURL = e.proxyPool.Next()
	}

	result, err := e.extractStealth(ctx, rawURL, proxyURL)

	elapsed := time.Since(start)
	if err != nil {
		if proxyURL != "" {
			e.proxyPool.MarkFailed(proxyURL)
		}
		e.logger.WarnContext(ctx, "L2 stealth extraction failed",
			"url", rawURL,
			"proxy", proxyURL != "",
			"duration_ms", elapsed.Milliseconds(),
			"error", err,
		)
		return nil, err
	}

	e.logger.InfoContext(ctx, "L2 stealth extraction complete",
		"url", rawURL,
		"title", result.Title,
		"text_length", len(result.TextContent),
		"proxy", proxyURL != "",
		"bot_blocked", result.Bot.Blocked,
		"duration_ms", elapsed.Milliseconds(),
	)

	return result, nil
}

// extractStealth performs Playwright extraction with anti-detection measures.
func (e *L2Extractor) extractStealth(ctx context.Context, rawURL, proxyURL string) (*L1Result, error) {
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("scraper: L2 parse URL: %w", err)
	}

	vp := randomViewport()
	ua := randomStealthUA(vp)

	contextOpts := playwright.BrowserNewContextOptions{
		UserAgent: playwright.String(ua),
		Viewport: &playwright.Size{
			Width:  vp.width,
			Height: vp.height,
		},
		Locale: playwright.String("en-US"),
	}
	if proxyURL != "" {
		contextOpts.Proxy = &playwright.Proxy{Server: proxyURL}
	}

	browserCtx, err := e.client.browser.NewContext(contextOpts)
	if err != nil {
		return nil, fmt.Errorf("scraper: L2 new context: %w", err)
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

	// Inject stealth patches before any page JS runs.
	if err := browserCtx.AddInitScript(playwright.Script{
		Content: playwright.String(buildStealthScript()),
	}); err != nil {
		return nil, fmt.Errorf("scraper: L2 add init script: %w", err)
	}

	page, err := browserCtx.NewPage()
	if err != nil {
		return nil, fmt.Errorf("scraper: L2 new page: %w", err)
	}

	if _, err := page.Goto(rawURL, playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
	}); err != nil {
		return nil, fmt.Errorf("scraper: L2 goto %s: %w", rawURL, err)
	}

	// Longer settle for stealth to let lazy-loaded JS complete.
	page.WaitForTimeout(800)

	html, err := page.Content()
	if err != nil {
		return nil, fmt.Errorf("scraper: L2 content for %s: %w", rawURL, err)
	}

	bot := detectBotBlock(html)

	article, err := readability.FromReader(strings.NewReader(html), parsedURL)
	if err != nil {
		return nil, fmt.Errorf("scraper: L2 readability for %s: %w", rawURL, err)
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

type viewport struct {
	width  int
	height int
}

// randomViewport generates realistic viewport dimensions within common ranges.
func randomViewport() viewport {
	widths := []int{1280, 1366, 1440, 1536, 1600, 1920}
	heights := []int{720, 768, 800, 864, 900, 1080}

	return viewport{
		width:  widths[rand.IntN(len(widths))],
		height: heights[rand.IntN(len(heights))],
	}
}

// stealthUAs is a pool of plausible UA strings rotated per request.
var stealthUAs = []string{
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
	"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:133.0) Gecko/20100101 Firefox/133.0",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:133.0) Gecko/20100101 Firefox/133.0",
}

func randomStealthUA(_ viewport) string {
	return stealthUAs[rand.IntN(len(stealthUAs))]
}

// buildStealthScript returns JavaScript that patches common automation
// detection vectors. Injected via BrowserContext.AddInitScript so it runs
// before any page scripts.
func buildStealthScript() string {
	return `
// Override navigator.webdriver to false.
Object.defineProperty(navigator, 'webdriver', {
  get: () => false,
  configurable: true,
});

// Provide a realistic navigator.platform.
Object.defineProperty(navigator, 'platform', {
  get: () => 'Win32',
  configurable: true,
});

// Provide a realistic navigator.vendor.
Object.defineProperty(navigator, 'vendor', {
  get: () => 'Google Inc.',
  configurable: true,
});

// Inject a minimal window.chrome object.
if (!window.chrome) {
  window.chrome = {
    runtime: {
      connect: function() {},
      sendMessage: function() {},
    },
    loadTimes: function() { return {}; },
    csi: function() { return {}; },
  };
}

// Override navigator.plugins to look non-empty.
Object.defineProperty(navigator, 'plugins', {
  get: () => [1, 2, 3, 4, 5],
  configurable: true,
});

// Override navigator.languages.
Object.defineProperty(navigator, 'languages', {
  get: () => ['en-US', 'en'],
  configurable: true,
});

// Patch permissions query to match real browser behavior for notifications.
const originalQuery = window.navigator.permissions.query;
window.navigator.permissions.query = (parameters) =>
  parameters.name === 'notifications'
    ? Promise.resolve({ state: Notification.permission })
    : originalQuery(parameters);
`
}
