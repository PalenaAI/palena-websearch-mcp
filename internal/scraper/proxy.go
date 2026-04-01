// Copyright (c) 2026 BITKAIO LLC. All rights reserved.
// Use of this source code is governed by the AGPL-3.0 license.

package scraper

import (
	"log/slog"
	"sync"
	"time"

	"github.com/bitkaio/palena-websearch-mcp/internal/config"
)

// ProxyPool manages a round-robin pool of proxies with cooldown on failure.
type ProxyPool struct {
	mu       sync.Mutex
	proxies  []proxyEntry
	index    int
	cooldown time.Duration
	logger   *slog.Logger
}

type proxyEntry struct {
	url      string
	region   string
	failedAt time.Time // zero value = not in cooldown
}

// NewProxyPool creates a proxy pool from the configured entries.
// Returns nil if proxy is disabled or the pool is empty.
func NewProxyPool(cfg config.ProxyConfig, logger *slog.Logger) *ProxyPool {
	if !cfg.Enabled || len(cfg.Pool) == 0 {
		return nil
	}

	entries := make([]proxyEntry, len(cfg.Pool))
	for i, p := range cfg.Pool {
		entries[i] = proxyEntry{
			url:    p.URL,
			region: p.Region,
		}
	}

	cooldown := time.Duration(cfg.CooldownSeconds) * time.Second
	if cooldown <= 0 {
		cooldown = 5 * time.Minute
	}

	return &ProxyPool{
		proxies:  entries,
		cooldown: cooldown,
		logger:   logger,
	}
}

// Next returns the next available proxy URL using round-robin rotation,
// skipping proxies that are in cooldown. Returns "" if no proxies are available.
func (p *ProxyPool) Next() string {
	if p == nil || len(p.proxies) == 0 {
		return ""
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	n := len(p.proxies)

	// Try all proxies starting from current index.
	for i := 0; i < n; i++ {
		idx := (p.index + i) % n
		entry := &p.proxies[idx]

		if !entry.failedAt.IsZero() && now.Before(entry.failedAt.Add(p.cooldown)) {
			continue // still in cooldown
		}

		// Reset cooldown if expired.
		entry.failedAt = time.Time{}

		// Advance index past this one for next call.
		p.index = (idx + 1) % n
		return entry.url
	}

	p.logger.Warn("all proxies in cooldown, none available")
	return ""
}

// MarkFailed puts a proxy into cooldown after a failure.
func (p *ProxyPool) MarkFailed(proxyURL string) {
	if p == nil {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	for i := range p.proxies {
		if p.proxies[i].url == proxyURL {
			p.proxies[i].failedAt = time.Now()
			p.logger.Info("proxy marked as failed, entering cooldown",
				"proxy", proxyURL,
				"cooldown", p.cooldown.String(),
			)
			return
		}
	}
}
