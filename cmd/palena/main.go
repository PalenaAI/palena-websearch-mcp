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
	client := search.NewSearXNGClient(cfg.Search, logger)

	// Run a hardcoded test query to prove the search + dedup pipeline works.
	query := "Go programming language concurrency"
	category := "general"
	engines := client.EnginesForCategory(category)

	logger.Info("executing test search",
		"query", query,
		"category", category,
		"engines", engines,
	)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := client.Search(ctx, search.SearchRequest{
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
	fmt.Printf("Results: %d (raw from SearXNG)\n\n", len(resp.Results))

	// Deduplicate results.
	deduped := search.Deduplicate(resp.Results)
	fmt.Printf("Results: %d (after deduplication)\n\n", len(deduped))

	// Print deduplicated results.
	for i, r := range deduped {
		fmt.Printf("[%d] %s\n", i+1, r.Title)
		fmt.Printf("    URL:     %s\n", r.URL)
		fmt.Printf("    Score:   %.2f\n", r.Score)
		fmt.Printf("    Engines: %s\n", strings.Join(r.Engines, ", "))
		if r.Snippet != "" {
			snippet := r.Snippet
			if len(snippet) > 200 {
				snippet = snippet[:200] + "..."
			}
			fmt.Printf("    Snippet: %s\n", snippet)
		}
		fmt.Println()
	}

	if len(resp.Suggestions) > 0 {
		fmt.Printf("Suggestions: %s\n", strings.Join(resp.Suggestions, ", "))
	}
}
