package contract

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Wrath-y/local-rag/internal/config"
	"github.com/Wrath-y/local-rag/internal/graphsnapshot"
	"github.com/Wrath-y/local-rag/internal/handler"
	"github.com/Wrath-y/local-rag/internal/operability"
	"github.com/Wrath-y/local-rag/internal/store"
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

	h := handler.New(handler.Deps{Config: &config.Config{}, Store: s, GraphService: service, GraphSnapshotReader: s, GraphTaskReader: s, GraphLifecycle: s, GraphRebuild: operability.RebuildService{Repository: s, NewTaskID: func() string { return "task-rebuild-1" }}})
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

	rebuild := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/graphs/project/snapshots/v1/rebuild", bytes.NewBufferString(`{"components":["fts","graph_indexes"]}`))
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
	if err = json.Unmarshal(rebuild.Body.Bytes(), &submission); err != nil || submission.TaskID != "task-rebuild-1" || submission.State != "queued" || len(submission.Components) != 2 || submission.Components[0] != "graph_indexes" {
		t.Fatalf("submission=%+v err=%v", submission, err)
	}

	poll := httptest.NewRecorder()
	router.ServeHTTP(poll, httptest.NewRequest(http.MethodGet, "/v1/tasks/task-rebuild-1", nil))
	if poll.Code != http.StatusOK || !bytes.Contains(poll.Body.Bytes(), []byte(`"submission_request_id":"fixture-request"`)) {
		t.Fatalf("poll=%d %s", poll.Code, poll.Body.String())
	}
}
