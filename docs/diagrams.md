# Local RAG — System Diagrams

## 1. System Architecture Overview

```mermaid
graph TB
    subgraph Agents
        CC[Claude Code]
        Cursor[Cursor / Other Agent]
    end

    subgraph "rag-server binary"
        subgraph "HTTP mode (:8765)"
            GIN[Gin Router]
            HOOK_EP[POST /hook]
            INGEST_EP[POST /ingest]
            RETRIEVE_EP[POST /retrieve]
            MANAGE_EP[Management endpoints]
        end

        subgraph "MCP mode (stdio)"
            MCP[MCP Server<br/>JSON-RPC over stdio]
        end

        subgraph "Core layer"
            CHUNKER[Chunker<br/>fixed/structure/semantic/agentic]
            STORE[SQLite Store<br/>vec0 + FTS5]
            PROVIDER[Provider layer<br/>Embed / Rerank / LLM]
        end
    end

    subgraph "External services"
        SIDECAR[Python Sidecar :8766<br/>/embed /rerank]
        OPENAI[OpenAI API]
        ANTHROPIC[Anthropic API]
    end

    CC -->|UserPromptSubmit Hook| HOOK_EP
    CC -->|MCP stdio| MCP
    Cursor -->|MCP stdio| MCP

    GIN --> INGEST_EP & RETRIEVE_EP & MANAGE_EP
    HOOK_EP --> RETRIEVE_EP

    INGEST_EP --> CHUNKER --> PROVIDER
    RETRIEVE_EP --> PROVIDER
    MCP --> CHUNKER & STORE & PROVIDER

    PROVIDER -->|HTTP| SIDECAR
    PROVIDER -->|HTTP| OPENAI
    PROVIDER -->|HTTP| ANTHROPIC

    CHUNKER --> STORE
    RETRIEVE_EP --> STORE
```

---

## 2. Ingest Sequence Diagram

```mermaid
sequenceDiagram
    participant User as User / Agent
    participant Server as rag-server
    participant Chunker as Chunker
    participant Embedder as EmbedProvider
    participant Store as SQLite Store

    User->>Server: POST /ingest {text, source}
    Server->>Server: Validate that text is non-empty

    Server->>Chunker: Chunk(text, source)
    alt strategy = fixed
        Chunker->>Chunker: Split by sentence → merge into token ranges
    else strategy = structure
        Chunker->>Chunker: Detect Markdown atomic blocks → split by heading
    else strategy = semantic
        Chunker->>Embedder: Embed(all sentences)
        Embedder-->>Chunker: [][]float32
        Chunker->>Chunker: Compute adjacent cosine similarity → split at breakpoints
    else strategy = agentic
        Chunker->>Server: LLM.Complete(prompt + text)
        Server-->>Chunker: JSON {chunks: [{start_line, end_line}]}
        Chunker->>Chunker: Parse boundaries → slice text
    end
    Chunker-->>Server: []Chunk

    Server->>Embedder: Embed(chunk texts[])
    Embedder-->>Server: [][]float32

    loop For each chunk
        Server->>Store: InsertChunk(text, source, md5, embedding)
        Store->>Store: Check MD5 deduplication
        alt Not duplicated
            Store->>Store: INSERT chunks + vec_chunks + FTS5 triggers
            Store-->>Server: id > 0
        else Duplicate
            Store-->>Server: id = 0 (skip)
        end
    end

    Server-->>User: {status: "ok", chunks_added: N}
```

---

## 3. Retrieval Sequence Diagram

```mermaid
sequenceDiagram
    participant User as User / Agent
    participant Server as rag-server
    participant Embedder as EmbedProvider
    participant Store as SQLite Store
    participant Reranker as RerankProvider

    User->>Server: POST /retrieve {text, context_tokens_used}
    Server->>Server: Calculate dynamic_top_k (when enabled)

    opt Query Rewrite enabled
        Server->>Server: LLM.Complete(rewrite prompt)
        Note right of Server: expansion / hyde / multi_query
    end

    Server->>Embedder: Embed(query_prefix + text)
    Embedder-->>Server: queryVec []float32

    Server->>Store: Retrieve(queryVec, text, opts)

    par Vector path
        Store->>Store: vec_chunks WHERE embedding MATCH ?<br/>ORDER BY distance LIMIT top_k*10
    and BM25 path
        Store->>Store: chunks_fts MATCH ?<br/>ORDER BY rank LIMIT top_k*10
    end

    Store->>Store: Merge, deduplicate, and apply weighted fusion<br/>final = α·vecSim + (1-α)·bm25Norm
    Store->>Store: Sort → retain the top_k*3 results
    Store->>Store: Hydrate (JOIN the chunks table)
    Store-->>Server: []RetrieveResult

    opt Rerank enabled
        Server->>Reranker: Rerank(query, docs, top_n)
        Reranker-->>Server: []RerankResult{index, score}
        Server->>Server: Reorder by rerank score
    end

    Server->>Server: Format: "[Source: src]\ntext"
    Server-->>User: {chunks: [...]}
```

