package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Wrath-y/local-rag/internal/config"
	"github.com/Wrath-y/local-rag/internal/graphsnapshot"
)

type graphServiceFake struct {
	result  graphsnapshot.SubmissionCheck
	err     *graphsnapshot.Error
	called  bool
	request graphsnapshot.Request
}

func (f *graphServiceFake) Put(_ context.Context, _, _ string, request graphsnapshot.Request) (graphsnapshot.SubmissionCheck, *graphsnapshot.Error) {
	f.called = true
	f.request = request
	return f.result, f.err
}

type graphReaderFake struct {
	snapshot  graphsnapshot.Snapshot
	found     bool
	err       error
	namespace string
	version   string
}

type graphTaskReaderFake struct {
	task  graphsnapshot.Task
	found bool
	err   error
	id    string
}

func (f *graphTaskReaderFake) LookupGraphTask(_ context.Context, id string) (graphsnapshot.Task, bool, error) {
	f.id = id
	return f.task, f.found, f.err
}

func (f *graphReaderFake) LookupGraphSnapshot(_ context.Context, namespace, version string) (graphsnapshot.Snapshot, bool, error) {
	f.namespace, f.version = namespace, version
	return f.snapshot, f.found, f.err
}

func newGraphSnapshotRouter(h *Handler) *gin.Engine {
	router := gin.New()
	router.Use(GraphRequestID())
	router.PUT("/v1/graphs/:namespace/snapshots/:version", h.PutGraphSnapshot)
	router.GET("/v1/graphs/:namespace/snapshots/:version", h.GetGraphSnapshot)
	router.GET("/v1/tasks/:task_id", h.GetGraphTask)
	return router
}

func TestGetGraphTaskReturnsEveryDurableStateAndNotFound(t *testing.T) {
	for _, state := range []graphsnapshot.TaskState{graphsnapshot.TaskQueued, graphsnapshot.TaskRunning, graphsnapshot.TaskSucceeded, graphsnapshot.TaskFailed} {
		t.Run(string(state), func(t *testing.T) {
			reader := &graphTaskReaderFake{task: graphsnapshot.Task{ID: "task", Namespace: "project", Version: "revision", State: state, Phase: "fts", Progress: 50}, found: true}
			handler := New(Deps{Config: &config.Config{}, GraphTaskReader: reader})
			response := httptest.NewRecorder()
			newGraphSnapshotRouter(handler).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/tasks/task", nil))
			if response.Code != http.StatusOK || reader.id != "task" || !strings.Contains(response.Body.String(), `"state":"`+string(state)+`"`) {
				t.Fatalf("response = %d %s; task=%q", response.Code, response.Body.String(), reader.id)
			}
		})
	}

	reader := &graphTaskReaderFake{}
	handler := New(Deps{Config: &config.Config{}, GraphTaskReader: reader})
	response := httptest.NewRecorder()
	newGraphSnapshotRouter(handler).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/tasks/missing", nil))
	if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), `"code":"TASK_NOT_FOUND"`) {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
}

func graphSnapshotRequest() string {
	return `{"schema_version":"1.0","mode":"full","content_hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","nodes":[],"edges":[]}`
}

func TestPutGraphSnapshotUsesAcceptedAndReplayStatuses(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		result   graphsnapshot.SubmissionCheck
		wantCode int
	}{
		{"new", graphsnapshot.SubmissionCheck{Snapshot: graphsnapshot.Snapshot{Namespace: "project", Version: "revision", TaskID: "task", Status: graphsnapshot.SnapshotBuilding}}, http.StatusAccepted},
		{"building replay", graphsnapshot.SubmissionCheck{Existing: true, Snapshot: graphsnapshot.Snapshot{Namespace: "project", Version: "revision", TaskID: "task", Status: graphsnapshot.SnapshotBuilding}}, http.StatusAccepted},
		{"ready replay", graphsnapshot.SubmissionCheck{Existing: true, Snapshot: graphsnapshot.Snapshot{Namespace: "project", Version: "revision", TaskID: "task", Status: graphsnapshot.SnapshotReady}}, http.StatusOK},
		{"failed replay", graphsnapshot.SubmissionCheck{Existing: true, Snapshot: graphsnapshot.Snapshot{Namespace: "project", Version: "revision", TaskID: "task", Status: graphsnapshot.SnapshotFailed}}, http.StatusOK},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			service := &graphServiceFake{result: testCase.result}
			handler := New(Deps{Config: &config.Config{}, GraphService: service})
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPut, "/v1/graphs/project/snapshots/revision", strings.NewReader(graphSnapshotRequest()))
			request.Header.Set("Content-Type", "application/json")
			newGraphSnapshotRouter(handler).ServeHTTP(response, request)

			if response.Code != testCase.wantCode {
				t.Fatalf("status = %d, want %d: %s", response.Code, testCase.wantCode, response.Body.String())
			}
			if !service.called || service.request.Mode != graphsnapshot.ModeFull || strings.Contains(response.Body.String(), "request_id") {
				t.Fatalf("unexpected submission call or resource: called=%t request=%+v body=%s", service.called, service.request, response.Body.String())
			}
			if response.Header().Get(graphRequestIDHeader) == "" {
				t.Fatal("successful response is missing X-Request-ID")
			}
		})
	}
}

func TestPutGraphSnapshotRejectsStrictJSONAndPayloadLimits(t *testing.T) {
	testCases := []struct {
		name string
		cfg  config.GraphConfig
		body string
	}{
		{"duplicate member", config.GraphConfig{}, `{"schema_version":"1.0","schema_version":"1.0"}`},
		{"node limit", config.GraphConfig{MaxPayloadBytes: 1 << 20, MaxNodes: 1, MaxEdges: 1}, `{"schema_version":"1.0","mode":"full","content_hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","nodes":[{},{}],"edges":[]}`},
		{"payload limit", config.GraphConfig{MaxPayloadBytes: 16, MaxNodes: 1, MaxEdges: 1}, graphSnapshotRequest()},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			service := &graphServiceFake{}
			handler := New(Deps{Config: &config.Config{Graph: testCase.cfg}, GraphService: service})
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPut, "/v1/graphs/project/snapshots/revision", strings.NewReader(testCase.body))
			newGraphSnapshotRouter(handler).ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"INVALID_SNAPSHOT_REQUEST"`) {
				t.Fatalf("response = %d %s", response.Code, response.Body.String())
			}
			if service.called {
				t.Fatal("invalid request reached the graph service")
			}
		})
	}
}

func TestGetGraphSnapshotReturnsCanonicalResourceAndNotFound(t *testing.T) {
	reader := &graphReaderFake{snapshot: graphsnapshot.Snapshot{Namespace: "project", Version: "revision", ContentHash: "hash", TaskID: "task", Status: graphsnapshot.SnapshotReady, QueryReady: true}, found: true}
	handler := New(Deps{Config: &config.Config{}, GraphSnapshotReader: reader})
	router := newGraphSnapshotRouter(handler)

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/graphs/project/snapshots/revision", nil))
	if response.Code != http.StatusOK || reader.namespace != "project" || reader.version != "revision" || !strings.Contains(response.Body.String(), `"query_ready":true`) {
		t.Fatalf("response = %d %s; reader=%q/%q", response.Code, response.Body.String(), reader.namespace, reader.version)
	}

	reader.found = false
	response = httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/graphs/project/snapshots/missing", nil))
	if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), `"code":"SNAPSHOT_NOT_FOUND"`) {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
}
