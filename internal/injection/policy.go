// Copyright (c) 2026 bitkaio LLC. All rights reserved.
// Licensed under the Apache License, Version 2.0. See LICENSE for details.

package injection

import (
	"context"
	"log/slog"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/bitkaio/palena-websearch-mcp/internal/config"
)

// Processor is the pipeline-facing prompt-injection interface. It wraps
// the TEIClient and applies policy-based logic per the configured mode.
type Processor struct {
	client  *TEIClient
	cfg     config.InjectionConfig
	logger  *slog.Logger
	enabled bool
}

// NewProcessor creates a prompt-injection processor. If injection scanning
// is disabled in config, Process is a no-op that passes content through.
func NewProcessor(cfg config.InjectionConfig, logger *slog.Logger) *Processor {
	var client *TEIClient
	if cfg.Enabled {
		client = NewTEIClient(cfg, logger)
	}
	return &Processor{
		client:  client,
		cfg:     cfg,
		logger:  logger,
		enabled: cfg.Enabled,
	}
}

// Process runs the full prompt-injection pipeline for a single document
// according to the configured mode (audit/annotate/block).
//
// If injection scanning is disabled or the TEI sidecar is unreachable,
// content passes through with Checked=false — the pipeline does not fail.
// This matches the PII processor's degrade-open semantics.
func (p *Processor) Process(ctx context.Context, doc *Document, requestID string) *Result {
	if !p.enabled {
		return &Result{
			Content: doc.Content,
			Checked: false,
			Action:  "skipped",
		}
	}

	span := trace.SpanFromContext(ctx)
	span.SetAttributes(attribute.String("injection.mode", p.cfg.Mode))

	chunks, offsets := chunkDocument(doc.Content, p.cfg.MaxChunkChars)
	if len(chunks) == 0 {
		return &Result{
			Content: doc.Content,
			Checked: true,
			Action:  "pass",
		}
	}

	scores, err := p.client.Predict(ctx, chunks)
	if err != nil {
		p.logger.WarnContext(ctx, "injection classifier unavailable, skipping check",
			"url", doc.URL,
			"error", err,
		)
		span.SetAttributes(attribute.Bool("injection.degraded", true))
		return &Result{
			Content: doc.Content,
			Checked: false,
			Action:  "skipped",
		}
	}

	contentLen := len(doc.Content)

	switch p.cfg.Mode {
	case "audit":
		return p.processAudit(ctx, doc, requestID, contentLen, scores)
	case "annotate":
		return p.processAnnotate(ctx, doc, requestID, contentLen, chunks, offsets, scores)
	case "block":
		return p.processBlock(ctx, doc, requestID, contentLen, scores)
	default:
		p.logger.ErrorContext(ctx, "unknown injection mode, falling back to audit",
			"mode", p.cfg.Mode,
		)
		return p.processAudit(ctx, doc, requestID, contentLen, scores)
	}
}

// processAudit logs findings and passes content through unmodified.
func (p *Processor) processAudit(
	ctx context.Context,
	doc *Document,
	requestID string,
	contentLen int,
	scores []ChunkScore,
) *Result {
	audit := buildAuditRecord(requestID, doc.URL, "audit", p.cfg.Model, contentLen, scores, "pass")
	logAuditRecord(p.logger, audit)
	setSpanAttributes(ctx, audit)

	return &Result{
		Content:  doc.Content,
		Checked:  true,
		Action:   "pass",
		MaxScore: audit.MaxScore,
		Chunks:   scores,
		Audit:    audit,
	}
}

// processAnnotate wraps suspicious chunks in delimiters that signal to the
// downstream LLM that the content is untrusted. The wrapper text is
// configurable so operators can match their model's known-good framing.
//
// Annotation, not deletion, is the default: removing the chunk would
// silently lose information from the page, while wrapping it lets the
// LLM still see (and refuse to follow) the suspicious segment.
func (p *Processor) processAnnotate(
	ctx context.Context,
	doc *Document,
	requestID string,
	contentLen int,
	chunks []string,
	offsets []int,
	scores []ChunkScore,
) *Result {
	any := false
	for _, s := range scores {
		if s.OverThreshold {
			any = true
			break
		}
	}

	if !any {
		audit := buildAuditRecord(requestID, doc.URL, "annotate", p.cfg.Model, contentLen, scores, "pass")
		logAuditRecord(p.logger, audit)
		setSpanAttributes(ctx, audit)
		return &Result{
			Content:  doc.Content,
			Checked:  true,
			Action:   "pass",
			MaxScore: audit.MaxScore,
			Chunks:   scores,
			Audit:    audit,
		}
	}

	annotated := annotate(doc.Content, chunks, offsets, scores, p.cfg.AnnotateOpen, p.cfg.AnnotateClose)

	audit := buildAuditRecord(requestID, doc.URL, "annotate", p.cfg.Model, contentLen, scores, "annotated")
	logAuditRecord(p.logger, audit)
	setSpanAttributes(ctx, audit)

	return &Result{
		Content:  annotated,
		Checked:  true,
		Action:   "annotated",
		MaxScore: audit.MaxScore,
		Chunks:   scores,
		Audit:    audit,
	}
}

