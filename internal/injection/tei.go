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
	"sync"

	"github.com/bitkaio/palena-websearch-mcp/internal/config"
)

// predictConcurrency caps in-flight classifier calls per document.
//
// Pinned to 1 because TEI v1.9 has a DeBERTa-v2/v3 batching bug: whenever
// more than one input lands in the same forward pass, the attention-mask
// shape comparison in DebertaV2Embeddings::forward triggers an incorrect
// unsqueeze and the batch fails with a broadcast_mul shape mismatch
// (HTTP 424). Upstream fix: huggingface/text-embeddings-inference#846,
// expected in TEI v1.10.0. When the compose image is bumped, raise this
// to 8 for ~8x throughput on long documents.
//
// Trade-off under the v1.9 pin: a 70-chunk page takes ~100 s through the
// classifier. The pipeline degrades open on classifier errors so the
// search keeps serving either way, but with concurrency=1 the scores
// actually populate.
const predictConcurrency = 1

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
// Request body (batched — each chunk is wrapped in its own inner array,
// which TEI's sequence-classification path interprets as a single-segment
// input; a two-element inner array would be a sentence-pair input):
//
//	{"inputs": [["text1"], ["text2"]]}
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

// teiPredictRequest is the JSON body sent to TEI /predict. Each chunk is
// wrapped in its own single-element inner array: TEI's batch format is
// [[string], [string, string], ...] where a 1-element inner array means
// single-segment classification and a 2-element inner array would be
// sentence-pair classification. Sending a flat []string triggers a 422
// because TEI reads it as a single pair input and rejects length != 2.
type teiPredictRequest struct {
	Inputs   [][]string `json:"inputs"`
	Truncate bool       `json:"truncate,omitempty"`
}

// teiLabelScore matches one entry of the inner TEI response array.
type teiLabelScore struct {
	Label string  `json:"label"`
	Score float64 `json:"score"`
}

// Predict classifies one or more text chunks via the TEI /predict endpoint.
// The returned slice has one ChunkScore per input chunk in the same order.
//
// Chunks are sent one-per-request rather than in a single batched POST for
// two reasons tied to the current TEI + DeBERTa-v3 release:
//  1. TEI rejects any batch whose length exceeds --max-client-batch-size
//     (default 32), which real scraped pages easily exceed.
//  2. TEI's batched path with DeBERTa-v3 hits a broadcast_mul shape
//     mismatch on ragged inputs, surfacing as HTTP 424 "Backend error".
// Serial calls sidestep both; the cost is ~100 ms per chunk. A future TEI
// release or a custom classifier sidecar can reintroduce batching — the
// caller contract (one ChunkScore per chunk, preserving order) stays stable.
func (c *TEIClient) Predict(ctx context.Context, chunks []string) ([]ChunkScore, error) {
	if len(chunks) == 0 {
		return nil, nil
	}

	out := make([]ChunkScore, len(chunks))
	sem := make(chan struct{}, predictConcurrency)

	// Shared-cancel context so the first failure aborts peers instead of
	// letting them waste classifier capacity on a doomed request.
	gCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	var firstErr error
	var errMu sync.Mutex

	for i, chunk := range chunks {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, chunk string) {
			defer wg.Done()
			defer func() { <-sem }()

			injection, benign, err := c.predictOne(gCtx, chunk)
			if err != nil {
				errMu.Lock()
				if firstErr == nil {
					firstErr = err
					cancel()
				}
				errMu.Unlock()
				return
			}
			out[i] = ChunkScore{
				Index:          i,
				Length:         len(chunk),
				InjectionScore: injection,
				BenignScore:    benign,
				OverThreshold:  injection > c.cfg.ScoreThreshold,
			}
		}(i, chunk)
	}
	wg.Wait()

	if firstErr != nil {
		return nil, firstErr
	}
	return out, nil
}

// predictOne classifies a single chunk. The request wraps the chunk in
// TEI's single-segment batch shape ([[text]]) so the response path is
// identical to a batched call — one inner array of label scores.
func (c *TEIClient) predictOne(ctx context.Context, chunk string) (injection, benign float64, err error) {
	body := teiPredictRequest{
		Inputs:   [][]string{{chunk}},
		Truncate: true,
	}
	data, err := json.Marshal(body)
	if err != nil {
		return 0, 0, fmt.Errorf("injection: marshal predict request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.predictURL+"/predict", bytes.NewReader(data))
	if err != nil {
		return 0, 0, fmt.Errorf("injection: create predict request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, 0, fmt.Errorf("injection: classifier unavailable: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, 0, fmt.Errorf("injection: read classifier response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return 0, 0, fmt.Errorf("injection: classifier returned %d: %s", resp.StatusCode, string(respBody))
	}

	var raw [][]teiLabelScore
	if err := json.Unmarshal(respBody, &raw); err != nil {
		return 0, 0, fmt.Errorf("injection: decode classifier response: %w", err)
	}
	if len(raw) != 1 {
		return 0, 0, fmt.Errorf("injection: classifier returned %d results for 1 input", len(raw))
	}
	injection, benign = pickScores(raw[0], c.cfg.InjectionLabel)
	return injection, benign, nil
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
