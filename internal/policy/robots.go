// Copyright (c) 2026 bitkaio LLC. All rights reserved.
// Licensed under the Apache License, Version 2.0.

package policy

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/temoto/robotstxt"

	"github.com/bitkaio/palena-websearch-mcp/internal/config"
	"github.com/bitkaio/palena-websearch-mcp/internal/search"
)

const robotsUserAgent = "Palena"

// robotsEntry is a cached robots.txt parse result for a single domain.
type robotsEntry struct {
	group     *robotstxt.Group
	fetchedAt time.Time
	err       error
}

// RobotsChecker fetches, caches, and evaluates robots.txt rules per domain.
type RobotsChecker struct {
	cache  sync.Map // map[string]*robotsEntry (domain → entry)
	ttl    time.Duration
	client *http.Client
	logger *slog.Logger
}

// NewRobotsChecker creates a RobotsChecker from the policy configuration.
// Returns nil if robots.txt enforcement is disabled.
func NewRobotsChecker(cfg config.PolicyConfig, logger *slog.Logger) *RobotsChecker {
	if !cfg.Robots.Enabled {
		logger.Info("policy: robots.txt enforcement disabled")
		return nil
	}

	ttl := time.Duration(cfg.Robots.CacheSeconds) * time.Second

	logger.Info("policy: robots.txt checker initialized",
		"cache_ttl", ttl,
	)

	return &RobotsChecker{
		ttl: ttl,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
		logger: logger,
	}
}

// IsAllowed checks whether the given URL is permitted by its domain's
// robots.txt. Returns (allowed, checked) where checked indicates whether
// robots.txt was successfully evaluated. On fetch failure, returns (true, false)
// for graceful degradation.
func (rc *RobotsChecker) IsAllowed(ctx context.Context, rawURL string) (allowed bool, checked bool) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return true, false
	}

	domain := u.Hostname()
	if domain == "" {
		return true, false
	}

	entry := rc.getOrFetch(ctx, domain)
	if entry.err != nil {
		// Graceful degradation: allow when robots.txt is unavailable.
		return true, false
	}

	path := u.Path
	if path == "" {
		path = "/"
	}
	if u.RawQuery != "" {
		path = path + "?" + u.RawQuery
	}

	return entry.group.Test(path), true
}

// CheckAll filters a slice of search results through robots.txt.
// Returns allowed results and blocked results.
func (rc *RobotsChecker) CheckAll(ctx context.Context, results []search.SearchResult) (allowed, blocked []search.SearchResult) {
	for _, r := range results {
		ok, checked := rc.IsAllowed(ctx, r.URL)
		if !ok && checked {
			rc.logger.Info("policy: robots.txt disallowed",
				"url", r.URL,
			)
			blocked = append(blocked, r)
		} else {
			allowed = append(allowed, r)
		}
	}
	return allowed, blocked
}

// getOrFetch returns a cached robots.txt entry for the domain, fetching it if
// the cache is empty or expired.
func (rc *RobotsChecker) getOrFetch(ctx context.Context, domain string) *robotsEntry {
	if v, ok := rc.cache.Load(domain); ok {
		entry := v.(*robotsEntry)
		if time.Since(entry.fetchedAt) < rc.ttl {
			return entry
		}
		// Expired; fall through to refetch.
	}

	entry := rc.fetchRobots(ctx, domain)
	rc.cache.Store(domain, entry)
	return entry
}

// fetchRobots fetches and parses robots.txt for the given domain.
func (rc *RobotsChecker) fetchRobots(ctx context.Context, domain string) *robotsEntry {
	robotsURL := fmt.Sprintf("https://%s/robots.txt", domain)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, robotsURL, nil)
	if err != nil {
		return &robotsEntry{err: err, fetchedAt: time.Now()}
	}

	resp, err := rc.client.Do(req)
	if err != nil {
		rc.logger.Debug("policy: robots.txt fetch failed",
			"domain", domain,
			"error", err,
		)
		return &robotsEntry{err: err, fetchedAt: time.Now()}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return &robotsEntry{err: err, fetchedAt: time.Now()}
	}

	data, err := robotstxt.FromStatusAndBytes(resp.StatusCode, body)
	if err != nil {
		return &robotsEntry{err: err, fetchedAt: time.Now()}
	}

	group := data.FindGroup(robotsUserAgent)

	rc.logger.Debug("policy: robots.txt cached",
		"domain", domain,
		"status", resp.StatusCode,
	)

	return &robotsEntry{
		group:     group,
		fetchedAt: time.Now(),
	}
}
