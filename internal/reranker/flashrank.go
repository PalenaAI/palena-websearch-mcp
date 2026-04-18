// Copyright (c) 2026 BITKAIO LLC. All rights reserved.
// Use of this source code is governed by the AGPL-3.0 license.

package reranker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"

	"github.com/bitkaio/palena-websearch-mcp/internal/config"
)

// FlashRankReranker calls a FlashRank sidecar HTTP endpoint for CPU-based
// ONNX reranking. The sidecar exposes POST /rerank with a simple JSON API.
type FlashRankReranker struct {
	endpoint   string
	httpClient *http.Client
	topK       int
	logger     *slog.Logger
}

// NewFlashRankReranker creates a client for the FlashRank sidecar.
func NewFlashRankReranker(cfg config.RerankerConfig, logger *slog.Logger) *FlashRankReranker {
	return &FlashRankReranker{
		endpoint: cfg.Endpoint,
		httpClient: &http.Client{
			Timeout: cfg.Timeout,
		},
		topK:   cfg.TopK,
		logger: logger,
	}
}

// flashRankRequest is the JSON body sent to the FlashRank sidecar.
type flashRankRequest struct {
	Query     string              `json:"query"`
	Documents []flashRankDocument `json:"documents"`
}

type flashRankDocument struct {
	Content string `json:"content"`
}

// flashRankResponse is the JSON body returned by the FlashRank sidecar.
type flashRankResponse struct {
	Results []flashRankResult `json:"results"`
}

type flashRankResult struct {
	Index int     `json:"index"`
	Score float64 `json:"score"`
}

func (r *FlashRankReranker) Rerank(ctx context.Context, query string, docs []Document) ([]RankedDocument, error) {
	if len(docs) == 0 {
		return nil, nil
	}

	// Build request payload.
	frDocs := make([]flashRankDocument, len(docs))
	for i, d := range docs {
		frDocs[i] = flashRankDocument{Content: d.Content}
	}

	payload, err := json.Marshal(flashRankRequest{
		Query:     query,
		Documents: frDocs,
	})
	if err != nil {
		return nil, fmt.Errorf("reranker: flashrank: marshal request: %w", err)
	}

	url := r.endpoint + "/rerank"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("reranker: flashrank: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("reranker: flashrank: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("reranker: flashrank: HTTP %d: %s", resp.StatusCode, body)
	}

	var frResp flashRankResponse
	if err := json.NewDecoder(resp.Body).Decode(&frResp); err != nil {
		return nil, fmt.Errorf("reranker: flashrank: decode response: %w", err)
	}

	// Map scores back to documents and sort by score descending.
	ranked := make([]RankedDocument, 0, len(frResp.Results))
	for _, res := range frResp.Results {
		if res.Index < 0 || res.Index >= len(docs) {
			r.logger.Warn("reranker: flashrank: index out of range, skipping",
				"index", res.Index, "doc_count", len(docs))
			continue
		}
		ranked = append(ranked, RankedDocument{
			Document: docs[res.Index],
			Score:    res.Score,
		})
	}

	sort.Slice(ranked, func(i, j int) bool {
		return ranked[i].Score > ranked[j].Score
	})

	// Apply top K.
	if r.topK > 0 && len(ranked) > r.topK {
		ranked = ranked[:r.topK]
	}

	// Assign ranks.
	for i := range ranked {
		ranked[i].Rank = i + 1
	}

	r.logger.Info("reranker: flashrank completed",
		"input_count", len(docs),
		"output_count", len(ranked),
		"top_score", ranked[0].Score,
	)

	return ranked, nil
}

func (r *FlashRankReranker) Name() string { return "flashrank" }
