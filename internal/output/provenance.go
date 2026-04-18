// Copyright (c) 2026 BITKAIO LLC. All rights reserved.
// Use of this source code is governed by the AGPL-3.0 license.

package output

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bitkaio/palena-websearch-mcp/internal/config"
)

// ClickHouse table DDL for provenance records. Create this table manually
// or via a migration tool before enabling ClickHouse export.
//
// CREATE TABLE palena_provenance (
//     request_id         String,
//     timestamp          DateTime64(3),
//     url                String,
//     final_url          String,
//     http_status        UInt16,
//     scraper_level      UInt8,
//     content_type       String,
//     raw_html_hash      String,
//     extracted_hash     String,
//     final_hash         String,
//     content_length     UInt32,
//     extraction_method  String,
//     pii_mode           String,
//     pii_entities_found UInt16,
//     pii_action         String,
//     reranker_score     Float64,
//     reranker_rank      UInt16,
//     robots_txt_checked Bool,
//     robots_txt_allowed Bool,
//     scrape_duration_ms UInt32,
//     pii_duration_ms    UInt32,
//     total_duration_ms  UInt32
// ) ENGINE = MergeTree()
// ORDER BY (timestamp, request_id)
// TTL timestamp + INTERVAL 90 DAY;

// ProvenanceRecord is the auditable provenance record for a scraped URL.
type ProvenanceRecord struct {
	// Identity
	RequestID string    `json:"request_id"`
	Timestamp time.Time `json:"timestamp"`
	URL       string    `json:"url"`

	// Fetch metadata
	HTTPStatus   int    `json:"http_status"`
	FinalURL     string `json:"final_url"`
	ScraperLevel int    `json:"scraper_level"`
	ContentType  string `json:"content_type"`

	// Content hashes (SHA-256)
	RawHTMLHash   string `json:"raw_html_hash"`
	ExtractedHash string `json:"extracted_hash"`
	FinalHash     string `json:"final_hash"`

	// Processing metadata
	ContentLength    int     `json:"content_length"`
	ExtractionMethod string  `json:"extraction_method"`
	PIIMode          string  `json:"pii_mode"`
	PIIEntitiesFound int     `json:"pii_entities_found"`
	PIIAction        string  `json:"pii_action"`
	RerankerScore    float64 `json:"reranker_score"`
	RerankerRank     int     `json:"reranker_rank"`

	// Policy
	RobotsTxtChecked bool `json:"robots_txt_checked"`
	RobotsTxtAllowed bool `json:"robots_txt_allowed"`

	// Timing
	ScrapeDurationMs int64 `json:"scrape_duration_ms"`
	PIIDurationMs    int64 `json:"pii_duration_ms"`
	TotalDurationMs  int64 `json:"total_duration_ms"`
}

// SHA256Hex computes the SHA-256 hash of content and returns it as a hex string.
func SHA256Hex(content string) string {
	h := sha256.Sum256([]byte(content))
	return hex.EncodeToString(h[:])
}

// ComputeProvenance computes the three-stage hash chain for content provenance.
// Returns (rawHTMLHash, extractedHash, finalHash).
func ComputeProvenance(rawHTML, extractedMarkdown, finalContent string) (string, string, string) {
	return SHA256Hex(rawHTML), SHA256Hex(extractedMarkdown), SHA256Hex(finalContent)
}

// ExtractionMethodName returns a human-readable name for the scraper level.
func ExtractionMethodName(level int) string {
	switch level {
	case 0:
		return "readability"
	case 1:
		return "cdp"
	case 2:
		return "cdp_stealth"
	default:
		return "unknown"
	}
}

// LogProvenance emits a structured log entry for a provenance record.
func LogProvenance(logger *slog.Logger, r *ProvenanceRecord) {
	logger.Info("provenance",
		"request_id", r.RequestID,
		"url", r.URL,
		"final_url", r.FinalURL,
		"http_status", r.HTTPStatus,
		"scraper_level", r.ScraperLevel,
		"content_type", r.ContentType,
		"raw_html_hash", r.RawHTMLHash,
		"extracted_hash", r.ExtractedHash,
		"final_hash", r.FinalHash,
		"content_length", r.ContentLength,
		"extraction_method", r.ExtractionMethod,
		"pii_mode", r.PIIMode,
		"pii_entities_found", r.PIIEntitiesFound,
		"pii_action", r.PIIAction,
		"reranker_score", r.RerankerScore,
		"reranker_rank", r.RerankerRank,
		"robots_txt_checked", r.RobotsTxtChecked,
		"robots_txt_allowed", r.RobotsTxtAllowed,
		"scrape_duration_ms", r.ScrapeDurationMs,
		"pii_duration_ms", r.PIIDurationMs,
		"total_duration_ms", r.TotalDurationMs,
	)
}

