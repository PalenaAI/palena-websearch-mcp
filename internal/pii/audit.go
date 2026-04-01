// Copyright (c) 2026 BITKAIO LLC. All rights reserved.
// Use of this source code is governed by the AGPL-3.0 license.

package pii

import (
	"log/slog"
	"time"
)

// PIIAuditRecord captures what was detected and what action was taken,
// without ever storing the actual PII text values.
type PIIAuditRecord struct {
	Timestamp     time.Time      `json:"timestamp"`
	RequestID     string         `json:"request_id"`
	URL           string         `json:"url"`
	Mode          string         `json:"mode"`
	Language      string         `json:"language"`
	Entities      []PIIEntityLog `json:"entities"`
	EntityCount   int            `json:"entity_count"`
	PIIDensity    float64        `json:"pii_density"`
	Action        string         `json:"action"` // pass, redacted, blocked
	ContentLength int            `json:"content_length"`
}

// PIIEntityLog records the type, aggregate count, and average confidence
// of detected PII entities. It must NEVER contain actual PII text.
type PIIEntityLog struct {
	Type  string  `json:"type"`
	Score float64 `json:"score"` // average confidence for this type
	Count int     `json:"count"`
}

// buildAuditRecord creates an audit record from analyzer results.
// It aggregates entities by type, computing counts and average scores,
// and calculates PII density (entities per 1000 characters).
func buildAuditRecord(
	requestID string,
	url string,
	mode string,
	language string,
	contentLength int,
	entities []PIIEntity,
	action string,
) PIIAuditRecord {
	// Aggregate by entity type.
	type agg struct {
		totalScore float64
		count      int
	}
	byType := make(map[string]*agg)
	for _, e := range entities {
		a, ok := byType[e.EntityType]
		if !ok {
			a = &agg{}
			byType[e.EntityType] = a
		}
		a.totalScore += e.Score
		a.count++
	}

	logs := make([]PIIEntityLog, 0, len(byType))
	for t, a := range byType {
		logs = append(logs, PIIEntityLog{
			Type:  t,
			Score: a.totalScore / float64(a.count),
			Count: a.count,
		})
	}

	var density float64
	if contentLength > 0 {
		density = float64(len(entities)) / float64(contentLength) * 1000
	}

	return PIIAuditRecord{
		Timestamp:     time.Now(),
		RequestID:     requestID,
		URL:           url,
		Mode:          mode,
		Language:      language,
		Entities:      logs,
		EntityCount:   len(entities),
		PIIDensity:    density,
		Action:        action,
		ContentLength: contentLength,
	}
}

// logAuditRecord emits the audit record as a structured slog entry.
// Fields are chosen to be useful for ClickHouse ingestion and alerting.
func logAuditRecord(logger *slog.Logger, rec PIIAuditRecord) {
	entityTypes := make([]string, len(rec.Entities))
	for i, e := range rec.Entities {
		entityTypes[i] = e.Type
	}

	logger.Info("pii audit",
		"request_id", rec.RequestID,
		"url", rec.URL,
		"mode", rec.Mode,
		"language", rec.Language,
		"entity_count", rec.EntityCount,
		"entity_types", entityTypes,
		"pii_density", rec.PIIDensity,
		"action", rec.Action,
		"content_length", rec.ContentLength,
	)
}
