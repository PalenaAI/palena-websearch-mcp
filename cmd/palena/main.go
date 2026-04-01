// Copyright (c) 2026 BITKAIO LLC. All rights reserved.
// Use of this source code is governed by the AGPL-3.0 license.

package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/bitkaio/palena-websearch-mcp/internal/config"
	"github.com/bitkaio/palena-websearch-mcp/internal/output"
	"github.com/bitkaio/palena-websearch-mcp/internal/scraper"
	"github.com/bitkaio/palena-websearch-mcp/internal/search"
)

func main() {
	// Load configuration.
	cfgPath := config.ConfigPath("./config/palena.yaml")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}

	// Set up structured logger.
	var handler slog.Handler
	opts := &slog.HandlerOptions{}
	switch cfg.Logging.Level {
	case "debug":
		opts.Level = slog.LevelDebug
	case "warn":
		opts.Level = slog.LevelWarn
	case "error":
		opts.Level = slog.LevelError
	default:
		opts.Level = slog.LevelInfo
	}

	if cfg.Logging.Format == "text" {
		handler = slog.NewTextHandler(os.Stderr, opts)
	} else {
		handler = slog.NewJSONHandler(os.Stderr, opts)
	}
	logger := slog.New(handler)

	logger.Info("palena starting",
		"config", cfgPath,
		"searxng_url", cfg.Search.SearXNGURL,
	)

	// Create the SearXNG search client.
	searchClient := search.NewSearXNGClient(cfg.Search, logger)

	// Run a hardcoded test query.
	query := "Go programming language concurrency"
	category := "general"
	engines := searchClient.EnginesForCategory(category)

	logger.Info("executing test search",
		"query", query,
		"category", category,
		"engines", engines,
	)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	resp, err := searchClient.Search(ctx, search.SearchRequest{
		Query:      query,
		Engines:    engines,
		Categories: []string{category},
		Language:   cfg.Search.DefaultLanguage,
		SafeSearch: cfg.Search.SafeSearch,
		MaxResults: cfg.Search.MaxResults,
	})
	if err != nil {
		logger.Error("search failed", "error", err)
		os.Exit(1)
	}

	fmt.Printf("Query:   %s\n", resp.Query)
	fmt.Printf("Results: %d (raw from SearXNG)\n", len(resp.Results))

	// Deduplicate results.
	deduped := search.Deduplicate(resp.Results)
	fmt.Printf("Results: %d (after deduplication)\n\n", len(deduped))

	// Take top 3 URLs for scraping.
	scrapeLimit := 3
	if len(deduped) < scrapeLimit {
		scrapeLimit = len(deduped)
	}

	urls := make([]string, scrapeLimit)
	for i := 0; i < scrapeLimit; i++ {
		urls[i] = deduped[i].URL
	}

	fmt.Printf("Scraping top %d URLs with L0 (HTTP + readability)...\n\n", scrapeLimit)

	// Scrape using L0 (HTTP + go-readability).
	sc := scraper.NewScraper(cfg.Scraper, logger)
	results := sc.ScrapeAll(ctx, urls)

	// Convert to markdown and print.
	for i, r := range results {
		fmt.Printf("═══════════════════════════════════════════════════════════\n")
		fmt.Printf("[%d] %s\n", i+1, r.Title)
		fmt.Printf("    URL: %s\n", r.URL)

		if r.Err != nil {
			fmt.Printf("    ERROR: %v\n\n", r.Err)
			continue
		}

		if r.NeedsJS {
			fmt.Printf("    ⚠ Content may be incomplete (page needs JavaScript)\n")
		}

		// Convert clean HTML to markdown.
		md, err := output.HTMLToMarkdown(r.Content)
		if err != nil {
			fmt.Printf("    ERROR converting to markdown: %v\n\n", err)
			continue
		}

		if md == "" {
			fmt.Printf("    (no readable content extracted)\n\n")
			continue
		}

		// Truncate for demo output.
		const maxDisplay = 2000
		display := md
		if len(display) > maxDisplay {
			display = display[:maxDisplay] + "\n\n... [truncated]"
		}

		fmt.Printf("    Engines: %s\n\n", strings.Join(deduped[i].Engines, ", "))
		fmt.Println(display)
		fmt.Println()
	}
}
