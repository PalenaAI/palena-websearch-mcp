// Copyright (c) 2026 BITKAIO LLC. All rights reserved.
// Use of this source code is governed by the AGPL-3.0 license.

package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the top-level configuration for the Palena MCP server.
type Config struct {
	Server     ServerConfig     `yaml:"server"`
	Search     SearchConfig     `yaml:"search"`
	Scraper    ScraperConfig    `yaml:"scraper"`
	Policy     PolicyConfig     `yaml:"policy"`
	PII        PIIConfig        `yaml:"pii"`
	Injection  InjectionConfig  `yaml:"injection"`
	Reranker   RerankerConfig   `yaml:"reranker"`
	Provenance ProvenanceConfig `yaml:"provenance"`
	OTel       OTelConfig       `yaml:"otel"`
	Logging    LoggingConfig    `yaml:"logging"`
}

// PolicyConfig holds settings for domain filtering, robots.txt, and rate limiting.
type PolicyConfig struct {
	Robots    RobotsConfig    `yaml:"robots"`
	Domains   DomainConfig    `yaml:"domains"`
	RateLimit RateLimitConfig `yaml:"rateLimit"`
}

// RobotsConfig controls robots.txt enforcement.
type RobotsConfig struct {
	Enabled      bool `yaml:"enabled"`
	CacheSeconds int  `yaml:"cacheSeconds"`
}

// DomainConfig controls domain allowlist/blocklist filtering.
type DomainConfig struct {
	Mode      string   `yaml:"mode"` // allowlist | blocklist
	Allowlist []string `yaml:"allowlist"`
	Blocklist []string `yaml:"blocklist"`
}

// RateLimitConfig controls per-domain request rate limiting.
type RateLimitConfig struct {
	Enabled                    bool `yaml:"enabled"`
	RequestsPerDomainPerMinute int  `yaml:"requestsPerDomainPerMinute"`
}

// PIIConfig holds settings for the PII detection and redaction subsystem.
type PIIConfig struct {
	Enabled        bool                       `yaml:"enabled"`
	Mode           string                     `yaml:"mode"` // audit | redact | block
	AnalyzerURL    string                     `yaml:"analyzerURL"`
	AnonymizerURL  string                     `yaml:"anonymizerURL"`
	Language       string                     `yaml:"language"`
	ScoreThreshold float64                    `yaml:"scoreThreshold"`
	BlockThreshold float64                    `yaml:"blockThreshold"` // entities per 1000 chars (mode=block)
	Entities       []string                   `yaml:"entities"`
	Anonymizers    map[string]AnonymizerEntry `yaml:"anonymizers"`
	Timeout        time.Duration              `yaml:"timeout"`
}

// AnonymizerEntry defines the redaction strategy for an entity type.
type AnonymizerEntry struct {
	Type        string `yaml:"type"`        // replace | mask
	NewValue    string `yaml:"newValue"`    // replacement text (type=replace)
	MaskingChar string `yaml:"maskingChar"` // mask character (type=mask)
	CharsToMask int    `yaml:"charsToMask"` // number of chars to mask (type=mask)
	FromEnd     bool   `yaml:"fromEnd"`     // mask from end (type=mask)
}

// InjectionConfig holds settings for the prompt-injection scanner subsystem.
//
// The default model is deepset/deberta-v3-base-injection served via Hugging
// Face's text-embeddings-inference (TEI) image. Any HTTP endpoint that
// implements the TEI /predict contract for sequence classification can be
// substituted — including a fine-tuned successor model trained on the same
// microsoft/deberta-v3-base backbone, swapped in by changing PredictURL
// and Model with no Palena code changes.
type InjectionConfig struct {
	Enabled        bool          `yaml:"enabled"`
	Mode           string        `yaml:"mode"`           // audit | annotate | block
	PredictURL     string        `yaml:"predictURL"`     // TEI sidecar base URL (no trailing /predict)
	Model          string        `yaml:"model"`          // model identifier loaded by the sidecar (informational; logged in audit)
	InjectionLabel string        `yaml:"injectionLabel"` // label name in TEI output that signals injection (e.g. "INJECTION")
	ScoreThreshold float64       `yaml:"scoreThreshold"` // chunks above this score are treated as injection
	MaxChunkChars  int           `yaml:"maxChunkChars"`  // upper bound on per-chunk character count
	AnnotateOpen   string        `yaml:"annotateOpen"`   // wrapper prefix for suspicious chunks (mode=annotate)
	AnnotateClose  string        `yaml:"annotateClose"`  // wrapper suffix for suspicious chunks (mode=annotate)
	Timeout        time.Duration `yaml:"timeout"`
}