---

## 4. Hook Automatic Retrieval Sequence Diagram

```mermaid
sequenceDiagram
    participant Claude as Claude Code
    participant Hook as hook.sh
    participant Server as rag-server
    participant Store as SQLite Store

    Claude->>Claude: User enters a prompt
    Claude->>Hook: UserPromptSubmit event<br/>(stdin: {prompt, cwd, transcript_path})

    Hook->>Server: POST /hook (curl --max-time 3)

    Server->>Server: Check the <cwd>/.rag-mode file
    alt .rag-mode does not exist
        Server-->>Hook: {additional_context: ""}
        Hook-->>Claude: (no output; remain silent)
    else .rag-mode exists
        Server->>Server: doRetrieve(prompt)
        Note right of Server: Run the full retrieval flow
        Server-->>Hook: {additional_context: "[RAG automatic retrieval results]\n..."}
        Hook-->>Claude: Output additionalContext JSON
        Claude->>Claude: Model sees: system + RAG results + user prompt
    end

    Note over Hook: Any error → exit 0 (silent; do not block the conversation)
```

---

## 5. MCP Call Sequence Diagram

```mermaid
sequenceDiagram
    participant Agent as Claude Code / Cursor
    participant MCP as rag-server mcp<br/>(stdio JSON-RPC)
    participant Core as Core layer<br/>(Chunker/Store/Provider)

    Agent->>MCP: initialize {protocolVersion, capabilities}
    MCP-->>Agent: {serverInfo, tools: [...]}

    Agent->>MCP: tools/call {name: "rag_retrieve", arguments: {query: "..."}}
    MCP->>Core: Embed(query)
    Core-->>MCP: queryVec
    MCP->>Core: Store.Retrieve(queryVec, text, opts)
    Core-->>MCP: []RetrieveResult
    MCP-->>Agent: {content: [{type: "text", text: "..."}]}

    Agent->>MCP: tools/call {name: "rag_ingest", arguments: {text: "...", source: "..."}}
    MCP->>Core: Chunker.Chunk(text)
    MCP->>Core: Embed(chunks)
    MCP->>Core: Store.InsertChunk(...)
    MCP-->>Agent: {content: [{type: "text", text: "Ingested 5 chunks"}]}
```

---

## 6. Startup Flow Diagram

```mermaid
flowchart TD
    START[./start.sh] --> CHECK_GO{Is Go installed?}
    CHECK_GO -->|No| ERROR_GO[❌ Exit: install Go]
    CHECK_GO -->|Yes| BUILD{Does rag-server need to be built?}
    BUILD -->|Yes| GO_BUILD[go build -o rag-server ./cmd/server/]
    BUILD -->|No| CHECK_PROVIDER

    GO_BUILD --> CHECK_PROVIDER{embedding.provider = local?}
    CHECK_PROVIDER -->|No| START_SERVER
    CHECK_PROVIDER -->|Yes| CHECK_PYTHON{Is Python3 installed?}
    CHECK_PYTHON -->|No| ERROR_PY[❌ Exit: install Python3]
    CHECK_PYTHON -->|Yes| CHECK_VENV{Does sidecar/.venv exist?}
    CHECK_VENV -->|No| SETUP_VENV[Create venv + pip install]
    CHECK_VENV -->|Yes| START_SERVER
    SETUP_VENV --> START_SERVER

    START_SERVER[Start rag-server in the background] --> WAIT_HEALTH{Does /health return 200?}
    WAIT_HEALTH -->|Success within 30s| DONE[✅ Service started]
    WAIT_HEALTH -->|Timed out| WARN[⚠️ Started but not ready]
```

