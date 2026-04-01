// Copyright (c) 2026 BITKAIO LLC. All rights reserved.
// Use of this source code is governed by the AGPL-3.0 license.

package reranker

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/bitkaio/palena-websearch-mcp/internal/config"
)

// Reranker re-scores documents by semantic relevance to a query.
type Reranker interface {
	// Rerank scores and sorts documents by relevance to the query.
	// Returns top K results sorted by score descending.
	Rerank(ctx context.Context, query string, documents []Document) ([]RankedDocument, error)

	// Name returns the provider name for logging and metadata.
	Name() string
}

// Document is a scraped result to be reranked.
type Document struct {
	Index   int // original index in the scraped results
	URL     string
	Title   string
	Content string // markdown content
}

// RankedDocument is a document with a relevance score assigned by the reranker.
type RankedDocument struct {
	Document
	Score float64 // relevance score from reranker
	Rank  int     // position after reranking (1-based)
}

// New creates a Reranker based on the configured provider.
func New(cfg config.RerankerConfig, logger *slog.Logger) (Reranker, error) {
	switch cfg.Provider {
	case "none", "":
		return NewNoopReranker(), nil
	case "flashrank":
		return NewFlashRankReranker(cfg, logger), nil
	case "kserve":
		return NewKServeReranker(cfg, logger), nil
	case "rankllm":
		return NewRankLLMReranker(cfg, logger), nil
	default:
		return nil, fmt.Errorf("reranker: unknown provider %q (valid: kserve, flashrank, rankllm, none)", cfg.Provider)
	}
}