// RerankerConfig holds settings for the pluggable reranker subsystem.
type RerankerConfig struct {
	Provider string        `yaml:"provider"` // kserve | flashrank | rankllm | none
	Endpoint string        `yaml:"endpoint"` // sidecar/inference endpoint URL
	Model    string        `yaml:"model"`    // model identifier (provider-specific)
	TopK     int           `yaml:"topK"`     // return top K results after reranking
	Timeout  time.Duration `yaml:"timeout"`  // reranker call timeout
}

// ScraperConfig holds tiered extraction settings.
type ScraperConfig struct {
	MaxConcurrency   int                    `yaml:"maxConcurrency"`
	Timeouts         ScraperTimeoutsConfig  `yaml:"timeouts"`
	Playwright       PlaywrightConfig       `yaml:"playwright"`
	Stealth          StealthConfig          `yaml:"stealth"`
	Proxy            ProxyConfig            `yaml:"proxy"`
	ContentDetection ContentDetectionConfig `yaml:"contentDetection"`
}

// PlaywrightConfig holds settings for connecting to an external Playwright server.
type PlaywrightConfig struct {
	Endpoint string `yaml:"endpoint"` // ws:// URL exposed by `playwright run-server`
	MaxTabs  int    `yaml:"maxTabs"`  // max concurrent browser contexts
}

// StealthConfig controls anti-detection measures for L2 extraction.
type StealthConfig struct {
	Enabled            bool `yaml:"enabled"`
	RandomizeViewport  bool `yaml:"randomizeViewport"`
	RandomizeUserAgent bool `yaml:"randomizeUserAgent"`
}

// ProxyConfig holds proxy rotation settings for L2 extraction.
type ProxyConfig struct {
	Enabled         bool             `yaml:"enabled"`
	Pool            []ProxyPoolEntry `yaml:"pool"`
	CooldownSeconds int              `yaml:"cooldownSeconds"`
}

// ProxyPoolEntry defines a single proxy in the pool.
type ProxyPoolEntry struct {
	URL      string `yaml:"url"`      // http://user:pass@host:port or socks5://...
	Region   string `yaml:"region"`   // optional region tag
	Priority int    `yaml:"priority"` // higher = preferred
}

// ScraperTimeoutsConfig holds per-level timeout values.
type ScraperTimeoutsConfig struct {
	HTTPGet     time.Duration `yaml:"httpGet"`
	BrowserPage time.Duration `yaml:"browserPage"`
	BrowserNav  time.Duration `yaml:"browserNav"`
}

// ContentDetectionConfig controls heuristics for deciding whether L0 content
// is sufficient or needs escalation to a browser-based level.
type ContentDetectionConfig struct {
	MinTextLength int     `yaml:"minTextLength"`
	MinTextRatio  float64 `yaml:"minTextRatio"`
	MaxScriptTags int     `yaml:"maxScriptTags"`
}

// ServerConfig holds HTTP server settings.
type ServerConfig struct {
	Host         string        `yaml:"host"`
	Port         int           `yaml:"port"`
	ReadTimeout  time.Duration `yaml:"readTimeout"`
	WriteTimeout time.Duration `yaml:"writeTimeout"`
}

// SearchConfig holds SearXNG and query-related settings.
type SearchConfig struct {
	SearXNGURL      string               `yaml:"searxngURL"`
	DefaultEngines  []string             `yaml:"defaultEngines"`
	EngineRoutes    map[string][]string  `yaml:"engineRoutes"`
	DefaultLanguage string               `yaml:"defaultLanguage"`
	SafeSearch      int                  `yaml:"safeSearch"`
	MaxResults      int                  `yaml:"maxResults"`
	Timeout         time.Duration        `yaml:"timeout"`
	QueryExpansion  QueryExpansionConfig `yaml:"queryExpansion"`
}

// QueryExpansionConfig controls optional query expansion behavior.
type QueryExpansionConfig struct {
	Enabled     bool `yaml:"enabled"`
	MaxVariants int  `yaml:"maxVariants"`
}

// ProvenanceConfig controls content provenance record generation and storage.
type ProvenanceConfig struct {
	Enabled    bool                       `yaml:"enabled"`
	ClickHouse ProvenanceClickHouseConfig `yaml:"clickhouse"`
}