---

## 7. Internal Server Startup Flow Diagram

```mermaid
flowchart TD
    MAIN[main()] --> LOAD_CFG[Load config.yaml]
    LOAD_CFG --> CHECK_MODE{os.Args[1] == "mcp"?}

    CHECK_MODE -->|Yes| MCP_MODE
    CHECK_MODE -->|No| HTTP_MODE

    subgraph MCP_MODE[MCP mode]
        M1[InitLogger error/text] --> M2[Start Sidecar]
        M2 --> M3[Initialize Provider]
        M3 --> M4[Initialize Store]
        M4 --> M5[Initialize Chunker]
        M5 --> M6[Register MCP Tools]
        M6 --> M7[server.Run StdioTransport<br/>Block while waiting for a client]
    end

    subgraph HTTP_MODE[HTTP mode]
        H1[InitLogger] --> H2[Start Sidecar]
        H2 --> H3[Initialize Provider]
        H3 --> H4[Initialize Store]
        H4 --> H5[Initialize Chunker]
        H5 --> H6[Build Handler]
        H6 --> H7[Register 28 Gin routes]
        H7 --> H8[Listen for SIGINT/SIGTERM]
        H8 --> H9[r.Run :8765]
    end
```

---

## 8. Sidecar Lifecycle Flow Diagram

```mermaid
flowchart TD
    START[Manager.Start] --> CHECK{provider == "local"?}
    CHECK -->|No| SKIP[Skip; do not start the sidecar]
    CHECK -->|Yes| SPAWN[Start python3 sidecar/main.py --port 8766]

    SPAWN --> POLL{Poll /health every 500ms}
    POLL -->|200 OK| READY[✅ Sidecar ready]
    POLL -->|Timed out after 30s| FAIL[❌ Startup failed; kill the process]

    READY --> LOOP[Background health-check loop<br/>Probe every 10s]
    LOOP --> HEALTH_OK{/health 200?}
    HEALTH_OK -->|Yes| RESET_FAIL[failures = 0]
    HEALTH_OK -->|No| INC_FAIL[failures++]
    RESET_FAIL --> LOOP
    INC_FAIL --> TOO_MANY{failures >= 3?}
    TOO_MANY -->|No| LOOP
    TOO_MANY -->|Yes| RESTART[Kill + restart]
    RESTART --> SPAWN
```

---

## 9. Degradation Strategy Flow Diagram

```mermaid
flowchart TD
    REQ[Request arrives] --> CHECK_DB{Is SQLite healthy?}
    CHECK_DB -->|No| ERROR_MODE["❌ Error mode<br/>All endpoints return 503"]
    CHECK_DB -->|Yes| CHECK_EMBED{Is the Embedder available?}

    CHECK_EMBED -->|Yes| NORMAL["✅ Normal mode<br/>All features available"]
    CHECK_EMBED -->|No| DEGRADED["⚠️ Degraded mode"]

    DEGRADED --> DEG_INGEST["/ingest → 503"]
    DEGRADED --> DEG_RETRIEVE["/retrieve → BM25 keyword search only"]
    DEGRADED --> DEG_OTHER["Other endpoints → normal"]
```

---

## 10. Hybrid Retrieval Scoring Flow Diagram

```mermaid
flowchart LR
    QUERY[User query] --> EMBED[Embed into a vector]
    QUERY --> FTS[FTS5 tokenization]

    EMBED --> VEC_SEARCH["vec_chunks KNN<br/>top_k × 10 candidates"]
    FTS --> BM25_SEARCH["chunks_fts MATCH<br/>top_k × 10 candidates"]

    VEC_SEARCH --> MERGE[Merge and deduplicate]
    BM25_SEARCH --> MERGE

    MERGE --> SCORE["Weighted fusion<br/>final = 0.7·vec + 0.3·bm25"]
    SCORE --> COARSE["Coarse filter: top_k × 3"]
    COARSE --> RERANK{Is reranking enabled?}
    RERANK -->|Yes| DO_RERANK["CrossEncoder reranking"]
    RERANK -->|No| FINAL
    DO_RERANK --> FINAL["Return top_k results"]
```

---

## 11. Agent frontend interaction (HTTP/JSON)

