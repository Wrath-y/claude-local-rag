package operability

import (
	"context"
	"errors"
	"testing"

	"github.com/Wrath-y/local-rag/internal/graphsnapshot"
)

type rebuildRepositoryFake struct {
	task     graphsnapshot.Task
	replayed bool
	err      error
	inputs   []Component
}

func (f *rebuildRepositoryFake) AdmitGraphRebuild(_ context.Context, _, _, _, _, _, _ string, components []Component) (graphsnapshot.Task, bool, error) {
	f.inputs = append([]Component(nil), components...)
	return f.task, f.replayed, f.err
}

func TestRebuildServiceNormalizesBeforeAdmissionAndWakesOnlyNewTasks(t *testing.T) {
	repo := &rebuildRepositoryFake{task: graphsnapshot.Task{ID: "task", State: graphsnapshot.TaskQueued}}
	wake := &wakeFake{}
	service := RebuildService{Repository: repo, Waker: wake, NewTaskID: func() string { return "task" }}
	result, graphErr := service.Submit(context.Background(), "project", "version", "request-1", "req-1", RebuildRequest{Components: []Component{ComponentVector, ComponentFTS}})
	if graphErr != nil || result.TaskID != "task" || len(repo.inputs) != 2 || repo.inputs[0] != ComponentFTS || wake.calls != 1 {
		t.Fatalf("result=%+v err=%v inputs=%v wakes=%d", result, graphErr, repo.inputs, wake.calls)
	}
	repo.replayed = true
	if _, graphErr = service.Submit(context.Background(), "project", "version", "request-1", "req-1", RebuildRequest{Components: []Component{ComponentFTS, ComponentVector}}); graphErr != nil || wake.calls != 1 {
		t.Fatalf("replay err=%v wakes=%d", graphErr, wake.calls)
	}
}

func TestRebuildServiceMapsStableAdmissionErrors(t *testing.T) {
	for _, testCase := range []struct {
		err  error
		code graphsnapshot.Code
	}{
		{ErrReimportRequired, graphsnapshot.CodeReimportRequired},
		{ErrIdempotencyConflict, graphsnapshot.CodeIdempotencyConflict},
		{ErrSnapshotNotFound, graphsnapshot.CodeSnapshotNotFound},
		{errors.New("private path"), graphsnapshot.CodeInternalError},
	} {
		service := RebuildService{Repository: &rebuildRepositoryFake{err: testCase.err}}
		_, graphErr := service.Submit(context.Background(), "project", "version", "key", "req", RebuildRequest{Components: []Component{ComponentFTS}})
		if graphErr == nil || graphErr.Code != testCase.code {
			t.Fatalf("err=%v code=%v", graphErr, testCase.code)
		}
	}
}

type wakeFake struct{ calls int }

func (w *wakeFake) Wake() { w.calls++ }