// ProvenanceClickHouseConfig holds optional ClickHouse export settings.
type ProvenanceClickHouseConfig struct {
	Enabled       bool          `yaml:"enabled"`
	Endpoint      string        `yaml:"endpoint"`
	Database      string        `yaml:"database"`
	Table         string        `yaml:"table"`
	BatchSize     int           `yaml:"batchSize"`
	FlushInterval time.Duration `yaml:"flushInterval"`
}

// OTelConfig holds OpenTelemetry tracing and metrics configuration.
type OTelConfig struct {
	Enabled        bool          `yaml:"enabled"`
	ServiceName    string        `yaml:"serviceName"`
	TraceExporter  string        `yaml:"traceExporter"`  // otlp | stdout | none
	TraceEndpoint  string        `yaml:"traceEndpoint"`  // OTLP gRPC endpoint
	MetricExporter string        `yaml:"metricExporter"` // prometheus | otlp | stdout | none
	MetricEndpoint string        `yaml:"metricEndpoint"` // OTLP gRPC endpoint for metrics
	SampleRate     float64       `yaml:"sampleRate"`     // 0.0 to 1.0
	ExportTimeout  time.Duration `yaml:"exportTimeout"`
}

// LoggingConfig controls structured logging.
type LoggingConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

// setDefaults populates Config with built-in defaults (step 1 of loading).
func (c *Config) setDefaults() {
	c.Server = ServerConfig{
		Host:         "0.0.0.0",
		Port:         8080,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
	}
	c.Search = SearchConfig{
		SearXNGURL:      "http://searxng:8080",
		DefaultEngines:  []string{"google", "duckduckgo", "brave"},
		DefaultLanguage: "en",
		SafeSearch:      1,
		MaxResults:      10,
		Timeout:         10 * time.Second,
		EngineRoutes: map[string][]string{
			"general": {"google", "duckduckgo", "brave"},
			"news":    {"google news", "duckduckgo", "bing news"},
			"code":    {"github", "stackoverflow", "duckduckgo"},
			"science": {"google scholar", "duckduckgo", "wikipedia"},
		},
		QueryExpansion: QueryExpansionConfig{
			Enabled:     false,
			MaxVariants: 2,
		},
	}
	c.Scraper = ScraperConfig{
		MaxConcurrency: 5,
		Timeouts: ScraperTimeoutsConfig{
			HTTPGet:     10 * time.Second,
			BrowserPage: 15 * time.Second,
			BrowserNav:  30 * time.Second,
		},
		Playwright: PlaywrightConfig{
			Endpoint: "ws://playwright:3000",
			MaxTabs:  3,
		},
		Stealth: StealthConfig{
			Enabled:            true,
			RandomizeViewport:  true,
			RandomizeUserAgent: true,
		},
		Proxy: ProxyConfig{
			Enabled:         false,
			CooldownSeconds: 300,
		},
		ContentDetection: ContentDetectionConfig{
			MinTextLength: 500,
			MinTextRatio:  0.05,
			MaxScriptTags: 5,
		},
	}
	c.Policy = PolicyConfig{
		Robots: RobotsConfig{
			Enabled:      true,
			CacheSeconds: 3600,
		},
		Domains: DomainConfig{
			Mode:      "blocklist",
			Allowlist: []string{},
			Blocklist: []string{},
		},
		RateLimit: RateLimitConfig{
			Enabled:                    true,
			RequestsPerDomainPerMinute: 10,
		},
	}
	c.PII = PIIConfig{
		Enabled:        true,
		Mode:           "audit",
		AnalyzerURL:    "http://presidio-analyzer:5002",
		AnonymizerURL:  "http://presidio-anonymizer:5001",
		Language:       "en",
		ScoreThreshold: 0.5,
		BlockThreshold: 5.0,
		Entities: []string{
			"PERSON", "EMAIL_ADDRESS", "PHONE_NUMBER", "CREDIT_CARD",
			"IBAN_CODE", "IP_ADDRESS", "LOCATION", "US_SSN", "MEDICAL_LICENSE",
		},
		Anonymizers: map[string]AnonymizerEntry{
			"DEFAULT":       {Type: "replace", NewValue: "<REDACTED>"},
			"PERSON":        {Type: "replace", NewValue: "<PERSON>"},
			"EMAIL_ADDRESS": {Type: "mask", MaskingChar: "*", CharsToMask: 100, FromEnd: false},
			"PHONE_NUMBER":  {Type: "replace", NewValue: "<PHONE>"},
		},
		Timeout: 5 * time.Second,
	}
	c.Injection = InjectionConfig{
		Enabled:        false,
		Mode:           "audit",
		PredictURL:     "http://injection-guard:8080",
		Model:          "deepset/deberta-v3-base-injection",
		InjectionLabel: "INJECTION",
		ScoreThreshold: 0.85,
		MaxChunkChars:  1200,
		AnnotateOpen:   "<untrusted-content reason=\"prompt-injection-suspected\">\n",
		AnnotateClose:  "\n</untrusted-content>",
		Timeout:        5 * time.Second,
	}
	c.Reranker = RerankerConfig{
		Provider: "none",
		Endpoint: "http://reranker:8080",
		TopK:     5,
		Timeout:  10 * time.Second,
	}
	c.Provenance = ProvenanceConfig{
		Enabled: true,
		ClickHouse: ProvenanceClickHouseConfig{
			Enabled:       false,
			Endpoint:      "http://clickhouse:8123",
			Database:      "palena",
			Table:         "palena_provenance",
			BatchSize:     50,
			FlushInterval: 5 * time.Second,
		},
	}
	c.OTel = OTelConfig{
		Enabled:        false,
		ServiceName:    "palena",
		TraceExporter:  "none",
		TraceEndpoint:  "localhost:4317",
		MetricExporter: "none",
		MetricEndpoint: "localhost:4317",
		SampleRate:     1.0,
		ExportTimeout:  10 * time.Second,
	}
	c.Logging = LoggingConfig{
		Level:  "info",
		Format: "json",
	}
}

