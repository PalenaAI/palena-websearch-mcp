# Copyright (c) 2026 BITKAIO LLC. All rights reserved.
# Use of this source code is governed by the AGPL-3.0 license.

"""FlashRank reranker sidecar — thin Flask wrapper around the FlashRank library.

Expects POST /rerank with JSON body:
  {"query": "...", "documents": [{"content": "..."}, ...]}

Returns JSON:
  {"results": [{"index": 0, "score": 0.94}, ...]}
"""

import os
from flask import Flask, request, jsonify
from flashrank import Ranker, RerankRequest

app = Flask(__name__)

model_name = os.environ.get("FLASHRANK_MODEL", "ms-marco-MiniLM-L-12-v2")
ranker = Ranker(model_name=model_name)


@app.route("/health", methods=["GET"])
def health():
    return jsonify({"status": "ok", "model": model_name})


@app.route("/rerank", methods=["POST"])
def rerank():
    data = request.json
    query = data.get("query", "")
    documents = data.get("documents", [])

    if not query or not documents:
        return jsonify({"error": "query and documents are required"}), 400

    passages = [
        {"id": i, "text": doc.get("content", "")}
        for i, doc in enumerate(documents)
    ]

    rerank_request = RerankRequest(query=query, passages=passages)
    results = ranker.rerank(rerank_request)

    return jsonify({
        "results": [
            {"index": r["id"], "score": float(r["score"])}
            for r in results
        ]
    })


if __name__ == "__main__":
    port = int(os.environ.get("PORT", "8080"))
    app.run(host="0.0.0.0", port=port)