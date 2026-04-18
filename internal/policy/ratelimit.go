// Copyright (c) 2026 BITKAIO LLC. All rights reserved.
// Licensed under the Apache License, Version 2.0.

package policy

import (
	"log/slog"
	"sync"
	"time"

	"github.com/bitkaio/palena-websearch-mcp/internal/config"
	"github.com/bitkaio/palena-websearch-mcp/internal/search"
)

// domainBucket is a per-domain token bucket that refills every minute.
type domainBucket struct {
	mu         sync.Mutex
	tokens     int
	lastRefill time.Time
}

// RateLimiter enforces per-domain request rate limits using a token bucket.
type RateLimiter struct {
	buckets      sync.Map // map[string]*domainBucket
	maxPerMinute int
	logger       *slog.Logger
}

// NewRateLimiter creates a RateLimiter from the policy configuration.
// Returns nil if rate limiting is disabled.
func NewRateLimiter(cfg config.PolicyConfig, logger *slog.Logger) *RateLimiter {
	if !cfg.RateLimit.Enabled {
		logger.Info("policy: rate limiter disabled")
		return nil
	}

	logger.Info("policy: rate limiter initialized",
		"requests_per_domain_per_minute", cfg.RateLimit.RequestsPerDomainPerMinute,
	)

	return &RateLimiter{
		maxPerMinute: cfg.RateLimit.RequestsPerDomainPerMinute,
		logger:       logger,
	}
}

// Allow checks whether a request to the given domain is within the rate limit.
// Consumes one token if allowed.
func (rl *RateLimiter) Allow(domain string) bool {
	v, _ := rl.buckets.LoadOrStore(domain, &domainBucket{
		tokens:     rl.maxPerMinute,
		lastRefill: time.Now(),
	})
	bucket := v.(*domainBucket)

	bucket.mu.Lock()
	defer bucket.mu.Unlock()

	// Refill if more than one minute has passed.
	if time.Since(bucket.lastRefill) >= time.Minute {
		bucket.tokens = rl.maxPerMinute
		bucket.lastRefill = time.Now()
	}

	if bucket.tokens <= 0 {
		return false
	}

	bucket.tokens--
	return true
}

// FilterAll applies rate limiting to a slice of search results.
// Returns allowed results and rate-limited results.
func (rl *RateLimiter) FilterAll(results []search.SearchResult) (allowed, limited []search.SearchResult) {
	for _, r := range results {
		host := extractHost(r.URL)
		if host == "" {
			// Cannot parse URL; allow it.
			allowed = append(allowed, r)
			continue
		}

		if rl.Allow(host) {
			allowed = append(allowed, r)
		} else {
			rl.logger.Info("policy: rate limit exceeded",
				"url", r.URL,
				"domain", host,
			)
			limited = append(limited, r)
		}
	}
	return allowed, limited
}
