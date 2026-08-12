package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Wrath-y/local-rag/internal/store"
)

type blockingRebuildEmbedder struct {
	started chan struct{}
	unblock chan struct{}
	once    sync.Once
	dims    int
}

func (m *blockingRebuildEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	m.once.Do(func() { close(m.started) })
	<-m.unblock
	vectors := make([][]float32, len(texts))
	for i := range vectors {
		vectors[i] = []float32{1, 0, 0, 0}
	}
	return vectors, nil
}

func (m *blockingRebuildEmbedder) Dims() int { return m.dims }

func indexRequest(t *testing.T, h *Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, path, nil)
	if method == http.MethodPost {
		h.IndexRebuild(c)
	} else {
		h.IndexStatus(c)
	}
	return w
}

func waitForIndexTerminal(t *testing.T, h *Handler) IndexStatusResponse {
	t.Helper()
	deadline := time.After(2 * time.Second)
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		status := h.indexRebuild.Status()
		if status.State != IndexStateRebuilding {
			return status
		}
		select {
		case <-deadline:
			t.Fatalf("rebuild did not finish: %#v", status)
		case <-ticker.C:
		}
	}
}

func indexRetrieveOpts() store.RetrieveOpts {
	return store.RetrieveOpts{TopK: 3, CandidateMultiplier: 2, VectorWeight: 0.7, BM25Weight: 0.3}
}

func TestIndexRebuildSuccessRetainsOldIndex(t *testing.T) {
	st := newTestStore(t)
	if _, err := st.InsertChunk("first", "test", "first", "", "", []float32{1, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertChunk("second", "test", "second", "", "", []float32{0, 1, 0, 0}); err != nil {
		t.Fatal(err)
	}
	deps := testDeps(t, st)
	deps.Embedder = &mockEmbedder{dims: 4}
	h := New(deps)

	w := indexRequest(t, h, http.MethodPost, "/index/rebuild")
	if w.Code != http.StatusAccepted {
		t.Fatalf("start = %d: %s", w.Code, w.Body.String())
	}
	status := waitForIndexTerminal(t, h)
	if status.State != IndexStateNormal || status.Progress != 1 || status.Processed != 2 || status.Total != 2 {
		t.Fatalf("unexpected completed status: %#v", status)
	}
	var retired int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name LIKE 'vec_chunks_retired_%' AND sql LIKE 'CREATE VIRTUAL TABLE%'`).Scan(&retired); err != nil {
		t.Fatal(err)
	}
	if retired != 1 {
		t.Fatalf("retained indexes = %d, want 1", retired)
	}
}

func TestLegacyIndexStatusKeepsItsFlatNonV1Envelope(t *testing.T) {
	st := newTestStore(t)
	h := New(testDeps(t, st))
	response := indexRequest(t, h, http.MethodGet, "/index/status")
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if _, ok := payload["state"]; !ok {
		t.Fatalf("legacy payload=%v", payload)
	}
	for _, absent := range []string{"operation", "namespace", "snapshot_version", "request_id"} {
		if _, ok := payload[absent]; ok {
			t.Fatalf("legacy payload unexpectedly has %s: %v", absent, payload)
		}
	}
}

func TestIndexRebuildDuplicateAndOnlineRead(t *testing.T) {
	st := newTestStore(t)
	if _, err := st.InsertChunk("online retrieval", "test", "online", "", "", []float32{1, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	embedder := &blockingRebuildEmbedder{started: make(chan struct{}), unblock: make(chan struct{}), dims: 4}
	deps := testDeps(t, st)
	deps.Embedder = embedder
	h := New(deps)
	if w := indexRequest(t, h, http.MethodPost, "/index/rebuild"); w.Code != http.StatusAccepted {
		t.Fatalf("start = %d: %s", w.Code, w.Body.String())
	}
	select {
	case <-embedder.started:
	case <-time.After(time.Second):
		t.Fatal("rebuild did not reach embedding")
	}
	if w := indexRequest(t, h, http.MethodPost, "/index/rebuild"); w.Code != http.StatusConflict {
		t.Fatalf("duplicate = %d: %s", w.Code, w.Body.String())
	}
	results, err := st.Retrieve([]float32{1, 0, 0, 0}, "online", indexRetrieveOpts())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 || results[0].Text != "online retrieval" {
		t.Fatalf("active index was unavailable during rebuild: %#v", results)
	}
	reset := httptest.NewRecorder()
	resetContext, _ := gin.CreateTestContext(reset)
	resetContext.Request = httptest.NewRequest(http.MethodDelete, "/reset", nil)
	h.Reset(resetContext)
	if reset.Code != http.StatusServiceUnavailable {
		t.Fatalf("write during rebuild = %d: %s", reset.Code, reset.Body.String())
	}
	count, err := st.ChunkCount()
	if err != nil || count != 1 {
		t.Fatalf("rejected write changed chunks: count=%d err=%v", count, err)
	}
	close(embedder.unblock)
	if status := waitForIndexTerminal(t, h); status.State != IndexStateNormal {
		t.Fatalf("final status: %#v", status)
	}
}

func TestIndexRebuildFailurePreservesOldIndexAndReportsStatus(t *testing.T) {
	st := newTestStore(t)
	if _, err := st.InsertChunk("preserve me", "test", "preserve", "", "", []float32{1, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	deps := testDeps(t, st)
	deps.Embedder = &mockEmbedder{dims: 4, err: errors.New("provider down")}
	h := New(deps)
	if w := indexRequest(t, h, http.MethodPost, "/index/rebuild"); w.Code != http.StatusAccepted {
		t.Fatalf("start = %d: %s", w.Code, w.Body.String())
	}
	status := waitForIndexTerminal(t, h)
	if status.State != IndexStateFailed || status.ErrorCategory != "embedding" || status.Error == "" {
		t.Fatalf("unexpected failure status: %#v", status)
	}
	w := indexRequest(t, h, http.MethodGet, "/index/status")
	var reported IndexStatusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &reported); err != nil {
		t.Fatal(err)
	}
	if reported.State != IndexStateFailed || reported.TaskID != status.TaskID {
		t.Fatalf("reported status: %#v", reported)
	}
	results, err := st.Retrieve([]float32{1, 0, 0, 0}, "preserve", indexRetrieveOpts())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 || results[0].Text != "preserve me" {
		t.Fatalf("failure changed active retrieval: %#v", results)
	}
}