// processBlock drops the document if any chunk's injection score exceeds
// the threshold. Per-chunk granularity catches a short malicious paragraph
// hidden inside an otherwise legitimate page.
func (p *Processor) processBlock(
	ctx context.Context,
	doc *Document,
	requestID string,
	contentLen int,
	scores []ChunkScore,
) *Result {
	for _, s := range scores {
		if s.OverThreshold {
			audit := buildAuditRecord(requestID, doc.URL, "block", p.cfg.Model, contentLen, scores, "blocked")
			logAuditRecord(p.logger, audit)
			setSpanAttributes(ctx, audit)
			p.logger.WarnContext(ctx, "prompt injection detected, rejecting document",
				"url", doc.URL,
				"max_score", audit.MaxScore,
				"over_threshold", audit.OverThreshold,
			)
			return &Result{
				Content:  "",
				Checked:  true,
				Action:   "blocked",
				Blocked:  true,
				MaxScore: audit.MaxScore,
				Chunks:   scores,
				Audit:    audit,
			}
		}
	}

	audit := buildAuditRecord(requestID, doc.URL, "block", p.cfg.Model, contentLen, scores, "pass")
	logAuditRecord(p.logger, audit)
	setSpanAttributes(ctx, audit)
	return &Result{
		Content:  doc.Content,
		Checked:  true,
		Action:   "pass",
		MaxScore: audit.MaxScore,
		Chunks:   scores,
		Audit:    audit,
	}
}

// chunkDocument splits content into paragraph-sized chunks for batched
// scoring. Splits on blank lines first; chunks longer than maxChars are
// further split on hard newlines, then on character boundaries as a
// last resort. Returns the chunk strings and their byte offsets in the
// original content (used by annotate to rewrite spans in place).
func chunkDocument(content string, maxChars int) ([]string, []int) {
	if maxChars <= 0 {
		maxChars = 1200
	}
	if strings.TrimSpace(content) == "" {
		return nil, nil
	}

	var (
		chunks  []string
		offsets []int
	)

	paragraphs := splitWithOffsets(content, "\n\n")
	for _, p := range paragraphs {
		if strings.TrimSpace(p.text) == "" {
			continue
		}
		if len(p.text) <= maxChars {
			chunks = append(chunks, p.text)
			offsets = append(offsets, p.offset)
			continue
		}

		// Long paragraph — try splitting on single newlines.
		lines := splitWithOffsets(p.text, "\n")
		buf := ""
		bufStart := -1
		for _, l := range lines {
			absOff := p.offset + l.offset
			if buf == "" {
				buf = l.text
				bufStart = absOff
				continue
			}
			if len(buf)+1+len(l.text) > maxChars {
				chunks = append(chunks, buf)
				offsets = append(offsets, bufStart)
				buf = l.text
				bufStart = absOff
				continue
			}
			buf = buf + "\n" + l.text
		}
		if buf != "" {
			// Hard split if a single line still exceeds maxChars.
			for len(buf) > maxChars {
				chunks = append(chunks, buf[:maxChars])
				offsets = append(offsets, bufStart)
				buf = buf[maxChars:]
				bufStart += maxChars
			}
			chunks = append(chunks, buf)
			offsets = append(offsets, bufStart)
		}
	}
	return chunks, offsets
}

type segment struct {
	text   string
	offset int
}

// splitWithOffsets splits s on sep and records the byte offset of each
// resulting segment in the original string.
func splitWithOffsets(s, sep string) []segment {
	var out []segment
	start := 0
	for {
		idx := strings.Index(s[start:], sep)
		if idx < 0 {
			out = append(out, segment{text: s[start:], offset: start})
			return out
		}
		out = append(out, segment{text: s[start : start+idx], offset: start})
		start = start + idx + len(sep)
	}
}

// annotate wraps each over-threshold chunk in the configured open/close
// markers. Chunks are processed in reverse offset order so each rewrite
// leaves earlier offsets untouched.
func annotate(content string, chunks []string, offsets []int, scores []ChunkScore, open, close string) string {
	out := content
	for i := len(scores) - 1; i >= 0; i-- {
		if !scores[i].OverThreshold {
			continue
		}
		off := offsets[i]
		end := off + len(chunks[i])
		if off < 0 || end > len(out) {
			continue
		}
		out = out[:off] + open + out[off:end] + close + out[end:]
	}
	return out
}

// setSpanAttributes adds injection audit data to the current OTel span.
func setSpanAttributes(ctx context.Context, audit AuditRecord) {
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(
		attribute.Int("injection.chunk_count", audit.ChunkCount),
		attribute.Int("injection.over_threshold", audit.OverThreshold),
		attribute.Float64("injection.max_score", audit.MaxScore),
		attribute.Float64("injection.mean_score", audit.MeanScore),
		attribute.String("injection.action", audit.Action),
	)
}