// Load reads configuration from the given YAML path, applies environment
// variable overrides, validates required fields, and returns the result.
func Load(path string) (*Config, error) {
	cfg := &Config{}
	cfg.setDefaults()

	// Step 2: read YAML file (if it exists).
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	if err == nil {
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("config: parse %s: %w", path, err)
		}
	}

	// Step 3: apply environment variable overrides.
	cfg.applyEnvOverrides()

	// Step 4: validate.
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("config: validate: %w", err)
	}

	return cfg, nil
}

// applyEnvOverrides reads PALENA_* environment variables and overrides the
// corresponding config values. Only a relevant subset is mapped here; more
// will be added as subsystems are implemented.
func (c *Config) applyEnvOverrides() {
	if v := os.Getenv("PALENA_SERVER_HOST"); v != "" {
		c.Server.Host = v
	}
	if v := os.Getenv("PALENA_SERVER_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			c.Server.Port = p
		}
	}
	if v := os.Getenv("PALENA_SEARCH_SEARXNG_URL"); v != "" {
		c.Search.SearXNGURL = v
	}
	if v := os.Getenv("PALENA_SEARCH_DEFAULT_LANGUAGE"); v != "" {
		c.Search.DefaultLanguage = v
	}
	if v := os.Getenv("PALENA_SEARCH_QUERY_EXPANSION_ENABLED"); v != "" {
		c.Search.QueryExpansion.Enabled = strings.EqualFold(v, "true")
	}
	if v := os.Getenv("PALENA_SCRAPER_PLAYWRIGHT_ENDPOINT"); v != "" {
		c.Scraper.Playwright.Endpoint = v
	}
	if v := os.Getenv("PALENA_SCRAPER_PROXY_ENABLED"); v != "" {
		c.Scraper.Proxy.Enabled = strings.EqualFold(v, "true")
	}
	// Policy overrides.
	if v := os.Getenv("PALENA_POLICY_ROBOTS_ENABLED"); v != "" {
		c.Policy.Robots.Enabled = strings.EqualFold(v, "true")
	}
	if v := os.Getenv("PALENA_POLICY_ROBOTS_CACHE_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.Policy.Robots.CacheSeconds = n
		}
	}
	if v := os.Getenv("PALENA_POLICY_DOMAINS_MODE"); v != "" {
		c.Policy.Domains.Mode = v
	}
	if v := os.Getenv("PALENA_POLICY_RATELIMIT_ENABLED"); v != "" {
		c.Policy.RateLimit.Enabled = strings.EqualFold(v, "true")
	}
	if v := os.Getenv("PALENA_POLICY_RATELIMIT_RPM"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.Policy.RateLimit.RequestsPerDomainPerMinute = n
		}
	}

	if v := os.Getenv("PALENA_PII_ENABLED"); v != "" {
		c.PII.Enabled = strings.EqualFold(v, "true")
	}
	if v := os.Getenv("PALENA_PII_MODE"); v != "" {
		c.PII.Mode = v
	}
	if v := os.Getenv("PALENA_PII_ANALYZER_URL"); v != "" {
		c.PII.AnalyzerURL = v
	}
	if v := os.Getenv("PALENA_PII_ANONYMIZER_URL"); v != "" {
		c.PII.AnonymizerURL = v
	}
	if v := os.Getenv("PALENA_INJECTION_ENABLED"); v != "" {
		c.Injection.Enabled = strings.EqualFold(v, "true")
	}
	if v := os.Getenv("PALENA_INJECTION_MODE"); v != "" {
		c.Injection.Mode = v
	}
	if v := os.Getenv("PALENA_INJECTION_PREDICT_URL"); v != "" {
		c.Injection.PredictURL = v
	}
	if v := os.Getenv("PALENA_INJECTION_MODEL"); v != "" {
		c.Injection.Model = v
	}
	if v := os.Getenv("PALENA_INJECTION_LABEL"); v != "" {
		c.Injection.InjectionLabel = v
	}
	if v := os.Getenv("PALENA_INJECTION_SCORE_THRESHOLD"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			c.Injection.ScoreThreshold = f
		}
	}
	if v := os.Getenv("PALENA_RERANKER_PROVIDER"); v != "" {
		c.Reranker.Provider = v
	}
	if v := os.Getenv("PALENA_RERANKER_ENDPOINT"); v != "" {
		c.Reranker.Endpoint = v
	}
	if v := os.Getenv("PALENA_RERANKER_TOP_K"); v != "" {
		if k, err := strconv.Atoi(v); err == nil {
			c.Reranker.TopK = k
		}
	}
	if v := os.Getenv("PALENA_PROVENANCE_ENABLED"); v != "" {
		c.Provenance.Enabled = strings.EqualFold(v, "true")
	}
	if v := os.Getenv("PALENA_PROVENANCE_CLICKHOUSE_ENABLED"); v != "" {
		c.Provenance.ClickHouse.Enabled = strings.EqualFold(v, "true")
	}
	if v := os.Getenv("PALENA_PROVENANCE_CLICKHOUSE_ENDPOINT"); v != "" {
		c.Provenance.ClickHouse.Endpoint = v
	}
	if v := os.Getenv("PALENA_OTEL_ENABLED"); v != "" {
		c.OTel.Enabled = strings.EqualFold(v, "true")
	}
	if v := os.Getenv("PALENA_OTEL_TRACE_EXPORTER"); v != "" {
		c.OTel.TraceExporter = v
	}
	if v := os.Getenv("PALENA_OTEL_TRACE_ENDPOINT"); v != "" {
		c.OTel.TraceEndpoint = v
	}
	if v := os.Getenv("PALENA_OTEL_METRIC_EXPORTER"); v != "" {
		c.OTel.MetricExporter = v
	}
	if v := os.Getenv("PALENA_OTEL_METRIC_ENDPOINT"); v != "" {
		c.OTel.MetricEndpoint = v
	}
	if v := os.Getenv("PALENA_LOGGING_LEVEL"); v != "" {
		c.Logging.Level = v
	}
}

