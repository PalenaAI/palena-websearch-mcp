// Copyright (c) 2026 BITKAIO LLC. All rights reserved.
// Use of this source code is governed by the AGPL-3.0 license.

package reranker

import "context"

// NoopReranker returns documents in their original search engine order
// without making any API calls. Used when reranking is disabled.
type NoopReranker struct{}

// NewNoopReranker creates a passthrough reranker.
func NewNoopReranker() *NoopReranker {
	return &NoopReranker{}
}

func (r *NoopReranker) Rerank(_ context.Context, _ string, docs []Document) ([]RankedDocument, error) {
	result := make([]RankedDocument, len(docs))
	for i, doc := range docs {
		result[i] = RankedDocument{
			Document: doc,
			Score:    1.0 - float64(i)*0.01,
			Rank:     i + 1,
		}
	}
	return result, nil
}

func (r *NoopReranker) Name() string { return "none" }
