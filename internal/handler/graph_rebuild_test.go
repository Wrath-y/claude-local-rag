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
	"github.com/Wrath-y/local-rag/internal/operability"
)

type graphRebuildFake struct {
	calls   int
	key     string
	request operability.RebuildRequest
	err     *graphsnapshot.Error
}

func (f *graphRebuildFake) Submit(_ context.Context, _, _, key, _ string, request operability.RebuildRequest) (operability.RebuildSubmission, *graphsnapshot.Error) {
	f.calls++
	f.key, f.request = key, request
	if f.err != nil {
		return operability.RebuildSubmission{}, f.err
	}
	return operability.RebuildSubmission{TaskID: "task-1", State: graphsnapshot.TaskQueued, TaskURL: "/v1/tasks/task-1", Components: request.Components, Namespace: "project", SnapshotVersion: "v1"}, nil
}

func graphRebuildRouter(h *Handler) *gin.Engine {
	router := gin.New()
	router.Use(GraphRequestID())
	router.POST("/v1/graphs/:namespace/snapshots/:version/rebuild", h.RebuildGraphSnapshot)
	return router
}

func TestRebuildGraphSnapshotValidatesBeforeSubmission(t *testing.T) {
	for _, body := range []string{`{"components":["fts"],"components":["vector"]}`, `{"components":["fts"],"other":true}`} {
		fake := &graphRebuildFake{}
		h := New(Deps{Config: &config.Config{}, GraphRebuild: fake})
		request := httptest.NewRequest(http.MethodPost, "/v1/graphs/project/snapshots/v1/rebuild", strings.NewReader(body))
		request.Header.Set("Idempotency-Key", "key")
		response := httptest.NewRecorder()
		graphRebuildRouter(h).ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest || fake.calls != 0 || !strings.Contains(response.Body.String(), `"code":"INVALID_REBUILD_REQUEST"`) {
			t.Fatalf("body=%s status=%d calls=%d response=%s", body, response.Code, fake.calls, response.Body.String())
		}
	}
}

func TestRebuildGraphSnapshotReturnsLocationAndRequestID(t *testing.T) {
	fake := &graphRebuildFake{}
	h := New(Deps{Config: &config.Config{}, GraphRebuild: fake})
	request := httptest.NewRequest(http.MethodPost, "/v1/graphs/project/snapshots/v1/rebuild", strings.NewReader(`{"components":["vector","fts"]}`))
	request.Header.Set("Idempotency-Key", "key-1")
	request.Header.Set(graphRequestIDHeader, "rebuild-request")
	response := httptest.NewRecorder()
	graphRebuildRouter(h).ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || fake.calls != 1 || fake.key != "key-1" || response.Header().Get("Location") != "/v1/tasks/task-1" || response.Header().Get(graphRequestIDHeader) != "rebuild-request" {
		t.Fatalf("status=%d fake=%+v headers=%v", response.Code, fake, response.Header())
	}
}
