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

// KServeReranker calls a KServe InferenceService predict endpoint for
// GPU-accelerated cross-encoder reranking (e.g. mxbai-rerank models).
type KServeReranker struct {
	endpoint   string
	model      string
	httpClient *http.Client
	topK       int
	logger     *slog.Logger
}

// NewKServeReranker creates a client for a KServe-hosted cross-encoder model.
func NewKServeReranker(cfg config.RerankerConfig, logger *slog.Logger) *KServeReranker {
	return &KServeReranker{
		endpoint: cfg.Endpoint,
		model:    cfg.Model,
		httpClient: &http.Client{
			Timeout: cfg.Timeout,
		},
		topK:   cfg.TopK,
		logger: logger,
	}
}

// kserveRequest is the KServe v2 predict request format.
// Cross-encoder models expect query-document pairs as inputs.
type kserveRequest struct {
	Inputs []kserveInput `json:"inputs"`
}

type kserveInput struct {
	Name     string      `json:"name"`
	Shape    []int       `json:"shape"`
	Datatype string      `json:"datatype"`
	Data     interface{} `json:"data"`
}

// kserveResponse is the KServe v2 predict response format.
type kserveResponse struct {
	Outputs []kserveOutput `json:"outputs"`
}

type kserveOutput struct {
	Name     string    `json:"name"`
	Shape    []int     `json:"shape"`
	Datatype string    `json:"datatype"`
	Data     []float64 `json:"data"`
}

func (r *KServeReranker) Rerank(ctx context.Context, query string, docs []Document) ([]RankedDocument, error) {
	if len(docs) == 0 {
		return nil, nil
	}

	// Build query-document pairs for the cross-encoder.
	queries := make([]string, len(docs))
	passages := make([]string, len(docs))
	for i, d := range docs {
		queries[i] = query
		passages[i] = d.Content
	}

	payload, err := json.Marshal(kserveRequest{
		Inputs: []kserveInput{
			{Name: "query", Shape: []int{len(docs)}, Datatype: "BYTES", Data: queries},
			{Name: "passages", Shape: []int{len(docs)}, Datatype: "BYTES", Data: passages},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("reranker: kserve: marshal request: %w", err)
	}

	// KServe v2 predict URL pattern.
	url := fmt.Sprintf("%s/v2/models/%s/infer", r.endpoint, r.model)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("reranker: kserve: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("reranker: kserve: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("reranker: kserve: HTTP %d: %s", resp.StatusCode, body)
	}

	var ksResp kserveResponse
	if err := json.NewDecoder(resp.Body).Decode(&ksResp); err != nil {
		return nil, fmt.Errorf("reranker: kserve: decode response: %w", err)
	}

	// Extract scores from the first output tensor.
	if len(ksResp.Outputs) == 0 || len(ksResp.Outputs[0].Data) != len(docs) {
		return nil, fmt.Errorf("reranker: kserve: unexpected response shape: got %d outputs, expected scores for %d docs",
			len(ksResp.Outputs), len(docs))
	}

	scores := ksResp.Outputs[0].Data

	// Build ranked results and sort by score descending.
	ranked := make([]RankedDocument, len(docs))
	for i, doc := range docs {
		ranked[i] = RankedDocument{
			Document: doc,
			Score:    scores[i],
		}
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

	r.logger.Info("reranker: kserve completed",
		"model", r.model,
		"input_count", len(docs),
		"output_count", len(ranked),
		"top_score", ranked[0].Score,
	)

	return ranked, nil
}

func (r *KServeReranker) Name() string { return "kserve" }