The browser-facing Agent API is synchronous REST over HTTP with JSON request
and response bodies. It does not use WebSocket or Server-Sent Events for
token streaming. The server enables CORS for browser clients. MCP over stdio
is a separate integration path and is not used by this flow.

```mermaid
sequenceDiagram
    participant UI as Browser frontend
    participant API as rag-server HTTP API
    participant Session as SQLite session store
    participant Loop as Agent tool loop
    participant LLM as LLM provider

    UI->>API: POST /agent/session {metadata?}
    API->>Session: Create session
    Session-->>API: session_id
    API-->>UI: 200 {session_id}

    UI->>API: POST /agent/chat {session_id, message}
    API->>Loop: ChatWithResult(session_id, message)
    Loop->>Session: Load prior messages; append user message
    Loop->>LLM: Complete with the fixed tool registry

    alt Final answer without a tool call
        LLM-->>Loop: Final text
        Loop->>Session: Append assistant message
        Loop-->>API: completed response
    else Read-only retrieval tool call
        LLM-->>Loop: rag_retrieve(query, top_k)
        Loop->>Loop: Validate and execute retrieval
        Loop->>LLM: Tool result and evidence
        LLM-->>Loop: Final text
        Loop-->>API: completed response with citations
    else Mutating tool call
        LLM-->>Loop: ingest, delete source, or rebuild index
        Loop-->>API: permission_required and permission_request
    end

    API-->>UI: JSON response: response, citations, outcome,<br/>rounds, tool_calls, and optional permission_request/retrieval_id
```

---

## 12. Human-in-the-loop approval for Agent mutations

Only the three knowledge-base mutations require approval: `rag_ingest`,
`rag_delete_source`, and `rag_index_rebuild`. The permission request exposes
the operation but deliberately does not expose the tool arguments, because an
ingest request can contain document content.

```mermaid
flowchart TD
    A[Agent requests a fixed registry tool] --> B{Does the tool mutate the knowledge base?}
    B -->|No: rag_retrieve| C[Validate arguments and execute in the Agent loop]
    C --> D[Return evidence to the model and continue this chat request]

    B -->|Yes| E[Stop the current chat request]
    E --> F[Return outcome: permission_required]
    F --> G[Return permission_request:<br/>token, tool, operation, expires_at]
    G --> H[Frontend presents an approval card]
    H --> I{User decision}

    I -->|Reject| J[POST /agent/permission/:token<br/>{session_id, approved: false}]
    J --> K[Consume token and return outcome: denied]

    I -->|Approve| L[POST /agent/permission/:token<br/>{session_id, approved: true}]
    L --> M{Token is valid?}
    M -->|No: expired, used, or wrong session| N[Reject request; execute nothing]
    M -->|Yes| O[Consume the single-use token]
    O --> P[Validate and execute exactly one mutation]
    P --> Q[Return execution result]

    Q --> R[Frontend starts a new /agent/chat turn if continuation is needed]
    R -. Current implementation does not auto-resume the prior LLM loop .-> A
```

---

## 13. Agent retrieval feedback and offline review

Feedback is a local, immutable quality ledger. It does not automatically
modify ranking, reranking, indexing, embeddings, or Agent behavior.

```mermaid
flowchart TD
    A[Agent chat completes with retrieval evidence] --> B[Persist one local retrieval event]
    B --> C[Create opaque retrieval_id]
    B --> D[Assign durable citation_id to each citation]
    C --> E[Return Agent response to frontend]
    D --> E

    E --> F{Feedback capture enabled and evidence returned?}
    F -->|No| G[Do not render feedback controls]
    F -->|Yes| H[Render Helpful, Unhelpful, and Citation Error controls]

    H --> I[POST /feedback with retrieval_id,<br/>session_id, kind, optional note]
    I --> J{kind is citation-error?}
    J -->|Yes| K[Require citation_ids belonging to the retrieval]
    J -->|No| L[Record immutable feedback disposition]
    K --> L
    L --> M[Store local ledger with privacy-minimized metadata]

    M --> N[Optional: convert filtered feedback to candidates]
    N --> O[Human reviewer approves or rejects each candidate]
    O --> P[Export or curate approved candidates into an evaluation set]
    P --> Q[Run offline evaluation and deliberately review any configuration change]
    Q -. No automatic online learning or retrieval change .-> A
```
