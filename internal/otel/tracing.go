// Copyright (c) 2026 BITKAIO LLC. All rights reserved.
// Use of this source code is governed by the AGPL-3.0 license.

package otel

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/bitkaio/palena-websearch-mcp/internal/config"
)

const tracerName = "github.com/bitkaio/palena-websearch-mcp"

// Tracer returns the package-level tracer used by Palena pipeline spans.
func Tracer() trace.Tracer {
	return otel.Tracer(tracerName)
}

// InitTracing sets up the OpenTelemetry trace provider based on config.
// Returns a shutdown function that must be called on process exit.
// If OTel is disabled, a no-op provider is registered and shutdown is a no-op.
func InitTracing(ctx context.Context, cfg config.OTelConfig, logger *slog.Logger) (func(context.Context) error, error) {
	if !cfg.Enabled || cfg.TraceExporter == "none" {
		logger.Info("otel tracing disabled")
		return func(context.Context) error { return nil }, nil
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceNameKey.String(cfg.ServiceName),
			semconv.ServiceVersionKey.String("0.1.0"),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("otel: create resource: %w", err)
	}

	var exporter sdktrace.SpanExporter
	switch cfg.TraceExporter {
	case "otlp":
		exporter, err = otlptracegrpc.New(ctx,
			otlptracegrpc.WithEndpoint(cfg.TraceEndpoint),
			otlptracegrpc.WithInsecure(),
		)
		if err != nil {
			return nil, fmt.Errorf("otel: create otlp trace exporter: %w", err)
		}
		logger.Info("otel trace exporter initialized", "type", "otlp", "endpoint", cfg.TraceEndpoint)

	case "stdout":
		exporter, err = stdouttrace.New(stdouttrace.WithWriter(os.Stdout))
		if err != nil {
			return nil, fmt.Errorf("otel: create stdout trace exporter: %w", err)
		}
		logger.Info("otel trace exporter initialized", "type", "stdout")

	default:
		return nil, fmt.Errorf("otel: unknown trace exporter %q", cfg.TraceExporter)
	}

	sampler := sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.SampleRate))

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler),
	)
	otel.SetTracerProvider(tp)

	logger.Info("otel tracing initialized",
		"service", cfg.ServiceName,
		"sampler_rate", cfg.SampleRate,
	)

	return tp.Shutdown, nil
}

// StartSpan starts a new span with the given name and returns the context and span.
func StartSpan(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	return Tracer().Start(ctx, name, opts...)
}

// SetSpanError records an error on the span and sets its status to Error.
func SetSpanError(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
}

// SetSpanOK sets the span status to OK.
func SetSpanOK(span trace.Span) {
	span.SetStatus(codes.Ok, "")
}

// StringAttr is a convenience alias for attribute.String.
func StringAttr(key, value string) attribute.KeyValue {
	return attribute.String(key, value)
}

// IntAttr is a convenience alias for attribute.Int.
func IntAttr(key string, value int) attribute.KeyValue {
	return attribute.Int(key, value)
}

// Int64Attr is a convenience alias for attribute.Int64.
func Int64Attr(key string, value int64) attribute.KeyValue {
	return attribute.Int64(key, value)
}

// Float64Attr is a convenience alias for attribute.Float64.
func Float64Attr(key string, value float64) attribute.KeyValue {
	return attribute.Float64(key, value)
}
