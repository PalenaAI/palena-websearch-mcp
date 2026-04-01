// Copyright (c) 2026 BITKAIO LLC. All rights reserved.
// Use of this source code is governed by the AGPL-3.0 license.

package pii

import (
	"context"
	"fmt"
	"log/slog"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/bitkaio/palena-websearch-mcp/internal/config"
)

// Processor is the pipeline-facing PII interface.
// It wraps the PresidioClient and applies policy-based logic.
type Processor struct {
	client  *PresidioClient
	cfg     config.PIIConfig
	logger  *slog.Logger
	enabled bool
}

// NewProcessor creates a PII processor. If PII is disabled in config,
// Process is a no-op that passes content through.
func NewProcessor(cfg config.PIIConfig, logger *slog.Logger) *Processor {
	var client *PresidioClient
	if cfg.Enabled {
		client = NewPresidioClient(cfg, logger)
	}
	return &Processor{
		client:  client,
		cfg:     cfg,
		logger:  logger,
		enabled: cfg.Enabled,
	}
}

// Process runs the full PII pipeline for a single document according to
// the configured mode (audit/redact/block).
//
// If PII is disabled or Presidio is unreachable, content passes through
// with PIIChecked=false — the pipeline does not fail.
func (p *Processor) Process(ctx context.Context, doc *Document, requestID string) *PIIResult {
	if !p.enabled {
		return &PIIResult{
			Content:    doc.Content,
			PIIChecked: false,
			Action:     "skipped",
		}
	}

	span := trace.SpanFromContext(ctx)
	span.SetAttributes(attribute.String("pii.mode", p.cfg.Mode))

	language := doc.Language
	if language == "" {
		language = p.cfg.Language
	}

	// Step 1: Analyze — detect PII entities.
	entities, err := p.client.Analyze(ctx, doc.Content, language)
	if err != nil {
		p.logger.WarnContext(ctx, "presidio analyzer unavailable, skipping PII check",
			"url", doc.URL,
			"error", err,
		)
		span.SetAttributes(attribute.Bool("pii.degraded", true))
		return &PIIResult{
			Content:    doc.Content,
			PIIChecked: false,
			Action:     "skipped",
		}
	}

	contentLen := len(doc.Content)

	// Step 2: Apply policy based on mode.
	switch p.cfg.Mode {
	case "audit":
		return p.processAudit(ctx, doc, requestID, language, contentLen, entities)
	case "redact":
		return p.processRedact(ctx, doc, requestID, language, contentLen, entities)
	case "block":
		return p.processBlock(ctx, doc, requestID, language, contentLen, entities)
	default:
		p.logger.ErrorContext(ctx, "unknown PII mode, falling back to audit",
			"mode", p.cfg.Mode,
		)
		return p.processAudit(ctx, doc, requestID, language, contentLen, entities)
	}
}

// processAudit detects PII, logs findings, and passes content through unmodified.
func (p *Processor) processAudit(
	ctx context.Context,
	doc *Document,
	requestID, language string,
	contentLen int,
	entities []PIIEntity,
) *PIIResult {
	audit := buildAuditRecord(requestID, doc.URL, "audit", language, contentLen, entities, "pass")
	logAuditRecord(p.logger, audit)
	setSpanAttributes(ctx, audit)

	return &PIIResult{
		Content:    doc.Content,
		PIIChecked: true,
		Action:     "pass",
		Audit:      audit,
	}
}

// processRedact detects PII, calls the anonymizer, and returns redacted content.
func (p *Processor) processRedact(
	ctx context.Context,
	doc *Document,
	requestID, language string,
	contentLen int,
	entities []PIIEntity,
) *PIIResult {
	if len(entities) == 0 {
		audit := buildAuditRecord(requestID, doc.URL, "redact", language, contentLen, entities, "pass")
		logAuditRecord(p.logger, audit)
		setSpanAttributes(ctx, audit)
		return &PIIResult{
			Content:    doc.Content,
			PIIChecked: true,
			Action:     "pass",
			Audit:      audit,
		}
	}

	anonymized, err := p.client.Anonymize(ctx, doc.Content, entities)
	if err != nil {
		p.logger.WarnContext(ctx, "presidio anonymizer unavailable, passing content unmodified",
			"url", doc.URL,
			"error", err,
		)
		audit := buildAuditRecord(requestID, doc.URL, "redact", language, contentLen, entities, "pass")
		logAuditRecord(p.logger, audit)
		setSpanAttributes(ctx, audit)
		return &PIIResult{
			Content:    doc.Content,
			PIIChecked: true,
			Action:     "pass",
			Audit:      audit,
		}
	}

	audit := buildAuditRecord(requestID, doc.URL, "redact", language, contentLen, entities, "redacted")
	logAuditRecord(p.logger, audit)
	setSpanAttributes(ctx, audit)

	return &PIIResult{
		Content:    anonymized.Text,
		PIIChecked: true,
		Action:     "redacted",
		Audit:      audit,
	}
}

// processBlock checks PII density against the threshold. If exceeded, the
// document is blocked. Otherwise, it falls through to redact logic.
func (p *Processor) processBlock(
	ctx context.Context,
	doc *Document,
	requestID, language string,
	contentLen int,
	entities []PIIEntity,
) *PIIResult {
	var density float64
	if contentLen > 0 {
		density = float64(len(entities)) / float64(contentLen) * 1000
	}

	if density > p.cfg.BlockThreshold {
		p.logger.WarnContext(ctx, "PII density exceeds block threshold, rejecting document",
			"url", doc.URL,
			"density", fmt.Sprintf("%.2f", density),
			"threshold", fmt.Sprintf("%.2f", p.cfg.BlockThreshold),
			"entity_count", len(entities),
		)
		audit := buildAuditRecord(requestID, doc.URL, "block", language, contentLen, entities, "blocked")
		logAuditRecord(p.logger, audit)
		setSpanAttributes(ctx, audit)

		return &PIIResult{
			Content:    "",
			PIIChecked: true,
			Action:     "blocked",
			Blocked:    true,
			Audit:      audit,
		}
	}

	// Below threshold — apply redaction.
	return p.processRedact(ctx, doc, requestID, language, contentLen, entities)
}

// setSpanAttributes adds PII audit data to the current OTel span.
func setSpanAttributes(ctx context.Context, audit PIIAuditRecord) {
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(
		attribute.Int("pii.entities_detected", audit.EntityCount),
		attribute.Float64("pii.density", audit.PIIDensity),
		attribute.String("pii.action", audit.Action),
	)
}
