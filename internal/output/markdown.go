// Copyright (c) 2026 BITKAIO LLC. All rights reserved.
// Use of this source code is governed by the AGPL-3.0 license.

package output

import (
	"strings"

	md "github.com/JohannesKaufmann/html-to-markdown/v2"
)

// HTMLToMarkdown converts clean HTML (from go-readability) to markdown.
// It preserves headings, paragraphs, lists, code blocks, bold/italic,
// and replaces images with their alt text.
func HTMLToMarkdown(html string) (string, error) {
	if strings.TrimSpace(html) == "" {
		return "", nil
	}

	markdown, err := md.ConvertString(html)
	if err != nil {
		return "", err
	}

	// Normalize excessive blank lines (3+ → 2).
	for strings.Contains(markdown, "\n\n\n") {
		markdown = strings.ReplaceAll(markdown, "\n\n\n", "\n\n")
	}

	return strings.TrimSpace(markdown), nil
}
