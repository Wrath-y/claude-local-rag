package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Wrath-y/local-rag/internal/config"
	"github.com/Wrath-y/local-rag/internal/graphretrieval"
	"github.com/Wrath-y/local-rag/internal/graphsnapshot"
	"github.com/Wrath-y/local-rag/internal/store"
)

type graphRetrievalFake struct {
	calls  int
	result graphretrieval.RetrievalResult
	err    *graphsnapshot.Error
}

func (f *graphRetrievalFake) Retrieve(_ context.Context, _ string, _ graphretrieval.Request) (graphretrieval.RetrievalResult, *graphsnapshot.Error) {
	f.calls++
	return f.result, f.err
}

func graphRetrievalRouter(h *Handler) *gin.Engine {
	router := gin.New()
	router.Use(GraphRequestID())
	router.POST("/v1/graphs/:namespace/retrieve", h.RetrieveGraph)
	return router
}

func TestRetrieveGraphUsesStrictBindingAndSharedErrorsBeforeService(t *testing.T) {
	for _, body := range []struct {
		body string
		code string
	}{
		{`{"query":"q","query":"again"}`, "INVALID_RETRIEVAL_REQUEST"},
		{`{"query":"q","seed_limit":101}`, "LIMIT_EXCEEDED"},
		{`{"query":" "}`, "INVALID_RETRIEVAL_REQUEST"},
	} {
		fake := &graphRetrievalFake{}
		h := New(Deps{Config: &config.Config{}, GraphRetrieval: fake})
		response := httptest.NewRecorder()
		graphRetrievalRouter(h).ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/graphs/project/retrieve", strings.NewReader(body.body)))
		if response.Code != http.StatusBadRequest || fake.calls != 0 || !strings.Contains(response.Body.String(), `"code":"`+body.code+`"`) {
			t.Fatalf("body=%s status=%d calls=%d response=%s", body.body, response.Code, fake.calls, response.Body.String())
		}
	}
}

func TestRetrieveGraphReturnsTypedResultAndRequestID(t *testing.T) {
	fake := &graphRetrievalFake{result: graphretrieval.RetrievalResult{ResolvedSnapshotVersion: "v1", ContentHash: "hash", ModeUsed: "bm25_only", Results: []graphretrieval.Result{}}}
	h := New(Deps{Config: &config.Config{}, GraphRetrieval: fake})
	request := httptest.NewRequest(http.MethodPost, "/v1/graphs/project/retrieve", strings.NewReader(`{"query":"q"}`))
	request.Header.Set(graphRequestIDHeader, "retrieve_test")
	response := httptest.NewRecorder()
	graphRetrievalRouter(h).ServeHTTP(response, request)
	if response.Code != http.StatusOK || fake.calls != 1 || response.Header().Get(graphRequestIDHeader) != "retrieve_test" || !strings.Contains(response.Body.String(), `"resolved_snapshot_version":"v1"`) {
		t.Fatalf("status=%d calls=%d response=%s", response.Code, fake.calls, response.Body.String())
	}
}

func TestRetrieveGraphPreservesDynamicRetrievalRetryability(t *testing.T) {
	fake := &graphRetrievalFake{err: graphsnapshot.NewError(graphsnapshot.CodeRetrievalUnavailable, nil, nil).WithRetryability(false)}
	h := New(Deps{Config: &config.Config{}, GraphRetrieval: fake})
	response := httptest.NewRecorder()
	graphRetrievalRouter(h).ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/graphs/project/retrieve", strings.NewReader(`{"query":"q"}`)))
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), `"retryable":false`) {
		t.Fatalf("status=%d response=%s", response.Code, response.Body.String())
	}
}

type graphRetrievalEndpointEmbedder struct{}

func (graphRetrievalEndpointEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	result := make([][]float32, len(texts))
	for i := range result {
		result[i] = []float32{1, 0}
	}
	return result, nil
}
func (graphRetrievalEndpointEmbedder) Dims() int { return 2 }

func TestRetrieveGraphUsesRealGinAndTemporarySQLite(t *testing.T) {
	st, err := store.New(t.TempDir()+"/rag.db", 2)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	nodes := []graphsnapshot.Node{{ID: "alpha", Type: "kind", Label: "Alpha", Text: "alpha", Properties: []byte(`{}`), Provenance: []byte(`{}`)}}
	_, hash, err := graphsnapshot.CanonicalHash(nodes, nil)
	if err != nil {
		t.Fatal(err)
	}
	service := graphsnapshot.NewService(st, func() (string, error) { return "retrieve-task", nil })
	if _, graphErr := service.Put(context.Background(), "project", "v1", graphsnapshot.Request{SchemaVersion: graphsnapshot.SchemaVersionV1, Mode: graphsnapshot.ModeFull, ContentHash: hash, Nodes: nodes}); graphErr != nil {
		t.Fatal(graphErr)
	}
	if err = st.PromoteGraphComponent(context.Background(), "retrieve-task"); err != nil {
		t.Fatal(err)
	}
	if err = st.PopulateGraphSearchDocuments(context.Background(), "retrieve-task"); err != nil {
		t.Fatal(err)
	}
	embedder := graphRetrievalEndpointEmbedder{}
	if err = st.BuildGraphVectors(context.Background(), "retrieve-task", graphsnapshot.WithEmbeddingIdentity(embedder, graphsnapshot.EmbeddingIdentity{Algorithm: graphsnapshot.SearchDocumentFormatV1 + "/embedding", Provider: "fake", Model: "fixture", Dimensions: 2})); err != nil {
		t.Fatal(err)
	}
	if _, err = st.ActivateGraphSnapshot(context.Background(), "project", "v1"); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	cfg.Embedding.Provider, cfg.Embedding.Model, cfg.Embedding.Dims = "fake", "fixture", 2
	h := New(Deps{Config: cfg, Stores: NewStoreLifecycle(st), Embedder: embedder})
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/graphs/project/retrieve", strings.NewReader(`{"query":"alpha","graph_depth":0}`))
	graphRetrievalRouter(h).ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"mode_used":"hybrid"`) || !strings.Contains(response.Body.String(), `"id":"alpha"`) {
		t.Fatalf("status=%d response=%s", response.Code, response.Body.String())
	}
}
