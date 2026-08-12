package contract

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Wrath-y/local-rag/internal/config"
	"github.com/Wrath-y/local-rag/internal/graphsnapshot"
	"github.com/Wrath-y/local-rag/internal/handler"
	"github.com/Wrath-y/local-rag/internal/operability"
	"github.com/Wrath-y/local-rag/internal/provider"
	"github.com/Wrath-y/local-rag/internal/store"
	"gopkg.in/yaml.v3"
)

func TestOperabilityFixturesReplayAgainstGinAndGraphSQLite(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s, err := store.New(t.TempDir()+"/graph.db", 4)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	node := graphsnapshot.Node{ID: "node", Type: "kind", Label: "label", Text: "text", Properties: []byte(`{}`), Provenance: []byte(`{}`)}
	_, hash, err := graphsnapshot.CanonicalHash([]graphsnapshot.Node{node}, nil)
	if err != nil {
		t.Fatal(err)
	}
	service := graphsnapshot.NewService(s, func() (string, error) { return "source-task", nil })
	if _, graphErr := service.Put(context.Background(), "project", "v1", graphsnapshot.Request{SchemaVersion: graphsnapshot.SchemaVersionV1, Mode: graphsnapshot.ModeFull, ContentHash: hash, Nodes: []graphsnapshot.Node{node}}); graphErr != nil {
		t.Fatal(graphErr)
	}
	if err = s.PromoteGraphComponent(context.Background(), "source-task"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB().Exec(`UPDATE graph_tasks SET state='succeeded',phase='completed',progress=10000 WHERE id='source-task'`); err != nil {
		t.Fatal(err)
	}

	h := handler.New(handler.Deps{Config: &config.Config{}, Store: s, Embedder: contractEmbedder{}, Reranker: contractReranker{}, GraphService: service, GraphSnapshotReader: s, GraphTaskReader: s, GraphLifecycle: s, GraphRebuild: operability.RebuildService{Repository: s, NewTaskID: func() string { return "task-rebuild-1" }}})
	router := gin.New()
	router.Use(handler.GraphRequestID())
	router.GET("/health", h.Health)
	router.POST("/v1/graphs/:namespace/snapshots/:version/rebuild", h.RebuildGraphSnapshot)
	router.GET("/v1/tasks/:task_id", h.GetGraphTask)

	health := httptest.NewRecorder()
	router.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/health", nil))
	if health.Code != http.StatusOK {
		t.Fatalf("health=%d %s", health.Code, health.Body.String())
	}
	var healthBody struct {
		SchemaVersion string   `json:"schema_version"`
		APIVersions   []string `json:"api_versions"`
	}
	if err = json.Unmarshal(health.Body.Bytes(), &healthBody); err != nil || healthBody.SchemaVersion != "1.0" || len(healthBody.APIVersions) != 1 || healthBody.APIVersions[0] != "v1" {
		t.Fatalf("health=%s err=%v", health.Body.String(), err)
	}
	validateOpenAPIResponse(t, "Health", health.Body.Bytes())

	rebuild := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/graphs/project/snapshots/v1/rebuild", bytes.NewBufferString(`{"components":["vector","fts","graph_indexes"]}`))
	request.Header.Set("Idempotency-Key", "fixture-key")
	request.Header.Set("X-Request-ID", "fixture-request")
	router.ServeHTTP(rebuild, request)
	if rebuild.Code != http.StatusAccepted || rebuild.Header().Get("Location") != "/v1/tasks/task-rebuild-1" || rebuild.Header().Get("X-Request-ID") != "fixture-request" {
		t.Fatalf("rebuild=%d headers=%v body=%s", rebuild.Code, rebuild.Header(), rebuild.Body.String())
	}
	var submission struct {
		TaskID     string   `json:"task_id"`
		State      string   `json:"state"`
		Components []string `json:"components"`
	}
	if err = json.Unmarshal(rebuild.Body.Bytes(), &submission); err != nil || submission.TaskID != "task-rebuild-1" || submission.State != "queued" || len(submission.Components) != 3 || submission.Components[0] != "graph_indexes" || submission.Components[2] != "vector" {
		t.Fatalf("submission=%+v err=%v", submission, err)
	}
	validateOpenAPIResponse(t, "RebuildSubmission", rebuild.Body.Bytes())
	replay := httptest.NewRecorder()
	replayRequest := httptest.NewRequest(http.MethodPost, "/v1/graphs/project/snapshots/v1/rebuild", bytes.NewBufferString(`{"components":["graph_indexes","fts","vector"]}`))
	replayRequest.Header.Set("Idempotency-Key", "fixture-key")
	router.ServeHTTP(replay, replayRequest)
	if replay.Code != http.StatusAccepted || !bytes.Contains(replay.Body.Bytes(), []byte(`"replayed":true`)) {
		t.Fatalf("replay=%d %s", replay.Code, replay.Body.String())
	}
	conflict := httptest.NewRecorder()
	conflictRequest := httptest.NewRequest(http.MethodPost, "/v1/graphs/project/snapshots/v1/rebuild", bytes.NewBufferString(`{"components":["fts"]}`))
	conflictRequest.Header.Set("Idempotency-Key", "fixture-key")
	router.ServeHTTP(conflict, conflictRequest)
	if conflict.Code != http.StatusConflict || !bytes.Contains(conflict.Body.Bytes(), []byte(`"code":"IDEMPOTENCY_CONFLICT"`)) {
		t.Fatalf("conflict=%d %s", conflict.Code, conflict.Body.String())
	}

	poll := httptest.NewRecorder()
	router.ServeHTTP(poll, httptest.NewRequest(http.MethodGet, "/v1/tasks/task-rebuild-1", nil))
	if poll.Code != http.StatusOK || !bytes.Contains(poll.Body.Bytes(), []byte(`"submission_request_id":"fixture-request"`)) {
		t.Fatalf("poll=%d %s", poll.Code, poll.Body.String())
	}
	validateOpenAPIResponse(t, "Task", poll.Body.Bytes())
	claimed, found, err := s.ClaimOldestQueuedGraphTask(context.Background())
	if err != nil || !found || claimed.ID != "task-rebuild-1" {
		t.Fatalf("claimed=%+v found=%v err=%v", claimed, found, err)
	}
	if _, err = s.AdvanceGraphTaskProgress(context.Background(), claimed.ID, "building_fts", 5500); err != nil {
		t.Fatal(err)
	}
	if err = s.RecoverGraphTasks(context.Background()); err != nil {
		t.Fatal(err)
	}
	recovered := httptest.NewRecorder()
	router.ServeHTTP(recovered, httptest.NewRequest(http.MethodGet, "/v1/tasks/task-rebuild-1", nil))
	if recovered.Code != http.StatusOK || !bytes.Contains(recovered.Body.Bytes(), []byte(`"state":"queued"`)) || !bytes.Contains(recovered.Body.Bytes(), []byte(`"phase":"queued"`)) || !bytes.Contains(recovered.Body.Bytes(), []byte(`"progress":0.55`)) {
		t.Fatalf("recovered=%d %s", recovered.Code, recovered.Body.String())
	}
	validateOpenAPIResponse(t, "Task", recovered.Body.Bytes())
	if _, err = s.DB().Exec(`DELETE FROM graph_nodes WHERE namespace='project' AND version='v1' AND node_id='node'`); err != nil {
		t.Fatal(err)
	}
	reimport := httptest.NewRecorder()
	reimportRequest := httptest.NewRequest(http.MethodPost, "/v1/graphs/project/snapshots/v1/rebuild", bytes.NewBufferString(`{"components":["fts"]}`))
	reimportRequest.Header.Set("Idempotency-Key", "reimport-key")
	reimportRequest.Header.Set("X-Request-ID", "request-reimport")
	router.ServeHTTP(reimport, reimportRequest)
	if reimport.Code != http.StatusConflict || !bytes.Contains(reimport.Body.Bytes(), []byte(`"code":"REIMPORT_REQUIRED"`)) || !bytes.Contains(reimport.Body.Bytes(), []byte(`"request_id":"request-reimport"`)) {
		t.Fatalf("reimport=%d %s", reimport.Code, reimport.Body.String())
	}
	validateOpenAPIResponse(t, "GraphError", reimport.Body.Bytes())
}

type contractEmbedder struct{}

func (contractEmbedder) Dims() int { return 4 }
func (contractEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	result := make([][]float32, len(texts))
	for i := range result {
		result[i] = []float32{1, 0, 0, 0}
	}
	return result, nil
}

type contractReranker struct{}

func (contractReranker) Rerank(_ context.Context, _ string, documents []string, topN int) ([]provider.RerankResult, error) {
	if topN > len(documents) {
		topN = len(documents)
	}
	result := make([]provider.RerankResult, topN)
	for i := range result {
		result[i] = provider.RerankResult{Index: i, RelevanceScore: float64(topN - i)}
	}
	return result, nil
}

// validateOpenAPIResponse keeps contract replay tied to the published schema.
// The full OpenAPI validator is intentionally unnecessary here: this checks
// every required response member from the source-of-truth document while the
// handlers' typed DTO tests cover nested field encoding.
func validateOpenAPIResponse(t *testing.T, schemaName string, body []byte) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "api", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err = yaml.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	schemas := document["components"].(map[string]any)["schemas"].(map[string]any)
	schema, ok := schemas[schemaName].(map[string]any)
	if !ok {
		t.Fatalf("OpenAPI schema %s missing", schemaName)
	}
	var value map[string]any
	if err = json.Unmarshal(body, &value); err != nil {
		t.Fatal(err)
	}
	for _, required := range schema["required"].([]any) {
		if _, ok := value[required.(string)]; !ok {
			t.Fatalf("%s response misses required OpenAPI field %q: %s", schemaName, required, body)
		}
	}
}
