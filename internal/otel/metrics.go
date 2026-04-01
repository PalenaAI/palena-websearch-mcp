// Copyright (c) 2026 BITKAIO LLC. All rights reserved.
// Use of this source code is governed by the AGPL-3.0 license.

package otel

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/exporters/stdout/stdoutmetric"
	otelmetric "go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"

	promclient "github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/bitkaio/palena-websearch-mcp/internal/config"
)

const meterName = "github.com/bitkaio/palena-websearch-mcp"

// Meters holds the pre-registered OTel metric instruments for Palena.
type Meters struct {
	// Counters
	SearchRequests   otelmetric.Int64Counter
	ScrapeAttempts   otelmetric.Int64Counter
	ScrapeErrors     otelmetric.Int64Counter
	PIIEntities      otelmetric.Int64Counter
	PIIBlocked       otelmetric.Int64Counter
	RerankRequests   otelmetric.Int64Counter
	PipelineRequests otelmetric.Int64Counter

	// Histograms (durations in milliseconds)
	SearchDuration   otelmetric.Float64Histogram
	ScrapeDuration   otelmetric.Float64Histogram
	PIIDuration      otelmetric.Float64Histogram
	RerankDuration   otelmetric.Float64Histogram
	PipelineDuration otelmetric.Float64Histogram

	// Content length histogram
	ContentLength otelmetric.Int64Histogram
}

// InitMetrics sets up the OpenTelemetry metric provider and registers instruments.
// Returns the Meters, an optional Prometheus HTTP handler (nil if not using prometheus exporter),
// and a shutdown function.
func InitMetrics(ctx context.Context, cfg config.OTelConfig, logger *slog.Logger) (*Meters, http.Handler, func(context.Context) error, error) {
	if !cfg.Enabled || cfg.MetricExporter == "none" {
		logger.Info("otel metrics disabled")
		m, err := registerMeters()
		if err != nil {
			return nil, nil, nil, err
		}
		return m, nil, func(context.Context) error { return nil }, nil
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceNameKey.String(cfg.ServiceName),
			semconv.ServiceVersionKey.String("0.1.0"),
		),
	)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("otel: create metric resource: %w", err)
	}

	var (
		reader      sdkmetric.Reader
		promHandler http.Handler
	)

	switch cfg.MetricExporter {
	case "prometheus":
		promExporter, err := prometheus.New()
		if err != nil {
			return nil, nil, nil, fmt.Errorf("otel: create prometheus exporter: %w", err)
		}
		reader = promExporter
		promHandler = promclient.Handler()
		logger.Info("otel metric exporter initialized", "type", "prometheus")

	case "otlp":
		otlpExporter, err := otlpmetricgrpc.New(ctx,
			otlpmetricgrpc.WithEndpoint(cfg.MetricEndpoint),
			otlpmetricgrpc.WithInsecure(),
		)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("otel: create otlp metric exporter: %w", err)
		}
		reader = sdkmetric.NewPeriodicReader(otlpExporter)
		logger.Info("otel metric exporter initialized", "type", "otlp", "endpoint", cfg.MetricEndpoint)

	case "stdout":
		stdoutExporter, err := stdoutmetric.New(stdoutmetric.WithWriter(os.Stdout))
		if err != nil {
			return nil, nil, nil, fmt.Errorf("otel: create stdout metric exporter: %w", err)
		}
		reader = sdkmetric.NewPeriodicReader(stdoutExporter)
		logger.Info("otel metric exporter initialized", "type", "stdout")

	default:
		return nil, nil, nil, fmt.Errorf("otel: unknown metric exporter %q", cfg.MetricExporter)
	}

	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(reader),
	)
	otel.SetMeterProvider(mp)

	m, err := registerMeters()
	if err != nil {
		return nil, nil, nil, err
	}

	logger.Info("otel metrics initialized", "service", cfg.ServiceName)

	return m, promHandler, mp.Shutdown, nil
}

// registerMeters creates all metric instruments from the global meter provider.
func registerMeters() (*Meters, error) {
	meter := otel.Meter(meterName)
	m := &Meters{}
	var err error

	// Counters.
	m.SearchRequests, err = meter.Int64Counter("palena.search.requests",
		otelmetric.WithDescription("Total search requests to SearXNG"))
	if err != nil {
		return nil, fmt.Errorf("otel: create search.requests counter: %w", err)
	}

	m.ScrapeAttempts, err = meter.Int64Counter("palena.scrape.attempts",
		otelmetric.WithDescription("Total scrape attempts across all levels"))
	if err != nil {
		return nil, fmt.Errorf("otel: create scrape.attempts counter: %w", err)
	}

	m.ScrapeErrors, err = meter.Int64Counter("palena.scrape.errors",
		otelmetric.WithDescription("Total scrape failures"))
	if err != nil {
		return nil, fmt.Errorf("otel: create scrape.errors counter: %w", err)
	}

	m.PIIEntities, err = meter.Int64Counter("palena.pii.entities",
		otelmetric.WithDescription("Total PII entities detected"))
	if err != nil {
		return nil, fmt.Errorf("otel: create pii.entities counter: %w", err)
	}

	m.PIIBlocked, err = meter.Int64Counter("palena.pii.blocked",
		otelmetric.WithDescription("Total documents blocked by PII policy"))
	if err != nil {
		return nil, fmt.Errorf("otel: create pii.blocked counter: %w", err)
	}

	m.RerankRequests, err = meter.Int64Counter("palena.rerank.requests",
		otelmetric.WithDescription("Total rerank requests"))
	if err != nil {
		return nil, fmt.Errorf("otel: create rerank.requests counter: %w", err)
	}

	m.PipelineRequests, err = meter.Int64Counter("palena.pipeline.requests",
		otelmetric.WithDescription("Total pipeline invocations"))
	if err != nil {
		return nil, fmt.Errorf("otel: create pipeline.requests counter: %w", err)
	}

	// Histograms.
	msUnit := otelmetric.WithUnit("ms")

	m.SearchDuration, err = meter.Float64Histogram("palena.search.duration",
		otelmetric.WithDescription("Search stage duration"), msUnit)
	if err != nil {
		return nil, fmt.Errorf("otel: create search.duration histogram: %w", err)
	}

	m.ScrapeDuration, err = meter.Float64Histogram("palena.scrape.duration",
		otelmetric.WithDescription("Per-URL scrape duration"), msUnit)
	if err != nil {
		return nil, fmt.Errorf("otel: create scrape.duration histogram: %w", err)
	}

	m.PIIDuration, err = meter.Float64Histogram("palena.pii.duration",
		otelmetric.WithDescription("Per-document PII processing duration"), msUnit)
	if err != nil {
		return nil, fmt.Errorf("otel: create pii.duration histogram: %w", err)
	}

	m.RerankDuration, err = meter.Float64Histogram("palena.rerank.duration",
		otelmetric.WithDescription("Rerank stage duration"), msUnit)
	if err != nil {
		return nil, fmt.Errorf("otel: create rerank.duration histogram: %w", err)
	}

	m.PipelineDuration, err = meter.Float64Histogram("palena.pipeline.duration",
		otelmetric.WithDescription("Total pipeline duration"), msUnit)
	if err != nil {
		return nil, fmt.Errorf("otel: create pipeline.duration histogram: %w", err)
	}

	m.ContentLength, err = meter.Int64Histogram("palena.scrape.content_length",
		otelmetric.WithDescription("Scraped content length in characters"),
		otelmetric.WithUnit("char"))
	if err != nil {
		return nil, fmt.Errorf("otel: create scrape.content_length histogram: %w", err)
	}

	return m, nil
}