// ClickHouseExporter batches provenance records and inserts them into ClickHouse.
// It is safe for concurrent use.
type ClickHouseExporter struct {
	endpoint  string
	database  string
	table     string
	batchSize int
	client    *http.Client
	logger    *slog.Logger

	mu    sync.Mutex
	batch []*ProvenanceRecord
	timer *time.Timer
	done  chan struct{}
}

// NewClickHouseExporter creates an exporter. Returns nil if ClickHouse is not enabled.
func NewClickHouseExporter(cfg config.ProvenanceClickHouseConfig, logger *slog.Logger) *ClickHouseExporter {
	if !cfg.Enabled || cfg.Endpoint == "" {
		return nil
	}

	batchSize := cfg.BatchSize
	if batchSize <= 0 {
		batchSize = 50
	}
	flushInterval := cfg.FlushInterval
	if flushInterval <= 0 {
		flushInterval = 5 * time.Second
	}

	e := &ClickHouseExporter{
		endpoint:  cfg.Endpoint,
		database:  cfg.Database,
		table:     cfg.Table,
		batchSize: batchSize,
		client:    &http.Client{Timeout: 10 * time.Second},
		logger:    logger,
		batch:     make([]*ProvenanceRecord, 0, batchSize),
		done:      make(chan struct{}),
	}

	e.timer = time.AfterFunc(flushInterval, func() {
		e.Flush()
	})

	return e
}

// Add adds a provenance record to the batch. If the batch is full, it is flushed.
func (e *ClickHouseExporter) Add(r *ProvenanceRecord) {
	if e == nil {
		return
	}

	e.mu.Lock()
	e.batch = append(e.batch, r)
	shouldFlush := len(e.batch) >= e.batchSize
	e.mu.Unlock()

	if shouldFlush {
		e.Flush()
	}
}

// Flush sends all buffered records to ClickHouse.
func (e *ClickHouseExporter) Flush() {
	if e == nil {
		return
	}

	e.mu.Lock()
	if len(e.batch) == 0 {
		e.mu.Unlock()
		return
	}
	records := e.batch
	e.batch = make([]*ProvenanceRecord, 0, e.batchSize)
	e.timer.Reset(5 * time.Second)
	e.mu.Unlock()

	if err := e.insert(context.Background(), records); err != nil {
		e.logger.Warn("provenance: clickhouse insert failed",
			"records", len(records),
			"error", err,
		)
	}
}

// Close flushes remaining records and stops the timer.
func (e *ClickHouseExporter) Close() {
	if e == nil {
		return
	}
	e.timer.Stop()
	e.Flush()
}

// insert sends a batch of records to ClickHouse via the HTTP interface using
// TSV format for simplicity (no ClickHouse Go driver dependency).
func (e *ClickHouseExporter) insert(ctx context.Context, records []*ProvenanceRecord) error {
	if len(records) == 0 {
		return nil
	}

	var buf strings.Builder
	for _, r := range records {
		// TSV row: all fields tab-separated.
		fmt.Fprintf(&buf, "%s\t%s\t%s\t%s\t%d\t%d\t%s\t%s\t%s\t%s\t%d\t%s\t%s\t%d\t%s\t%f\t%d\t%t\t%t\t%d\t%d\t%d\n",
			r.RequestID,
			r.Timestamp.UTC().Format("2006-01-02 15:04:05.000"),
			r.URL,
			r.FinalURL,
			r.HTTPStatus,
			r.ScraperLevel,
			r.ContentType,
			r.RawHTMLHash,
			r.ExtractedHash,
			r.FinalHash,
			r.ContentLength,
			r.ExtractionMethod,
			r.PIIMode,
			r.PIIEntitiesFound,
			r.PIIAction,
			r.RerankerScore,
			r.RerankerRank,
			r.RobotsTxtChecked,
			r.RobotsTxtAllowed,
			r.ScrapeDurationMs,
			r.PIIDurationMs,
			r.TotalDurationMs,
		)
	}

	query := fmt.Sprintf("INSERT INTO %s.%s FORMAT TabSeparated", e.database, e.table)
	url := fmt.Sprintf("%s/?query=%s", e.endpoint, query)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(buf.String()))
	if err != nil {
		return fmt.Errorf("provenance: build clickhouse request: %w", err)
	}
	req.Header.Set("Content-Type", "text/tab-separated-values")

	resp, err := e.client.Do(req)
	if err != nil {
		return fmt.Errorf("provenance: clickhouse request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("provenance: clickhouse returned %d", resp.StatusCode)
	}

	e.logger.Debug("provenance: clickhouse batch inserted",
		"records", len(records),
	)
	return nil
}
