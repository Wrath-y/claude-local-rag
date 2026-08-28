---
name: local-rag
description: Manage this repository's Local RAG knowledge base from Codex. Use when the user asks to ingest, update, retrieve, list, export, import, delete, reset, or configure Local RAG knowledge; or asks to enable automatic RAG retrieval for Codex.
---

# Local RAG

Run all requests from the user's working directory. Start by checking that the
service is available with `curl -fsS http://127.0.0.1:8765/health`; if it is
not, tell the user to run `./start.sh` from the repository root.

Use `$local-rag` explicitly, for example:

```text
$local-rag ingest ./product-guide.pdf --source product-guide
$local-rag retrieve How does source sync handle retries?
$local-rag auto-retrieval on
```

## Operations

Interpret the first operation word as one of the following. Report the useful
response fields and preserve server errors rather than guessing success.

| Operation | Action |
| --- | --- |
| `ingest <content-or-url-or-path> [--source <name>]` | Ingest text, a Feishu document, a web URL, a local document, or a Git repository. |
| `update <url-or-path> [--source <name>]` | Delete the source's existing chunks, then ingest its current contents. |
| `retrieve <question>` | POST the question to `/retrieve` and present matching chunks and sources. |
| `status` / `sources` | Read `/health` plus `/stats` and `/storage/integrity-check`, or list `/sources`. |
| `rerank`, `rewrite`, `verbose`, `dynamic-top-k` | Toggle the corresponding retrieval setting. |
| `export [path]` | Download `/export` to the requested path; default to `~/rag_backup.zip`. |
| `import <zip-path>` | Import a backup only after explicit user confirmation. |
| `delete-source <source>` / `reset` | Destructive operations: inspect the affected count, then require explicit confirmation. |
| `auto-retrieval on|off` | Create or remove `.rag-mode` in the current working directory. |

## Ingest and update

Parse an optional `--source <name>`. When absent, use the URL for Feishu,
the final safe URL for a web page, the basename for a local file, and `manual`
for direct text. Fetch Feishu content with `lark-doc` when that skill is
available. Submit direct text using a JSON body built by `jq -n`; never
interpolate user text into JSON manually.

Send web URLs as `{ "url": ... }`; send local PDF/DOCX/JSON with an explicit
`kind` and `path`; send Git with `kind: "git"`, a credential-free local/remote
path, and optional `ref`/`exclusions`. For an update, identify the exact source,
DELETE `/source?source=<url-encoded-source>`, then ingest the replacement.

## Retrieval controls

Use these endpoints:

```text
POST /rerank/toggle?enabled=true|false
POST /retrieve/verbose?enabled=true|false
POST /retrieve/dynamic-top-k?enabled=true|false
POST /retrieve/query-rewrite  body: {"enabled":true|false,"strategy":"expansion|hyde|multi_query"}
POST /retrieve                 body: {"text":"<question>"}
```

For automatic retrieval, `touch .rag-mode` enables it and `rm .rag-mode`
disables it. Codex's `UserPromptSubmit` hook reads this flag and injects RAG
results as developer context. Tell the user to run `/hooks` once after opening
the repository, review, and trust the project hook; Codex skips untrusted hooks.

## Safety

Before `delete-source`, `reset`, or `import`, show the impact and wait for a
clear confirmation in the conversation. `import` must send multipart form data
with `confirm=true`; the server validates and can roll back, but never assume a
successful restore without checking its response.
