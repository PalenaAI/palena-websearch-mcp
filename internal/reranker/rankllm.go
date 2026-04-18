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
	"strings"

	"github.com/bitkaio/palena-websearch-mcp/internal/config"
)

// RankLLMReranker uses any OpenAI-compatible LLM endpoint to score document
// relevance via a structured prompt. Slower and more expensive than a dedicated
// cross-encoder, but requires no additional model deployment.
type RankLLMReranker struct {
	endpoint   string
	model      string
	httpClient *http.Client
	topK       int
	logger     *slog.Logger
}

// NewRankLLMReranker creates an LLM-as-reranker client.
func NewRankLLMReranker(cfg config.RerankerConfig, logger *slog.Logger) *RankLLMReranker {
	return &RankLLMReranker{
		endpoint: cfg.Endpoint,
		model:    cfg.Model,
		httpClient: &http.Client{
			Timeout: cfg.Timeout,
		},
		topK:   cfg.TopK,
		logger: logger,
	}
}

// chatRequest is the OpenAI-compatible chat completion request.
type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// chatResponse is the minimal OpenAI-compatible chat completion response.
type chatResponse struct {
	Choices []chatChoice `json:"choices"`
}

type chatChoice struct {
	Message chatMessage `json:"message"`
}

func (r *RankLLMReranker) Rerank(ctx context.Context, query string, docs []Document) ([]RankedDocument, error) {
	if len(docs) == 0 {
		return nil, nil
	}

	prompt := buildRerankPrompt(query, docs)

	payload, err := json.Marshal(chatRequest{
		Model: r.model,
		Messages: []chatMessage{
			{Role: "user", Content: prompt},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("reranker: rankllm: marshal request: %w", err)
	}

	// Use OpenAI-compatible /v1/chat/completions endpoint.
	url := r.endpoint + "/v1/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("reranker: rankllm: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("reranker: rankllm: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("reranker: rankllm: HTTP %d: %s", resp.StatusCode, body)
	}

	var chatResp chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return nil, fmt.Errorf("reranker: rankllm: decode response: %w", err)
	}

	if len(chatResp.Choices) == 0 {
		return nil, fmt.Errorf("reranker: rankllm: empty response from LLM")
	}

	// Parse the JSON array of scores from the LLM response.
	scores, err := parseScores(chatResp.Choices[0].Message.Content, len(docs))
	if err != nil {
		r.logger.Warn("reranker: rankllm: score parsing failed, falling back to original order",
			"error", err,
			"raw_response", chatResp.Choices[0].Message.Content,
		)
		// Graceful fallback: return documents in original order.
		result := make([]RankedDocument, len(docs))
		for i, doc := range docs {
			result[i] = RankedDocument{Document: doc, Score: 1.0 - float64(i)*0.01, Rank: i + 1}
		}
		return result, nil
	}

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

	r.logger.Info("reranker: rankllm completed",
		"model", r.model,
		"input_count", len(docs),
		"output_count", len(ranked),
		"top_score", ranked[0].Score,
	)

	return ranked, nil
}

func (r *RankLLMReranker) Name() string { return "rankllm" }

// buildRerankPrompt constructs the scoring prompt for the LLM.
func buildRerankPrompt(query string, docs []Document) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Given the query: %q\n\n", query)
	b.WriteString("Score the relevance of each document on a scale of 0.0 to 1.0.\n")
	b.WriteString("Respond ONLY with a JSON array of scores in the same order as the documents.\n\n")

	for i, doc := range docs {
		// Truncate long content to stay within token limits.
		content := doc.Content
		const maxChars = 2000
		if len(content) > maxChars {
			content = content[:maxChars] + "..."
		}
		fmt.Fprintf(&b, "Document %d: %s\n", i+1, content)
	}

	b.WriteString("\nResponse format: [0.95, 0.23, 0.87]")
	return b.String()
}

// parseScores extracts a JSON array of float64 scores from the LLM response text.
// It tolerates surrounding text by finding the first '[' and last ']'.
func parseScores(text string, expectedCount int) ([]float64, error) {
	start := strings.Index(text, "[")
	end := strings.LastIndex(text, "]")
	if start == -1 || end == -1 || end <= start {
		return nil, fmt.Errorf("no JSON array found in response")
	}

	var scores []float64
	if err := json.Unmarshal([]byte(text[start:end+1]), &scores); err != nil {
		return nil, fmt.Errorf("parse JSON array: %w", err)
	}

	if len(scores) != expectedCount {
		return nil, fmt.Errorf("expected %d scores, got %d", expectedCount, len(scores))
	}

	// Clamp scores to [0, 1].
	for i := range scores {
		if scores[i] < 0 {
			scores[i] = 0
		}
		if scores[i] > 1 {
			scores[i] = 1
		}
	}

	return scores, nil
}
