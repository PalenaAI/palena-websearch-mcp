// Copyright (c) 2026 BITKAIO LLC. All rights reserved.
// Use of this source code is governed by the AGPL-3.0 license.

package search

import (
	"net/url"
	"sort"
	"strings"
)

// trackingParams are URL query parameters commonly used for tracking that
// should be stripped during normalization for deduplication.
var trackingParams = map[string]struct{}{
	"utm_source":   {},
	"utm_medium":   {},
	"utm_campaign": {},
	"utm_term":     {},
	"utm_content":  {},
	"ref":          {},
	"fbclid":       {},
	"gclid":        {},
	"msclkid":      {},
	"mc_cid":       {},
	"mc_eid":       {},
	"_ga":          {},
}

// NormalizeURL parses a raw URL, lowercases the host, removes trailing slashes,
// strips tracking parameters, sorts remaining query params, and returns the
// canonical string. Returns the original string unchanged on parse errors.
func NormalizeURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}

	// Lowercase scheme and host.
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)

	// Remove trailing slashes from path (but keep "/" for root).
	u.Path = strings.TrimRight(u.Path, "/")
	if u.Path == "" {
		u.Path = "/"
	}

	// Strip fragment.
	u.Fragment = ""

	// Strip tracking params and sort the rest.
	orig := u.Query()
	cleaned := url.Values{}
	for k, v := range orig {
		if _, tracking := trackingParams[k]; !tracking {
			cleaned[k] = v
		}
	}

	// Sort query parameter keys for deterministic output.
	keys := make([]string, 0, len(cleaned))
	for k := range cleaned {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	sorted := url.Values{}
	for _, k := range keys {
		sorted[k] = cleaned[k]
	}
	u.RawQuery = sorted.Encode()

	return u.String()
}

// Deduplicate merges search results that share the same normalized URL.
// For duplicates: keep the highest score, merge engine lists, prefer the
// longest snippet. Preserves the order of first occurrence.
func Deduplicate(results []SearchResult) []SearchResult {
	type entry struct {
		result SearchResult
		index  int // insertion order
	}

	seen := make(map[string]*entry, len(results))
	var order []string

	for _, r := range results {
		key := NormalizeURL(r.URL)

		if existing, ok := seen[key]; ok {
			// Merge: keep highest score.
			if r.Score > existing.result.Score {
				existing.result.Score = r.Score
			}
			// Merge engine lists (deduplicated).
			existing.result.Engines = mergeEngines(existing.result.Engines, r.Engines)
			// Keep longest snippet.
			if len(r.Snippet) > len(existing.result.Snippet) {
				existing.result.Snippet = r.Snippet
			}
		} else {
			seen[key] = &entry{result: r, index: len(order)}
			order = append(order, key)
		}
	}

	deduped := make([]SearchResult, 0, len(order))
	for _, key := range order {
		deduped = append(deduped, seen[key].result)
	}
	return deduped
}

// mergeEngines combines two engine slices, removing duplicates.
func mergeEngines(a, b []string) []string {
	set := make(map[string]struct{}, len(a)+len(b))
	for _, e := range a {
		set[e] = struct{}{}
	}
	for _, e := range b {
		set[e] = struct{}{}
	}
	merged := make([]string, 0, len(set))
	for e := range set {
		merged = append(merged, e)
	}
	sort.Strings(merged)
	return merged
}