// validate checks required fields and value constraints.
func (c *Config) validate() error {
	if c.Search.SearXNGURL == "" {
		return fmt.Errorf("search.searxngURL is required")
	}
	if _, err := url.ParseRequestURI(c.Search.SearXNGURL); err != nil {
		return fmt.Errorf("search.searxngURL is not a valid URL: %w", err)
	}
	if c.Search.MaxResults < 1 {
		return fmt.Errorf("search.maxResults must be >= 1, got %d", c.Search.MaxResults)
	}
	if c.Search.Timeout <= 0 {
		return fmt.Errorf("search.timeout must be positive")
	}

	// Policy validation.
	validDomainModes := map[string]bool{"allowlist": true, "blocklist": true}
	if !validDomainModes[c.Policy.Domains.Mode] {
		return fmt.Errorf("policy.domains.mode must be one of: allowlist, blocklist; got %q", c.Policy.Domains.Mode)
	}
	if c.Policy.Robots.CacheSeconds < 0 {
		return fmt.Errorf("policy.robots.cacheSeconds must be >= 0, got %d", c.Policy.Robots.CacheSeconds)
	}
	if c.Policy.RateLimit.Enabled && c.Policy.RateLimit.RequestsPerDomainPerMinute < 1 {
		return fmt.Errorf("policy.rateLimit.requestsPerDomainPerMinute must be >= 1, got %d", c.Policy.RateLimit.RequestsPerDomainPerMinute)
	}

	// PII validation.
	if c.PII.Enabled {
		validModes := map[string]bool{"audit": true, "redact": true, "block": true}
		if !validModes[c.PII.Mode] {
			return fmt.Errorf("pii.mode must be one of: audit, redact, block; got %q", c.PII.Mode)
		}
		if c.PII.AnalyzerURL == "" {
			return fmt.Errorf("pii.analyzerURL is required when pii.enabled is true")
		}
		if c.PII.Timeout <= 0 {
			return fmt.Errorf("pii.timeout must be positive")
		}
	}

	// Injection validation.
	if c.Injection.Enabled {
		validInjectionModes := map[string]bool{"audit": true, "annotate": true, "block": true}
		if !validInjectionModes[c.Injection.Mode] {
			return fmt.Errorf("injection.mode must be one of: audit, annotate, block; got %q", c.Injection.Mode)
		}
		if c.Injection.PredictURL == "" {
			return fmt.Errorf("injection.predictURL is required when injection.enabled is true")
		}
		if _, err := url.ParseRequestURI(c.Injection.PredictURL); err != nil {
			return fmt.Errorf("injection.predictURL is not a valid URL: %w", err)
		}
		if c.Injection.InjectionLabel == "" {
			return fmt.Errorf("injection.injectionLabel is required when injection.enabled is true")
		}
		if c.Injection.ScoreThreshold < 0 || c.Injection.ScoreThreshold > 1 {
			return fmt.Errorf("injection.scoreThreshold must be between 0.0 and 1.0, got %f", c.Injection.ScoreThreshold)
		}
		if c.Injection.MaxChunkChars < 1 {
			return fmt.Errorf("injection.maxChunkChars must be >= 1, got %d", c.Injection.MaxChunkChars)
		}
		if c.Injection.Timeout <= 0 {
			return fmt.Errorf("injection.timeout must be positive")
		}
	}

	// Reranker validation.
	validProviders := map[string]bool{"kserve": true, "flashrank": true, "rankllm": true, "none": true, "": true}
	if !validProviders[c.Reranker.Provider] {
		return fmt.Errorf("reranker.provider must be one of: kserve, flashrank, rankllm, none; got %q", c.Reranker.Provider)
	}
	if c.Reranker.Provider != "none" && c.Reranker.Provider != "" {
		if c.Reranker.Endpoint == "" {
			return fmt.Errorf("reranker.endpoint is required when provider is %q", c.Reranker.Provider)
		}
		if c.Reranker.TopK < 1 {
			return fmt.Errorf("reranker.topK must be >= 1, got %d", c.Reranker.TopK)
		}
	}

	// OTel validation.
	if c.OTel.Enabled {
		validTraceExporters := map[string]bool{"otlp": true, "stdout": true, "none": true}
		if !validTraceExporters[c.OTel.TraceExporter] {
			return fmt.Errorf("otel.traceExporter must be one of: otlp, stdout, none; got %q", c.OTel.TraceExporter)
		}
		validMetricExporters := map[string]bool{"prometheus": true, "otlp": true, "stdout": true, "none": true}
		if !validMetricExporters[c.OTel.MetricExporter] {
			return fmt.Errorf("otel.metricExporter must be one of: prometheus, otlp, stdout, none; got %q", c.OTel.MetricExporter)
		}
		if c.OTel.SampleRate < 0 || c.OTel.SampleRate > 1 {
			return fmt.Errorf("otel.sampleRate must be between 0.0 and 1.0, got %f", c.OTel.SampleRate)
		}
	}

	// Provenance ClickHouse validation.
	if c.Provenance.ClickHouse.Enabled {
		if c.Provenance.ClickHouse.Endpoint == "" {
			return fmt.Errorf("provenance.clickhouse.endpoint is required when enabled")
		}
		if c.Provenance.ClickHouse.BatchSize < 1 {
			return fmt.Errorf("provenance.clickhouse.batchSize must be >= 1, got %d", c.Provenance.ClickHouse.BatchSize)
		}
	}

	return nil
}

// ConfigPath returns the config file path from the environment, or the
// given default.
func ConfigPath(fallback string) string {
	if v := os.Getenv("PALENA_CONFIG_PATH"); v != "" {
		return v
	}
	return fallback
}
