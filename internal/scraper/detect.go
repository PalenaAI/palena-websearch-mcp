// Copyright (c) 2026 BITKAIO LLC. All rights reserved.
// Use of this source code is governed by the AGPL-3.0 license.

package scraper

import (
	"regexp"
	"strings"

	"github.com/bitkaio/palena-websearch-mcp/internal/config"
)

// ContentAssessment captures the heuristic signals used to decide whether
// L0 content is sufficient or needs escalation to a browser-based level.
type ContentAssessment struct {
	HasSubstantialText bool
	TextLength         int
	TextToMarkupRatio  float64
	HasNoscriptWarning bool
	HasEmptyRoot       bool
	ScriptTagCount     int
	SSRFramework       string // "nextjs", "nuxt", "gatsby", or ""
	NeedsJavaScript    bool   // final verdict: should we escalate to L1?
}

var (
	scriptTagRe    = regexp.MustCompile(`(?i)<script[\s>]`)
	noscriptRe     = regexp.MustCompile(`(?i)<noscript[^>]*>(.*?)</noscript>`)
	emptyRootRe    = regexp.MustCompile(`(?i)<div\s+id=["'](root|app|__next)["']\s*>\s*</div>`)
	spaMetaRe      = regexp.MustCompile(`(?i)<meta\s+name=["']fragment["']\s+content=["']!["']`)
	nextDataRe     = regexp.MustCompile(`__NEXT_DATA__`)
	nuxtDataRe     = regexp.MustCompile(`__NUXT__`)
	gatsbyRe       = regexp.MustCompile(`___gatsby`)
	htmlTagStripRe = regexp.MustCompile(`<[^>]*>`)

	jsRequiredPhrases = []string{
		"enable javascript",
		"requires javascript",
		"javascript is required",
		"javascript must be enabled",
		"please enable javascript",
		"you need to enable javascript",
	}
)

// AssessContent runs content-detection heuristics on raw HTML to determine
// whether the L0 extraction produced enough readable text.
func AssessContent(html string, cfg config.ContentDetectionConfig) ContentAssessment {
	a := ContentAssessment{}

	// Count <script> tags.
	a.ScriptTagCount = len(scriptTagRe.FindAllStringIndex(html, -1))

	// Extract visible text (strip all HTML tags).
	visibleText := htmlTagStripRe.ReplaceAllString(html, " ")
	visibleText = strings.Join(strings.Fields(visibleText), " ") // normalise whitespace
	a.TextLength = len(visibleText)

	// Text-to-markup ratio.
	if len(html) > 0 {
		a.TextToMarkupRatio = float64(a.TextLength) / float64(len(html))
	}

	a.HasSubstantialText = a.TextLength >= cfg.MinTextLength

	// Check for empty SPA root elements.
	a.HasEmptyRoot = emptyRootRe.MatchString(html)

	// Check <noscript> warnings.
	for _, match := range noscriptRe.FindAllStringSubmatch(html, -1) {
		if len(match) > 1 {
			inner := strings.ToLower(match[1])
			for _, phrase := range jsRequiredPhrases {
				if strings.Contains(inner, phrase) {
					a.HasNoscriptWarning = true
					break
				}
			}
		}
		if a.HasNoscriptWarning {
			break
		}
	}

	// Detect SSR frameworks.
	switch {
	case nextDataRe.MatchString(html):
		a.SSRFramework = "nextjs"
	case nuxtDataRe.MatchString(html):
		a.SSRFramework = "nuxt"
	case gatsbyRe.MatchString(html):
		a.SSRFramework = "gatsby"
	}

	// Final verdict: needs JavaScript?
	a.NeedsJavaScript = needsJavaScript(a, cfg)

	return a
}

// needsJavaScript applies the escalation rules from the spec.
func needsJavaScript(a ContentAssessment, cfg config.ContentDetectionConfig) bool {
	// SPA meta tag is a strong signal.
	// We don't have the raw HTML here directly, but HasEmptyRoot is equivalent.

	// Thin HTML + heavy scripts.
	if a.TextLength < 200 && a.ScriptTagCount > cfg.MaxScriptTags {
		return true
	}

	// Empty framework root div.
	if a.HasEmptyRoot {
		return true
	}

	// Noscript warning.
	if a.HasNoscriptWarning {
		return true
	}

	// Very low text-to-markup ratio.
	if a.TextToMarkupRatio < cfg.MinTextRatio && a.TextLength < cfg.MinTextLength {
		return true
	}

	// SSR framework with content present → content is fine, no JS needed.
	if a.SSRFramework != "" && a.HasSubstantialText {
		return false
	}

	// Substantial text → content is fine.
	if a.HasSubstantialText {
		return false
	}

	// If we have neither substantial text nor a clear JS signal,
	// default to not escalating (L0 result will just be short).
	return false
}
