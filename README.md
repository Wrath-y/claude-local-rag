<div align="center">

# 🧠 Claude Local RAG

**Give Claude Code persistent long-term memory — retrieve knowledge precisely from your own documents**

[![Python](https://img.shields.io/badge/Python-3.8+-3776AB?style=flat-square&logo=python&logoColor=white)](https://www.python.org/)
[![FastAPI](https://img.shields.io/badge/FastAPI-0.100+-009688?style=flat-square&logo=fastapi&logoColor=white)](https://fastapi.tiangolo.com/)
[![FAISS](https://img.shields.io/badge/FAISS-Vector_Search-blue?style=flat-square)](https://github.com/facebookresearch/faiss)
[![License](https://img.shields.io/badge/License-MIT-green?style=flat-square)](LICENSE)
[![Claude Code](https://img.shields.io/badge/Claude_Code-Plugin-orange?style=flat-square)](https://claude.ai/code)

[Installation](#installation) · [Usage](#usage) · [How It Works](#how-it-works-how-the-prompt-is-modified) · [Command Reference](#command-reference) · [FAQ](#faq)

📖 [中文文档](README_zh.md)

</div>

---

## Why Do You Need This?

Claude Code has no built-in vector database. Its native memory system is file-based (CLAUDE.md) and **lacks semantic retrieval**.

| Native Limitation | Symptom | This Plugin's Solution |
|-------------------|---------|------------------------|
| Forget on session close | New conversation — Claude knows nothing about last session | Documents stored in a local vector DB, persisted forever, always available |
| Large docs burn tokens | Pasting a 100-page manual into the chat costs a fortune just reading the doc | Only retrieves relevant excerpts; the rest is never transmitted |
| No cross-document semantic search | Claude can't "remember" multiple documents and search across them semantically | All ingested documents are indexed together, returning the most relevant content by meaning |

> 🔒 **All data is stored locally. Nothing is uploaded to any server.**

---

## Installation

### Prerequisites

| Tool | macOS / Linux | Windows |
|------|--------------|---------|
| [Python 3.8+](https://www.python.org/downloads/) | Required | Required |
| [Node.js 16+](https://nodejs.org) (optional) | Feishu doc ingestion | Feishu doc ingestion |
| [curl](https://curl.se) | Built-in | Built-in on Windows 10 1803+ |

### One-Command Install

**macOS / Linux**

```bash
git clone https://github.com/Wrath-y/claude-local-rag
cd claude-local-rag
./start.sh
```

**Windows** (Command Prompt / PowerShell, run as Administrator)

```bat
git clone https://github.com/Wrath-y/claude-local-rag
cd claude-local-rag
start.bat
```

The script automatically installs dependencies, registers Hooks, and starts the service. When you see the following message, the setup is complete:

```
安装完成！重启 Claude Code 后即可开箱即用。
```

**Restart Claude Code to start using it.**

> The script is idempotent — safe to re-run. If you move the project directory, re-run the script to update paths.

---

## Usage

All operations are done inside the Claude Code chat box. Type `/rag` to trigger command autocompletion.

### 📥 Ingest Documents

```bash
/rag your document text...                       # Paste text directly
/rag https://xxx.feishu.cn/docx/xxx              # Feishu doc link
/rag https://example.com/docs/api                # Any web URL
/rag /path/to/file.txt                           # Local file (.txt .md .pdf, etc.)
/rag /path/to/file.txt --source product-manual   # Custom source label
```

| Input Type | Auto-inferred Source Label |
|------------|---------------------------|
| Plain text | `manual` |
| Feishu doc link | Link URL |
| Any web URL | Link URL |
| Local file path | Filename (e.g. `manual.txt`) |

> 📌 Retrieval results show `[source: xxx]`. You can also delete by source.

---

### 🔍 Retrieve from Knowledge Base

```bash
/rag-retrieve How do I handle Redis cache penetration?
```

---

### ⚡ Auto-Retrieve Mode

When enabled, every prompt submission automatically queries the knowledge base and injects the results — no manual trigger needed:

```bash
/rag-mode on    # Enable (persistent, survives restarts)
/rag-mode off   # Disable
```

> Driven by a Hook — independent of conversation context. Unaffected by `/clear` or compaction.

---

### 🤖 Auto-Index Code

When enabled, Claude automatically syncs the vector DB whenever it reads or edits source files:

```bash
/rag-auto-index on    # Enable (persistent)
/rag-auto-index off   # Disable
```

| Action | Behavior |
|--------|----------|
| Claude reads a source file | Auto-ingest (deduplicates) |
| Claude edits a source file | Delete old chunks + re-ingest |

> Only processes source files (`.py` `.ts` `.go` `.java` `.rs` etc.). Skips files > 100 KB.

---

### 🔄 Update a Document

Re-sync after content changes — one command replaces the two-step "delete + re-ingest" flow:

```bash
/rag-update https://xxx.feishu.cn/docx/xxx
/rag-update /path/to/file.txt --source product-manual
```

---

### 📊 Manage Knowledge Base

```bash
/rag-status                        # Service status, chunk count, retrieval hit rate
/rag-sources                       # List all sources with chunk counts
/rag-source-delete <source-name>   # Delete a source (confirmation prompt)
/rag-reset                         # Clear entire knowledge base (confirmation prompt)
/rag-export ~/backup.zip           # Export knowledge base as zip (for migration)
/rag-import ~/backup.zip           # Import from backup (confirmation prompt, replaces current data)
```

---

### 🎯 Rerank

When enabled, retrieval results are re-ranked by a cross-encoder for higher relevance precision:

```bash
/rag-rerank on    # Enable
/rag-rerank off   # Disable
```

> First enable downloads `BAAI/bge-reranker-base` (~400 MB). Subsequent runs reuse the loaded model in-process. Adds ~50–200 ms per retrieval; consumes no tokens.

---

## Command Reference

| Command | Description | Extra Tokens |
|---------|-------------|:------------:|
| `/rag <content or link> [--source <name>]` | Ingest document; `--source` overrides auto-inferred label | — |
| `/rag-update <link or path> [--source <name>]` | Update source (delete old + re-ingest); `--source` must match original label | — |
| `/rag-retrieve <question>` | Manual retrieval | ✓ Small |
| `/rag-mode on/off` | Auto-retrieve mode | ✓ When on |
| `/rag-auto-index on/off` | Auto-index code files | — |
| `/rag-rerank on/off` | Cross-encoder rerank | — |
| `/rag-verbose on/off` | Retrieval observability logs | — |
| `/rag-status` | Service status + chunk count + hit rate | — |
| `/rag-sources` | All sources with chunk counts | — |
| `/rag-source-delete <name>` | Delete by source (exact name match required) | — |
| `/rag-reset` | Clear entire knowledge base | — |
| `/rag-export [path]` | Export as zip backup (default: `~/rag_backup.zip`) | — |
| `/rag-import <zip-path>` | Import from zip backup, replacing current data (confirmation step) | — |

---

## How It Works: How the Prompt Is Modified

RAG intercepts the prompt via the **Claude Code Hook mechanism**, injecting retrieval results before it is sent to the model:

```
User submits prompt
    ↓
UserPromptSubmit Hook (hook_script.py)
    ├─ rag-mode off → pass through unchanged
    └─ rag-mode on  → POST /retrieve
                        ↓
                      outputs additionalContext
                        ↓
                      injected into system prompt area
                        ↓
                      model sees: [system prompt] + [RAG results] + [user prompt]
```

`additionalContext` is injected at the system prompt layer — **visible to the model, not shown to the user** — without altering the conversation structure.

### Retrieval Pipeline

```
User question
    ↓
① FAISS vector search (top_k × 3 candidates, cosine similarity)
    ↓
② Threshold filter (discard similarity < 0.45)
    ↓
③ BM25 hybrid scoring (final = vec × 0.7 + bm25 × 0.3) → take top_k
    ↓
④ Cross-Encoder Rerank (optional, re-orders top_k results)
    ↓
Final chunks injected into system prompt
```

### Ingestion Pipeline

```
Raw text (pasted / file / URL / Feishu doc)
    ↓
Chunk splitting (sentence boundaries, 200–400 tokens/chunk, 2-sentence overlap between adjacent chunks)
    ↓
Embedding cache hit? → Yes: reuse vector; No: encode with BGE model
    ↓
FAISS IndexFlatIP write + BM25 index rebuild
    ↓
Persist (index.bin + chunks.pkl)
```

> **Key optimization**: The embedding cache is automatically restored from FAISS vectors at startup — no re-encoding needed after restart. When a source is deleted and the index is rebuilt, all remaining chunks hit the cache. The cache is pruned on deletion to prevent unbounded growth; `/rag-update` never causes cache bloat.

### Retrieval Observability

```bash
/rag-verbose on    # Enable detailed logging
tail -f /tmp/claude-local-rag.log
```

```
[retrieve] query: 'GET /api/v2/orders returns 403, help me debug'
[retrieve] FAISS returned 9 candidates (total: 137)
[retrieve] threshold filter (< 0.45) discarded 6, remaining 3
  vec=0.774 bm25=0.600 final=0.722 [api-spec.md] '/api/v2/orders requires scope: orders:read...'
  vec=0.691 bm25=0.400 final=0.604 [auth-guide.md] 'Bearer Token missing permission returns 403...'
  vec=0.652 bm25=0.200 final=0.516 [changelog.md] 'v2.3.0 added IP allowlist check'
[retrieve] after rerank:
  rerank=0.912 'Bearer Token missing permission returns 403...'
  rerank=0.743 '/api/v2/orders requires scope: orders:read...'
[retrieve] returning 3 chunks
```

Each candidate shows vector similarity (`vec`), BM25 keyword score (`bm25`), hybrid score (`final`), source, and a text preview.

### Real-World Example

A team stores internal API docs, interface specs, and release checklists in the vector DB, then enables `rag-mode on`.

User types:
```
GET /api/v2/orders returns 403, help me debug
```

What Claude actually receives (invisible to the user):
```
[RAG auto-retrieval results]
[source: api-spec.md]
/api/v2/orders requires scope: orders:read. The caller must declare this scope when requesting a token...
---
[source: auth-guide.md]
When a Bearer Token lacks permission, a 403 is returned. Check the token's scope list...
---
[source: changelog.md]
v2.3.0 added an IP allowlist check to /orders. Non-allowlisted IPs also return 403...
```

**Result:** Claude pinpoints scope misconfiguration and IP allowlist as the two suspects immediately, without guessing from scratch.

---

## Service Management

**macOS / Linux**

```bash
./start.sh                          # Install dependencies + start service
./stop.sh                           # Stop service
tail -f /tmp/claude-local-rag.log   # View logs
```

**Windows**

```bat
start.bat                                    # Install dependencies + start service
stop.bat                                     # Stop service
type %TEMP%\claude-local-rag.log             # View logs
```

---

## Configuration

Edit `config.yaml` to tune parameters:

| Parameter | Default | Description |
|-----------|---------|-------------|
| `chunk.min_tokens` | `200` | Minimum tokens per chunk |
| `chunk.max_tokens` | `400` | Maximum tokens per chunk |
| `retrieve.top_k` | `3` | Number of chunks returned per retrieval |
| `retrieve.verbose` | `true` | Enable retrieval logs |
| `rerank.enabled` | `false` | Enable rerank by default |
| `rerank.model` | `BAAI/bge-reranker-base` | Rerank model |
| `model.name` | `BAAI/bge-small-zh-v1.5` | Embedding Model |
| `embedding.doc_prefix` | `段落：` | Prefix prepended to text at ingestion time (BGE-specific) |
| `embedding.query_prefix` | `查询：` | Prefix prepended to query at retrieval time (BGE-specific) |

### Switching the Embedding Model

`model.name` accepts any `sentence-transformers`-compatible model. Edit `config.yaml` and restart the service to apply. **You must run `/rag-reset` before switching models** — different models produce incompatible vector spaces, so searching a new model's index with old vectors will return garbage results.

`doc_prefix` / `query_prefix` are BGE-specific prefixes. When switching to a non-BGE model, clear both:

```yaml
embedding:
  doc_prefix: ""
  query_prefix: ""
```

Common alternatives:

| Model | Dim | Language | Notes |
|-------|-----|----------|-------|
| `BAAI/bge-small-zh-v1.5` (default) | 512 | Chinese | Small, fast |
| `BAAI/bge-base-zh-v1.5` | 768 | Chinese | Higher accuracy, larger |
| `BAAI/bge-small-en-v1.5` | 512 | English | Best for English docs |
| `BAAI/bge-m3` | 1024 | Multilingual | Best for mixed Chinese/English, slower |
| `sentence-transformers/all-MiniLM-L6-v2` | 384 | English | Generic English; clear both prefixes |

### Why Is `top_k` 3 by Default?

`top_k = 3` balances **recall** against **token cost**:

- **Token budget**: Each chunk is ~200–400 tokens; 3 chunks total ~600–1200 tokens — retrieval results don't dominate the context
- **Quality floor**: Three-layer filtering (vector → hybrid scoring → rerank) means 3 high-quality results outperform 10 mixed-quality candidates
- **"Lost in the Middle"**: LLMs are known to pay less attention to content in the middle of long contexts — more chunks can actually hurt accuracy

| Scenario | Suggested Value |
|----------|----------------|
| Default / general | `3` |
| Broad topic, multi-document synthesis | `5` |
| Strict token budget | `1–2` |
| Not recommended above | `8` |

---

## Project Structure

```
claude-local-rag/
├── server.py                   # FastAPI backend service
├── config.yaml                 # Configuration file
├── requirements.txt            # Python dependencies
├── setup_hook.py               # Cross-platform Hook registration (called by start.sh / start.bat)
├── start.sh                    # One-command install script (macOS / Linux)
├── stop.sh                     # Stop service script (macOS / Linux)
├── start.bat                   # One-command install script (Windows)
├── stop.bat                    # Stop service script (Windows)
├── index.bin                   # Vector index (auto-generated)
├── chunks.pkl                  # Document store (auto-generated)
└── .claude/
    ├── settings.json           # Hook configuration
    ├── hook_script.py          # UserPromptSubmit Hook
    ├── auto_index_hook.py      # PostToolUse Hook (auto-index code)
    └── commands/               # Slash command definitions
        ├── rag.md
        ├── rag-retrieve.md
        ├── rag-mode.md
        ├── rag-auto-index.md
        └── ...
```

---

## Roadmap

> Checked items are implemented. Open items are planned improvements. Contributions via Issue or PR are welcome.

**Retrieval Quality**

- [x] Vector semantic search (FAISS + BGE Embedding)
- [x] BM25 hybrid scoring (vec × 0.7 + bm25 × 0.3) — improves long-tail keyword recall
- [x] Cross-Encoder Rerank
- [ ] Chunk head/tail overlap — prevent semantic truncation at boundaries
- [ ] Semantic chunking (split on paragraph/topic boundaries instead of sentence boundaries)
- [x] Dynamic top_k (auto-adjust based on remaining context window)

**Knowledge Base Management**

- [x] Source-based management (ingest / update / delete)
- [x] Feishu docs, local files, plain text ingestion
- [x] Auto-index code files (PostToolUse Hook)
- [x] Embedding cache (skip re-encoding identical content, speeds up repeated ingestion)
- [ ] Scheduled re-indexing (watch file changes, auto-trigger `/rag-update`)
- [x] Export / Import (backup `index.bin` + `chunks.pkl` and migrate)

**Document Format Support**

- [x] Plain text / Markdown
- [x] Feishu cloud docs
- [x] PDF parsing
- [ ] Word / Excel parsing
- [x] Web URL scraping (non-Feishu)

**Observability & Tuning**

- [x] Retrieval observability logs (vec / bm25 / final scores per candidate)
- [x] `/rag-verbose on/off` toggle
- [ ] Web management UI (visualize chunks, test retrieval)
- [x] Retrieval hit rate statistics

---

## FAQ

<details>
<summary><b>Q: The /rag command doesn't show autocomplete?</b></summary>

Restart Claude Code and make sure you have run `./start.sh` (or `start.bat` on Windows).
</details>

<details>
<summary><b>Q: "Service not running" error?</b></summary>

Run `./start.sh` to restart the service, or check the logs:

```bash
tail -f /tmp/claude-local-rag.log
```
</details>

<details>
<summary><b>Q: Feishu documents can't be read?</b></summary>

You need to install and configure lark-cli first. See the [official docs](https://www.feishu.cn/content/article/7623291503305083853).
</details>

<details>
<summary><b>Q: Retrieval results are inaccurate?</b></summary>

- Store paragraphs with clear structure and complete semantics
- Avoid ingesting table screenshots or scanned text
- For larger knowledge bases, enable `/rag-rerank on` for better precision
- Use `/rag-verbose on` and inspect the logs to analyze specific hit behavior
</details>

<details>
<summary><b>Q: Does RAG still work after /clear?</b></summary>

Yes. `/clear` only clears the conversation context — it does not affect the vector store, persistent flag files (`rag_mode`, etc.), or the background service.
</details>

<details>
<summary><b>Q: Why no Query Rewriting?</b></summary>

Query Rewriting uses an LLM to rewrite the user's question into a form better suited for retrieval. It's a common RAG enhancement technique. This project **intentionally omits it** for three reasons:

- **Conflicts with the core goal of saving tokens**: Each rewrite requires an extra LLM call, increasing cost rather than reducing it
- **Synchronous Hook is unsuitable for LLM calls**: The `UserPromptSubmit` Hook is a synchronous intercept — an LLM call would introduce noticeable latency
- **Three-layer filtering already covers most cases**: Vector semantic search → hybrid BM25 scoring → Rerank handles the vast majority of real-world retrieval scenarios

> For ambiguous queries (e.g. "what about that one?"), use `/rag-retrieve <full question>` to retrieve manually — this works better than relying on auto mode.
</details>
