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
	Server   ServerConfig   `yaml:"server"`
	Search   SearchConfig   `yaml:"search"`
	Scraper  ScraperConfig  `yaml:"scraper"`
	Reranker RerankerConfig `yaml:"reranker"`
	Logging  LoggingConfig  `yaml:"logging"`
	// Future sections: PII, Policy, Output, OTel
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
	ContentDetection ContentDetectionConfig `yaml:"contentDetection"`
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
		ContentDetection: ContentDetectionConfig{
			MinTextLength: 500,
			MinTextRatio:  0.05,
			MaxScriptTags: 5,
		},
	}
	c.Reranker = RerankerConfig{
		Provider: "none",
		Endpoint: "http://reranker:8080",
		TopK:     5,
		Timeout:  10 * time.Second,
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
