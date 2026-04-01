// Copyright (c) 2026 BITKAIO LLC. All rights reserved.
// Use of this source code is governed by the AGPL-3.0 license.

package pii

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

// PIIEntity represents a single PII detection from the Presidio Analyzer.
type PIIEntity struct {
	EntityType string  `json:"entity_type"`
	Start      int     `json:"start"`
	End        int     `json:"end"`
	Score      float64 `json:"score"`
}

// AnonymizedResult is the response from the Presidio Anonymizer.
type AnonymizedResult struct {
	Text  string           `json:"text"`
	Items []AnonymizedItem `json:"items"`
}

// AnonymizedItem describes one redaction applied by the anonymizer.
type AnonymizedItem struct {
	Start      int    `json:"start"`
	End        int    `json:"end"`
	EntityType string `json:"entity_type"`
	Text       string `json:"text"`
	Operator   string `json:"operator"`
}

// Document represents a scraped page to be processed for PII.
type Document struct {
	URL      string
	Content  string
	Language string
}

// PIIResult is returned by Process and contains the (possibly modified)
// content plus metadata about what was found and what action was taken.
type PIIResult struct {
	Content    string         // original or anonymized content
	PIIChecked bool           // true if Presidio was reachable and analysis ran
	Action     string         // pass, redacted, blocked
	Blocked    bool           // true if the document was rejected (mode=block)
	Audit      PIIAuditRecord // audit record (never contains PII text)
}

// PresidioClient calls the Presidio Analyzer and Anonymizer REST APIs.
type PresidioClient struct {
	analyzerURL   string
	anonymizerURL string
	httpClient    *http.Client
	cfg           config.PIIConfig
	logger        *slog.Logger
}

// NewPresidioClient creates a client configured from PIIConfig.
func NewPresidioClient(cfg config.PIIConfig, logger *slog.Logger) *PresidioClient {
	return &PresidioClient{
		analyzerURL:   cfg.AnalyzerURL,
		anonymizerURL: cfg.AnonymizerURL,
		httpClient: &http.Client{
			Timeout: cfg.Timeout,
		},
		cfg:    cfg,
		logger: logger,
	}
}

// analyzeRequest is the JSON body sent to Presidio Analyzer.
type analyzeRequest struct {
	Text           string   `json:"text"`
	Language       string   `json:"language"`
	Entities       []string `json:"entities,omitempty"`
	ScoreThreshold float64  `json:"score_threshold"`
}

// Analyze detects PII entities in text by calling the Presidio Analyzer.
func (c *PresidioClient) Analyze(ctx context.Context, text, language string) ([]PIIEntity, error) {
	body := analyzeRequest{
		Text:           text,
		Language:       language,
		Entities:       c.cfg.Entities,
		ScoreThreshold: c.cfg.ScoreThreshold,
	}

	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("pii: marshal analyze request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.analyzerURL+"/analyze", bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("pii: create analyze request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("pii: analyzer unavailable: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("pii: read analyzer response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("pii: analyzer returned %d: %s", resp.StatusCode, string(respBody))
	}

	var entities []PIIEntity
	if err := json.Unmarshal(respBody, &entities); err != nil {
		return nil, fmt.Errorf("pii: decode analyzer response: %w", err)
	}

	return entities, nil
}

// anonymizeRequest is the JSON body sent to Presidio Anonymizer.
type anonymizeRequest struct {
	Text            string                      `json:"text"`
	AnalyzerResults []PIIEntity                 `json:"analyzer_results"`
	Anonymizers     map[string]anonymizerAction `json:"anonymizers"`
}

// anonymizerAction matches the Presidio anonymizer config format.
type anonymizerAction struct {
	Type        string `json:"type"`
	NewValue    string `json:"new_value,omitempty"`
	MaskingChar string `json:"masking_char,omitempty"`
	CharsToMask int    `json:"chars_to_mask,omitempty"`
	FromEnd     bool   `json:"from_end,omitempty"`
}

// Anonymize calls the Presidio Anonymizer to redact detected PII entities.
func (c *PresidioClient) Anonymize(ctx context.Context, text string, entities []PIIEntity) (*AnonymizedResult, error) {
	anons := make(map[string]anonymizerAction, len(c.cfg.Anonymizers))
	for name, entry := range c.cfg.Anonymizers {
		anons[name] = anonymizerAction{
			Type:        entry.Type,
			NewValue:    entry.NewValue,
			MaskingChar: entry.MaskingChar,
			CharsToMask: entry.CharsToMask,
			FromEnd:     entry.FromEnd,
		}
	}

	body := anonymizeRequest{
		Text:            text,
		AnalyzerResults: entities,
		Anonymizers:     anons,
	}

	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("pii: marshal anonymize request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.anonymizerURL+"/anonymize", bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("pii: create anonymize request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("pii: anonymizer unavailable: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("pii: read anonymizer response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("pii: anonymizer returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result AnonymizedResult
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("pii: decode anonymizer response: %w", err)
	}

	return &result, nil
}
