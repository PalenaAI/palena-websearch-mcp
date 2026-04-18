// Copyright (c) 2026 bitkaio LLC. All rights reserved.
// Licensed under the Apache License, Version 2.0. See LICENSE for details.

package injection

import (
	"log/slog"
	"time"
)

// AuditRecord captures what the prompt-injection classifier found and what
// action was taken, without storing the chunk text itself. The structure
// mirrors the PII audit record so downstream observability (slog → SIEM →
// ClickHouse) can ingest both event families with the same shape.
type AuditRecord struct {
	Timestamp     time.Time `json:"timestamp"`
	RequestID     string    `json:"request_id"`
	URL           string    `json:"url"`
	Mode          string    `json:"mode"`
	Model         string    `json:"model"`           // model identifier loaded by the sidecar
	ChunkCount    int       `json:"chunk_count"`     // total chunks scored
	OverThreshold int       `json:"over_threshold"`  // chunks with score > threshold
	MaxScore      float64   `json:"max_score"`       // max injection score across chunks
	MeanScore     float64   `json:"mean_score"`      // mean injection score across chunks
	Action        string    `json:"action"`          // pass | annotated | blocked
	ContentLength int       `json:"content_length"`
}

// buildAuditRecord summarizes per-chunk scores into a single record.
// It computes max/mean injection scores and the count of chunks over
// the configured threshold. It never references chunk text.
func buildAuditRecord(
	requestID string,
	url string,
	mode string,
	model string,
	contentLength int,
	chunks []ChunkScore,
	action string,
) AuditRecord {
	var (
		over    int
		maxS    float64
		sumS    float64
	)
	for _, c := range chunks {
		if c.OverThreshold {
			over++
		}
		if c.InjectionScore > maxS {
			maxS = c.InjectionScore
		}
		sumS += c.InjectionScore
	}

	var mean float64
	if len(chunks) > 0 {
		mean = sumS / float64(len(chunks))
	}

	return AuditRecord{
		Timestamp:     time.Now(),
		RequestID:     requestID,
		URL:           url,
		Mode:          mode,
		Model:         model,
		ChunkCount:    len(chunks),
		OverThreshold: over,
		MaxScore:      maxS,
		MeanScore:     mean,
		Action:        action,
		ContentLength: contentLength,
	}
}

// logAuditRecord emits the audit record as a structured slog entry.
// Field names match the PII audit shape where overlap exists, so a single
// SIEM dashboard can join both event types on request_id and url.
func logAuditRecord(logger *slog.Logger, rec AuditRecord) {
	logger.Info("injection: audit",
		"request_id", rec.RequestID,
		"url", rec.URL,
		"mode", rec.Mode,
		"model", rec.Model,
		"chunk_count", rec.ChunkCount,
		"over_threshold", rec.OverThreshold,
		"max_score", rec.MaxScore,
		"mean_score", rec.MeanScore,
		"action", rec.Action,
		"content_length", rec.ContentLength,
	)
}
