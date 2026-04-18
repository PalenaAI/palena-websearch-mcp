// Copyright (c) 2026 bitkaio LLC. All rights reserved.
// Licensed under the Apache License, Version 2.0. See LICENSE for details.

// Package injection provides a sidecar-backed prompt-injection classifier
// for scraped web content. The default model is
// deepset/deberta-v3-base-injection served by Hugging Face's
// text-embeddings-inference (TEI) image, but any HTTP endpoint that
// implements the TEI /predict contract can be used — including a fine-tuned
// successor model trained on the same microsoft/deberta-v3-base backbone.
package injection

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/bitkaio/palena-websearch-mcp/internal/config"
)

// Document represents a scraped page to be screened for prompt injection
// before it is handed to the reranker / LLM.
type Document struct {
	URL     string
	Content string
}

// ChunkScore is the classifier output for a single text chunk.
type ChunkScore struct {
	Index           int     `json:"index"`            // 0-based chunk index in the document
	Length          int     `json:"length"`           // chunk length in characters
	InjectionScore  float64 `json:"injection_score"`  // probability of the configured injection label
	BenignScore     float64 `json:"benign_score"`     // probability of the complementary label
	OverThreshold   bool    `json:"over_threshold"`   // true if InjectionScore > config threshold
}

// Result is returned by Process and contains the (possibly modified)
// content plus metadata describing what the classifier found and what
// action the policy applied.
type Result struct {
	Content        string       // original, annotated, or empty (when blocked)
	Checked        bool         // true if the classifier was reachable and ran
	Action         string       // pass | annotated | blocked | skipped
	Blocked        bool         // true if the document was rejected (mode=block)
	MaxScore       float64      // max InjectionScore across chunks
	Chunks         []ChunkScore // per-chunk findings (no source text)
	Audit          AuditRecord  // audit record (never contains chunk text)
}

// TEIClient calls the Hugging Face Text Embeddings Inference /predict
// endpoint. TEI auto-detects the model task from its config; for
// deepset/deberta-v3-base-injection it serves SequenceClassification.
//
// Request body (single or batched):
//
//	{"inputs": "text"}
//	{"inputs": ["text1", "text2"]}
//
// Response body (one inner array per input, one entry per label):
//
//	[[{"label":"INJECTION","score":0.998},{"label":"LEGIT","score":0.002}]]
type TEIClient struct {
	predictURL  string
	httpClient  *http.Client
	cfg         config.InjectionConfig
	logger      *slog.Logger
}

// NewTEIClient creates a client configured from InjectionConfig.
func NewTEIClient(cfg config.InjectionConfig, logger *slog.Logger) *TEIClient {
	return &TEIClient{
		predictURL: cfg.PredictURL,
		httpClient: &http.Client{
			Timeout: cfg.Timeout,
		},
		cfg:    cfg,
		logger: logger,
	}
}

// teiPredictRequest is the JSON body sent to TEI /predict. The Inputs
// field is encoded as a JSON array even for a single string so the
// wire format is identical for batched and unbatched calls.
type teiPredictRequest struct {
	Inputs   []string `json:"inputs"`
	Truncate bool     `json:"truncate,omitempty"`
}

// teiLabelScore matches one entry of the inner TEI response array.
type teiLabelScore struct {
	Label string  `json:"label"`
	Score float64 `json:"score"`
}

// Predict classifies one or more text chunks via the TEI /predict endpoint.
// The returned slice has one ChunkScore per input chunk in the same order.
func (c *TEIClient) Predict(ctx context.Context, chunks []string) ([]ChunkScore, error) {
	if len(chunks) == 0 {
		return nil, nil
	}

	body := teiPredictRequest{
		Inputs:   chunks,
		Truncate: true,
	}

	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("injection: marshal predict request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.predictURL+"/predict", bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("injection: create predict request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("injection: classifier unavailable: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("injection: read classifier response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("injection: classifier returned %d: %s", resp.StatusCode, string(respBody))
	}

	var raw [][]teiLabelScore
	if err := json.Unmarshal(respBody, &raw); err != nil {
		return nil, fmt.Errorf("injection: decode classifier response: %w", err)
	}
	if len(raw) != len(chunks) {
		return nil, fmt.Errorf("injection: classifier returned %d results for %d inputs", len(raw), len(chunks))
	}

	out := make([]ChunkScore, len(chunks))
	for i, labels := range raw {
		injection, benign := pickScores(labels, c.cfg.InjectionLabel)
		out[i] = ChunkScore{
			Index:          i,
			Length:         len(chunks[i]),
			InjectionScore: injection,
			BenignScore:    benign,
			OverThreshold:  injection > c.cfg.ScoreThreshold,
		}
	}
	return out, nil
}

// pickScores extracts the injection-class probability and the highest
// remaining probability (treated as the benign score) from a TEI label
// list. Label matching is case-insensitive so a fine-tuned model that
// uses "injection" vs "Injection" still works without config changes.
func pickScores(labels []teiLabelScore, injectionLabel string) (injection, benign float64) {
	for _, ls := range labels {
		if equalFold(ls.Label, injectionLabel) {
			injection = ls.Score
			continue
		}
		if ls.Score > benign {
			benign = ls.Score
		}
	}
	return injection, benign
}

// equalFold is a small helper to avoid pulling in strings just for one call.
func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if 'A' <= ca && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if 'A' <= cb && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}
