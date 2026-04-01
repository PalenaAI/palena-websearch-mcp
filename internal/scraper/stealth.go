// Copyright (c) 2026 BITKAIO LLC. All rights reserved.
// Use of this source code is governed by the AGPL-3.0 license.

package scraper

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/url"
	"strings"
	"time"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	readability "github.com/go-shiori/go-readability"

	"github.com/bitkaio/palena-websearch-mcp/internal/config"
)

// L2Extractor applies stealth anti-detection measures and optional proxy
// rotation on top of headless Chromium extraction.
type L2Extractor struct {
	endpoint  string
	tabSem    chan struct{} // shared with L1 or separate — strategy decides
	proxyPool *ProxyPool
	logger    *slog.Logger
	cfg       config.ScraperConfig
}

// NewL2Extractor creates an L2 extractor. Returns nil if no CDP endpoint
// is configured or stealth is disabled.
func NewL2Extractor(cfg config.ScraperConfig, proxyPool *ProxyPool, logger *slog.Logger) *L2Extractor {
	if cfg.ChromiumCDP.Endpoint == "" || !cfg.Stealth.Enabled {
		return nil
	}

	maxTabs := cfg.ChromiumCDP.MaxTabs
	if maxTabs <= 0 {
		maxTabs = 3
	}

	return &L2Extractor{
		endpoint:  cfg.ChromiumCDP.Endpoint,
		tabSem:    make(chan struct{}, maxTabs),
		proxyPool: proxyPool,
		logger:    logger,
		cfg:       cfg,
	}
}

// Extract navigates to the URL with stealth measures and optional proxy rotation.
func (e *L2Extractor) Extract(ctx context.Context, rawURL string) (*L1Result, error) {
	start := time.Now()

	// Acquire tab semaphore.
	select {
	case e.tabSem <- struct{}{}:
		defer func() { <-e.tabSem }()
	case <-ctx.Done():
		return nil, fmt.Errorf("scraper: L2 tab semaphore: %w", ctx.Err())
	}

	// Select proxy (if available).
	proxyURL := ""
	if e.proxyPool != nil {
		proxyURL = e.proxyPool.Next()
	}

	result, err := e.extractStealth(ctx, rawURL, proxyURL)

	elapsed := time.Since(start)
	if err != nil {
		// Mark proxy as failed if one was used.
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

// extractStealth performs CDP extraction with anti-detection measures.
func (e *L2Extractor) extractStealth(ctx context.Context, rawURL, proxyURL string) (*L1Result, error) {
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("scraper: L2 parse URL: %w", err)
	}

	// Connect to the remote Chromium instance.
	allocCtx, allocCancel := chromedp.NewRemoteAllocator(ctx, e.endpoint)
	defer allocCancel()

	pageTimeout := e.cfg.Timeouts.BrowserPage
	if pageTimeout <= 0 {
		pageTimeout = 15 * time.Second
	}
	tabCtx, tabCancel := context.WithTimeout(allocCtx, pageTimeout)
	defer tabCancel()

	tabCtx, tabCancel2 := chromedp.NewContext(tabCtx)
	defer tabCancel2()

	// Generate randomized fingerprint.
	vp := randomViewport()
	ua := randomStealthUA(vp)

	// Build stealth action sequence.
	actions := []chromedp.Action{
		// Set viewport dimensions.
		emulation.SetDeviceMetricsOverride(int64(vp.width), int64(vp.height), 1.0, false),

		// Set user agent.
		emulation.SetUserAgentOverride(ua),

		// Inject stealth scripts before any page JS runs.
		chromedp.ActionFunc(func(ctx context.Context) error {
			script := buildStealthScript()
			_, err := page.AddScriptToEvaluateOnNewDocument(script).Do(ctx)
			return err
		}),
	}

	// Navigate and extract.
	var html string
	actions = append(actions,
		chromedp.Navigate(rawURL),
		chromedp.WaitReady("body"),
		chromedp.Sleep(800*time.Millisecond), // longer settle for stealth
		chromedp.OuterHTML("html", &html),
	)

	if err := chromedp.Run(tabCtx, actions...); err != nil {
		return nil, fmt.Errorf("scraper: L2 chromedp for %s: %w", rawURL, err)
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

// stealthUAs maps viewport widths to plausible UA strings.
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
// detection vectors. Injected via page.AddScriptToEvaluateOnNewDocument
// so it runs before any page scripts.
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

// Patch permissions query to deny 'notifications' (common bot test).
const originalQuery = window.navigator.permissions.query;
window.navigator.permissions.query = (parameters) =>
  parameters.name === 'notifications'
    ? Promise.resolve({ state: Notification.permission })
    : originalQuery(parameters);
`
}
