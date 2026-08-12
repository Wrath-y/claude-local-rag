package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Wrath-y/local-rag/internal/chunk"
	"github.com/Wrath-y/local-rag/internal/config"
	"github.com/Wrath-y/local-rag/internal/document"
	"github.com/Wrath-y/local-rag/internal/observe"
	"github.com/Wrath-y/local-rag/internal/store"
)

func init() {
	gin.SetMode(gin.TestMode)
	observe.InitMetrics()
}

// ---------------------------------------------------------------------------
// Mock implementations
// ---------------------------------------------------------------------------

type mockEmbedder struct {
	vecs [][]float32
	err  error
	dims int
}

func (m *mockEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	if m.err != nil {
		return nil, m.err
	}
	out := make([][]float32, len(texts))
	for i := range texts {
		if i < len(m.vecs) {
			out[i] = m.vecs[i]
		} else {
			// Return a zero vector of the right dimension.
			d := m.dims
			if d == 0 {
				d = 4
			}
			out[i] = make([]float32, d)
		}
	}
	return out, nil
}

func (m *mockEmbedder) Dims() int {
	if m.dims == 0 {
		return 4
	}
	return m.dims
}

type mockChunker struct {
	chunks []chunk.Chunk
	err    error
	calls  int
}

func (m *mockChunker) Chunk(text, source string) ([]chunk.Chunk, error) {
	m.calls++
	if m.err != nil {
		return nil, m.err
	}
	if m.chunks != nil {
		return m.chunks, nil
	}
	return []chunk.Chunk{{Text: text, Source: source, MD5: "abc123"}}, nil
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func testDeps(t *testing.T, st *store.Store) Deps {
	t.Helper()
	cfg := &config.Config{
		Retrieve: config.RetrieveConfig{
			TopK:                3,
			CandidateMultiplier: 2,
			RerankCandidates:    9,
			ContextWindow:       180000,
			ResponseReserve:     8000,
			ScoreWeights:        config.ScoreWeights{Vector: 0.7, BM25: 0.3},
		},
		Chunk: config.ChunkConfig{Strategy: "fixed"},
	}
	return Deps{
		Config:   cfg,
		Store:    st,
		Embedder: &mockEmbedder{dims: 4},
		Chunker:  &mockChunker{},
	}
}

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.New(t.TempDir()+"/test.db", 4)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// ---------------------------------------------------------------------------
// TestIngest_Success
// ---------------------------------------------------------------------------

func TestIngest_Success(t *testing.T) {
	st := newTestStore(t)
	h := New(testDeps(t, st))

	body, _ := json.Marshal(map[string]string{
		"text":   "Hello world, this is a test document.",
		"source": "test-src",
	})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest(http.MethodPost, "/ingest", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	h.Ingest(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["status"] != "ok" && resp["status"] != "skip" {
		t.Errorf("unexpected status: %v", resp["status"])
	}
}

// ---------------------------------------------------------------------------
// TestIngest_MissingText
// ---------------------------------------------------------------------------

func TestIngest_MissingText(t *testing.T) {
	st := newTestStore(t)
	h := New(testDeps(t, st))

	body, _ := json.Marshal(map[string]string{"source": "x"})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest(http.MethodPost, "/ingest", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	h.Ingest(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestIngest_DefaultsLegacyTextRequestToManualSource(t *testing.T) {
	st := newTestStore(t)
	h := New(testDeps(t, st))
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/ingest", bytes.NewBufferString(`{"text":"legacy request"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	h.Ingest(c)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	sources, err := st.ListSources()
	if err != nil || len(sources) != 1 || sources[0].Source != "manual" {
		t.Fatalf("sources = %#v, %v", sources, err)
	}
}

func TestIngest_RejectsInvalidLoaderResultBeforePipeline(t *testing.T) {
	st := newTestStore(t)
	deps := testDeps(t, st)
	chunker := &mockChunker{}
	deps.Chunker = chunker
	deps.LoaderRegistry = document.NewRegistry(invalidResultLoader{})
	h := New(deps)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/ingest", bytes.NewBufferString(`{"text":"ignored"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	h.Ingest(c)
	if w.Code != http.StatusBadRequest || chunker.calls != 0 {
		t.Fatalf("status=%d pipeline calls=%d body=%s", w.Code, chunker.calls, w.Body.String())
	}
}

func TestIngest_SanitizesDownstreamFailure(t *testing.T) {
	st := newTestStore(t)
	deps := testDeps(t, st)
	deps.Embedder = &mockEmbedder{dims: 4, err: errors.New("credential=secret")}
	h := New(deps)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/ingest", bytes.NewBufferString(`{"text":"content"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	h.Ingest(c)
	if w.Code != http.StatusInternalServerError || !bytes.Contains(w.Body.Bytes(), []byte(`"code":"ingest_failed"`)) || bytes.Contains(w.Body.Bytes(), []byte("secret")) {
		t.Fatalf("unexpected response %d: %s", w.Code, w.Body.String())
	}
}

type invalidResultLoader struct{}

func (invalidResultLoader) Name() string                   { return "invalid" }
func (invalidResultLoader) Supports(document.Request) bool { return true }
func (invalidResultLoader) Load(context.Context, document.Request) ([]document.Document, error) {
	return []document.Document{
		{Content: "one", Metadata: document.Metadata{Source: "duplicate", DisplayName: "one", Kind: document.InputText}},
		{Content: "two", Metadata: document.Metadata{Source: "duplicate", DisplayName: "two", Kind: document.InputText}},
	}, nil
}

// ---------------------------------------------------------------------------
// TestRetrieve_EmptyStore
// ---------------------------------------------------------------------------

func TestRetrieve_EmptyStore(t *testing.T) {
	st := newTestStore(t)
	h := New(testDeps(t, st))

	body, _ := json.Marshal(map[string]string{"text": "what is redis?"})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest(http.MethodPost, "/retrieve", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	h.Retrieve(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if _, ok := resp["chunks"]; !ok {
		t.Error("response missing 'chunks' key")
	}
}

// ---------------------------------------------------------------------------
// TestRetrieve_MissingText
// ---------------------------------------------------------------------------

func TestRetrieve_MissingText(t *testing.T) {
	st := newTestStore(t)
	h := New(testDeps(t, st))

	body, _ := json.Marshal(map[string]string{})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest(http.MethodPost, "/retrieve", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	h.Retrieve(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// TestHealth_ReturnsOK
// ---------------------------------------------------------------------------

func TestHealth_ReturnsOK(t *testing.T) {
	st := newTestStore(t)
	h := New(testDeps(t, st))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest(http.MethodGet, "/health", nil)

	h.Health(c)

	// Health is a side-effect-free compatibility document; it does not probe
	// the embedder with synthetic content.
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if (resp["status"] != "ok" && resp["status"] != "degraded") || resp["schema_version"] != "1.0" || resp["service"] != "local-rag" {
		t.Errorf("unexpected health response: %v", resp)
	}
}

func TestHealthReportsEffectiveGraphSnapshotLimits(t *testing.T) {
	st := newTestStore(t)
	deps := testDeps(t, st)
	deps.Config.Graph.MaxPayloadBytes = 1234
	deps.Config.Graph.MaxNodes = 12
	deps.Config.Graph.MaxEdges = 34
	h := New(deps)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest(http.MethodGet, "/health", nil)
	h.Health(c)
	var response struct {
		Limits []struct {
			Name  string `json:"name"`
			Value int    `json:"value"`
		} `json:"limits"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for _, limit := range response.Limits {
		got[limit.Name] = limit.Value
	}
	for name, want := range map[string]int{"snapshot_payload_bytes": 1234, "snapshot_nodes": 12, "snapshot_edges": 34} {
		if got[name] != want {
			t.Fatalf("%s=%d want %d", name, got[name], want)
		}
	}
}

func TestHealthReportsUnavailableAfterStoreLifecycleCloses(t *testing.T) {
	st := newTestStore(t)
	deps := testDeps(t, st)
	h := New(deps)
	if err := h.deps.Stores.Close(); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest(http.MethodGet, "/health", nil)
	h.Health(c)
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), `"status":"unavailable"`) || strings.Contains(w.Body.String(), "rag.db") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestAgentCreateSession_InitializesFromStoreLifecycle(t *testing.T) {
	st := newTestStore(t)
	h := New(testDeps(t, st))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest(http.MethodPost, "/agent/session", bytes.NewBufferString(`{"metadata":"{}"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.AgentCreateSession(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var response struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.SessionID == "" {
		t.Fatal("response did not contain a session_id")
	}
}

// ---------------------------------------------------------------------------
// TestRerankToggle
// ---------------------------------------------------------------------------

func TestRerankToggle(t *testing.T) {
	st := newTestStore(t)
	h := New(testDeps(t, st))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest(http.MethodPost, "/config/rerank", nil)

	h.RerankToggle(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if _, ok := resp["rerank_enabled"]; !ok {
		t.Error("missing rerank_enabled field")
	}
}

func TestResetRequiresConfirmationAndUsesSharedService(t *testing.T) {
	st := newTestStore(t)
	if _, err := st.InsertChunk("content", "source", "reset-test", "", "", []float32{0, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	h := New(testDeps(t, st))
	withoutConfirmation := httptest.NewRecorder()
	contextWithoutConfirmation, _ := gin.CreateTestContext(withoutConfirmation)
	contextWithoutConfirmation.Request = httptest.NewRequest(http.MethodDelete, "/reset", nil)
	h.Reset(contextWithoutConfirmation)
	if withoutConfirmation.Code != http.StatusBadRequest {
		t.Fatalf("unconfirmed reset = %d, want 400", withoutConfirmation.Code)
	}
	withConfirmation := httptest.NewRecorder()
	contextWithConfirmation, _ := gin.CreateTestContext(withConfirmation)
	contextWithConfirmation.Request = httptest.NewRequest(http.MethodDelete, "/reset?confirm=true", nil)
	h.Reset(contextWithConfirmation)
	if withConfirmation.Code != http.StatusOK {
		t.Fatalf("confirmed reset = %d: %s", withConfirmation.Code, withConfirmation.Body.String())
	}
	if count, err := st.ChunkCount(); err != nil || count != 0 {
		t.Fatalf("chunk count after reset = %d, %v", count, err)
	}
}

// ---------------------------------------------------------------------------
// TestSetChunkStrategy_Invalid
// ---------------------------------------------------------------------------

func TestSetChunkStrategy_Invalid(t *testing.T) {
	st := newTestStore(t)
	h := New(testDeps(t, st))

	body, _ := json.Marshal(map[string]string{"strategy": "unknown_mode"})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest(http.MethodPut, "/config/chunk-strategy", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	h.SetChunkStrategy(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// TestListSources_Empty
// ---------------------------------------------------------------------------

func TestListSources_Empty(t *testing.T) {
	st := newTestStore(t)
	h := New(testDeps(t, st))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest(http.MethodGet, "/sources", nil)

	h.ListSources(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// TestStats
// ---------------------------------------------------------------------------

func TestStats(t *testing.T) {
	st := newTestStore(t)
	h := New(testDeps(t, st))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest(http.MethodGet, "/stats", nil)

	h.Stats(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}
