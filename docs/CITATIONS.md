# Citation Contract

Retrieval creates a short-lived, request-scoped evidence manifest. Citation
labels are deterministic ordinals in the final ranked order of that one
retrieval: `[1]` through `[N]`. They are not document IDs and must never be
carried over to another retrieval request.

## Retrieval responses

`POST /retrieve` retains the existing `chunks` array for compatibility and
also returns:

```json
{
  "chunks": ["[1] [source marker: redis.md]\\n..."],
  "evidence_token": "opaque-short-lived-token",
  "citations": [{
    "id": 1,
    "label": "[1]",
    "source": "redis.md",
    "title": "Redis guide",
    "uri": "/docs/redis.md",
    "location": "section:cache",
    "content_hash": "...",
    "excerpt": "..."
  }]
}
```

The `chunks` entries are display text. Clients should use the structured
`citations` field, rather than parsing a localized source marker, whenever
machine-readable provenance is required.

The MCP `rag_retrieve` result exposes the same `chunks`, `citations`, and
`evidence_token` fields. Callers may supply `title`, `uri`, and `location`
when ingesting through HTTP or MCP. When omitted, the source is used as the
URI/path for newly ingested chunks and a `chunk:N` location is recorded.

## Validating an answer

Send the finished answer to `POST /citations/validate`:

```json
{"evidence_token":"...", "answer":"Redis caches this value [1]."}
```

The result lists detected `labels`, `valid_labels`, `invalid_labels`, and a
`citation_map` containing only evidence from that token's manifest.
`missing_citations` is true when evidence was supplied but the answer has no
`[n]` label. An unknown or expired token returns `404`; manifests are kept in
memory for one hour and do not survive a server restart.

## Grounded-answer behavior

Hook and Agent contexts render the current evidence plus these rules:

- Attach only supplied `[n]` labels to supported factual claims.
- Do not invent, alter, or reuse labels from another request.
- If the available evidence does not establish a claim, explicitly state that
  it is uncertain or not established.

The service validates labels and reports diagnostics; it cannot guarantee that
an LLM follows the instruction.

## Legacy chunks

Existing databases are migrated without reindexing. Their missing title, URI,
or document location remains absent rather than being fabricated. Retrieval
still returns available source, content hash, and excerpt; re-ingest documents
to populate richer provenance.
