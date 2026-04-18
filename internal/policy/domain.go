// Copyright (c) 2026 bitkaio LLC. All rights reserved.
// Licensed under the Apache License, Version 2.0.

package policy

import (
	"log/slog"
	"net/url"
	"strings"

	"github.com/bitkaio/palena-websearch-mcp/internal/config"
	"github.com/bitkaio/palena-websearch-mcp/internal/search"
)

// DomainFilter enforces domain allowlist/blocklist policies on search results.
type DomainFilter struct {
	mode      string   // "allowlist" or "blocklist"
	allowlist []string // lowercase domain suffixes
	blocklist []string // lowercase domain suffixes
	logger    *slog.Logger
}

// NewDomainFilter creates a DomainFilter from the policy configuration.
// Returns nil if both lists are empty and mode is blocklist (no filtering needed).
func NewDomainFilter(cfg config.PolicyConfig, logger *slog.Logger) *DomainFilter {
	domains := cfg.Domains

	// Normalize all patterns to lowercase.
	allow := make([]string, len(domains.Allowlist))
	for i, d := range domains.Allowlist {
		allow[i] = strings.ToLower(strings.TrimSpace(d))
	}
	block := make([]string, len(domains.Blocklist))
	for i, d := range domains.Blocklist {
		block[i] = strings.ToLower(strings.TrimSpace(d))
	}

	// If blocklist mode with no entries, no filtering is needed.
	if domains.Mode == "blocklist" && len(block) == 0 {
		logger.Info("policy: domain filter disabled (blocklist mode with no entries)")
		return nil
	}

	logger.Info("policy: domain filter initialized",
		"mode", domains.Mode,
		"allowlist_count", len(allow),
		"blocklist_count", len(block),
	)

	return &DomainFilter{
		mode:      domains.Mode,
		allowlist: allow,
		blocklist: block,
		logger:    logger,
	}
}

// Filter applies the domain policy to a slice of search results.
// Returns the allowed results and the dropped results.
func (f *DomainFilter) Filter(results []search.SearchResult) (allowed, dropped []search.SearchResult) {
	for _, r := range results {
		host := extractHost(r.URL)
		if host == "" {
			// Cannot parse URL; keep it (fail open).
			allowed = append(allowed, r)
			continue
		}

		switch f.mode {
		case "blocklist":
			if matchesDomainSuffix(host, f.blocklist) {
				f.logger.Info("policy: domain blocked",
					"url", r.URL,
					"domain", host,
					"reason", "blocklist",
				)
				dropped = append(dropped, r)
			} else {
				allowed = append(allowed, r)
			}
		case "allowlist":
			if matchesDomainSuffix(host, f.allowlist) {
				allowed = append(allowed, r)
			} else {
				f.logger.Info("policy: domain blocked",
					"url", r.URL,
					"domain", host,
					"reason", "not on allowlist",
				)
				dropped = append(dropped, r)
			}
		default:
			// Unknown mode; fail open.
			allowed = append(allowed, r)
		}
	}
	return allowed, dropped
}

// extractHost parses a URL and returns the lowercase host without port.
func extractHost(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	return host
}

// matchesDomainSuffix checks whether host matches any of the given domain
// patterns by suffix. For example, "docs.github.com" matches "github.com".
func matchesDomainSuffix(host string, patterns []string) bool {
	for _, p := range patterns {
		if host == p {
			return true
		}
		// Check if host ends with "."+pattern (subdomain match).
		if strings.HasSuffix(host, "."+p) {
			return true
		}
	}
	return false
}
