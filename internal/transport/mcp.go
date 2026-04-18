// Copyright (c) 2026 BITKAIO LLC. All rights reserved.
// Use of this source code is governed by the AGPL-3.0 license.

package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bitkaio/palena-websearch-mcp/internal/config"
	"github.com/bitkaio/palena-websearch-mcp/internal/injection"
	palenaOTel "github.com/bitkaio/palena-websearch-mcp/internal/otel"
	"github.com/bitkaio/palena-websearch-mcp/internal/output"
	"github.com/bitkaio/palena-websearch-mcp/internal/pii"
	"github.com/bitkaio/palena-websearch-mcp/internal/policy"
	"github.com/bitkaio/palena-websearch-mcp/internal/reranker"
	"github.com/bitkaio/palena-websearch-mcp/internal/scraper"
	"github.com/bitkaio/palena-websearch-mcp/internal/search"
)

const version = "0.1.0"

// Server wraps the MCP server and the HTTP server that exposes it.
type Server struct {
	mcpServer    *mcp.Server
	httpServer   *http.Server
	cfg          *config.Config
	logger       *slog.Logger
	searchClient *search.SearXNGClient
}

// NewServer creates a Palena MCP server with the web_search tool registered.
func NewServer(
	cfg *config.Config,
	searchClient *search.SearXNGClient,
	sc *scraper.Scraper,
	domainFilter *policy.DomainFilter,
	robotsChecker *policy.RobotsChecker,
	rateLimiter *policy.RateLimiter,
	piiProc *pii.Processor,
	injProc *injection.Processor,
	rr reranker.Reranker,
	meters *palenaOTel.Meters,
	provExporter *output.ClickHouseExporter,
	promHandler http.Handler,
	logger *slog.Logger,
) *Server {
	mcpServer := mcp.NewServer(
		&mcp.Implementation{
			Name:    "palena",
			Version: version,
		},
		nil,
	)

	// Register the web_search tool.
	toolHandler := NewToolHandler(searchClient, sc, domainFilter, robotsChecker, rateLimiter, piiProc, injProc, rr, cfg, meters, provExporter, logger)
	mcp.AddTool(mcpServer, WebSearchTool(), toolHandler.HandleWebSearch)

	s := &Server{
		mcpServer:    mcpServer,
		cfg:          cfg,
		logger:       logger,
		searchClient: searchClient,
	}

	// Build HTTP mux with MCP transports + operational endpoints.
	mux := http.NewServeMux()

	// SSE transport (legacy, widely supported by Claude Desktop, LibreChat).
	// The SSE handler serves both GET (event stream) and POST (messages) on
	// the same path. Client does GET /sse to connect, then POST /sse?sessionid=...
	sseHandler := mcp.NewSSEHandler(func(r *http.Request) *mcp.Server {
		return mcpServer
	}, nil)
	mux.Handle("/sse", sseHandler)

	// Streamable HTTP transport (current MCP spec).
	streamHandler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		return mcpServer
	}, nil)
	mux.Handle("/mcp", streamHandler)

	// Operational endpoints.
	mux.HandleFunc("GET /health", s.handleHealth)

	// Prometheus metrics endpoint (if configured).
	if promHandler != nil {
		mux.Handle("/metrics", promHandler)
	}

	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	s.httpServer = &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
	}

	return s
}

// Start begins listening and serving. It blocks until the server is shut down.
func (s *Server) Start() error {
	addr := s.httpServer.Addr
	s.logger.Info("palena MCP server starting",
		"addr", addr,
		"version", version,
		"transports", "SSE (/sse), Streamable HTTP (/mcp)",
	)

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("transport: listen %s: %w", addr, err)
	}

	s.logger.Info("palena MCP server listening",
		"addr", ln.Addr().String(),
	)

	return s.httpServer.Serve(ln)
}

// Shutdown gracefully shuts down the HTTP server.
func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info("palena MCP server shutting down")
	return s.httpServer.Shutdown(ctx)
}

// healthResponse is the JSON body returned by GET /health.
type healthResponse struct {
	Status   string            `json:"status"`
	Version  string            `json:"version"`
	Sidecars map[string]string `json:"sidecars"`
}

// handleHealth checks reachability of configured sidecars and reports status.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	resp := healthResponse{
		Status:  "ok",
		Version: version,
		Sidecars: map[string]string{
			"searxng": s.pingSidecar(r.Context(), s.cfg.Search.SearXNGURL+"/healthz"),
		},
	}

	// If SearXNG (hard dependency) is down, report degraded.
	if resp.Sidecars["searxng"] != "ok" {
		resp.Status = "degraded"
	}

	// PII sidecars are soft dependencies — report status but don't degrade.
	if s.cfg.PII.Enabled {
		resp.Sidecars["presidio-analyzer"] = s.pingSidecar(r.Context(), s.cfg.PII.AnalyzerURL+"/health")
		resp.Sidecars["presidio-anonymizer"] = s.pingSidecar(r.Context(), s.cfg.PII.AnonymizerURL+"/health")
	}

	// Injection-guard sidecar is also a soft dependency — degrades open
	// when unreachable so the pipeline keeps serving.
	if s.cfg.Injection.Enabled {
		resp.Sidecars["injection-guard"] = s.pingSidecar(r.Context(), s.cfg.Injection.PredictURL+"/health")
	}

	w.Header().Set("Content-Type", "application/json")
	if resp.Status != "ok" {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.Error("transport: encode /health response failed", "error", err)
	}
}

// pingSidecar makes a lightweight GET request to a sidecar health endpoint.
func (s *Server) pingSidecar(ctx context.Context, url string) string {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "error"
	}

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "unavailable"
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return "ok"
	}
	return fmt.Sprintf("unhealthy (%d)", resp.StatusCode)
}
