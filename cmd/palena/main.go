// Copyright (c) 2026 BITKAIO LLC. All rights reserved.
// Use of this source code is governed by the AGPL-3.0 license.

package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bitkaio/palena-websearch-mcp/internal/config"
	"github.com/bitkaio/palena-websearch-mcp/internal/pii"
	"github.com/bitkaio/palena-websearch-mcp/internal/reranker"
	"github.com/bitkaio/palena-websearch-mcp/internal/scraper"
	"github.com/bitkaio/palena-websearch-mcp/internal/search"
	"github.com/bitkaio/palena-websearch-mcp/internal/transport"
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

	// Create pipeline components.
	searchClient := search.NewSearXNGClient(cfg.Search, logger)
	sc := scraper.NewScraper(cfg.Scraper, logger)

	rr, err := reranker.New(cfg.Reranker, logger)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
	logger.Info("reranker initialized", "provider", rr.Name())

	// Create PII processor.
	piiProc := pii.NewProcessor(cfg.PII, logger)
	if cfg.PII.Enabled {
		logger.Info("pii processor initialized", "mode", cfg.PII.Mode)
	} else {
		logger.Info("pii processing disabled")
	}

	// Create and start MCP server.
	srv := transport.NewServer(cfg, searchClient, sc, piiProc, rr, logger)

	// Graceful shutdown on SIGINT/SIGTERM.
	done := make(chan os.Signal, 1)
	signal.Notify(done, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		if err := srv.Start(); err != nil {
			logger.Error("server stopped", "error", err)
			os.Exit(1)
		}
	}()

	<-done
	logger.Info("shutdown signal received")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("shutdown error", "error", err)
		os.Exit(1)
	}

	logger.Info("palena stopped")
}
