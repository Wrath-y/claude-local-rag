package store

import (
	"context"
	"errors"
	"testing"

	"github.com/Wrath-y/local-rag/internal/graphsnapshot"
)

type failingGraphEmbedder struct{}

func (failingGraphEmbedder) Embed(context.Context, []string) ([][]float32, error) {
	return nil, errors.New("embedding provider unavailable")
}

func TestServiceDispatchCompletesDurableGraphTask(t *testing.T) {
	s, err := New(t.TempDir()+"/rag.db", 4)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	node := graphsnapshot.Node{ID: "node", Type: "kind", Label: "label", Text: "text", Properties: []byte(`{}`), Provenance: []byte(`{}`)}
	_, hash, err := graphsnapshot.CanonicalHash([]graphsnapshot.Node{node}, nil)
	if err != nil {
		t.Fatal(err)
	}
	service := graphsnapshot.NewService(s, func() (string, error) { return "dispatch-task", nil })
	if _, graphErr := service.Put(context.Background(), "project", "revision", graphsnapshot.Request{SchemaVersion: graphsnapshot.SchemaVersionV1, Mode: graphsnapshot.ModeFull, ContentHash: hash, Nodes: []graphsnapshot.Node{node}}); graphErr != nil {
		t.Fatal(graphErr)
	}
	task, found, err := s.ClaimOldestQueuedGraphTask(context.Background())
	if err != nil || !found {
		t.Fatalf("claim task=%#v found=%v err=%v", task, found, err)
	}
	if err = service.Dispatch(context.Background(), task, graphEmbedderFake{}); err != nil {
		t.Fatal(err)
	}
	storedTask, found, err := s.LookupGraphTask(context.Background(), task.ID)
	if err != nil || !found || storedTask.State != graphsnapshot.TaskSucceeded || storedTask.Progress != 1 {
		t.Fatalf("task=%#v found=%v err=%v", storedTask, found, err)
	}
	snapshot, found, err := s.LookupGraphSnapshot(context.Background(), "project", "revision")
	if err != nil || !found || snapshot.Status != graphsnapshot.SnapshotReady || !snapshot.QueryReady {
		t.Fatalf("snapshot=%#v found=%v err=%v", snapshot, found, err)
	}
}

func TestServiceDispatchDegradesVectorFailure(t *testing.T) {
	s, err := New(t.TempDir()+"/rag.db", 4)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	node := graphsnapshot.Node{ID: "node", Type: "kind", Label: "label", Text: "text", Properties: []byte(`{}`), Provenance: []byte(`{}`)}
	_, hash, err := graphsnapshot.CanonicalHash([]graphsnapshot.Node{node}, nil)
	if err != nil {
		t.Fatal(err)
	}
	service := graphsnapshot.NewService(s, func() (string, error) { return "vector-failure-task", nil })
	if _, graphErr := service.Put(context.Background(), "project", "revision", graphsnapshot.Request{SchemaVersion: graphsnapshot.SchemaVersionV1, Mode: graphsnapshot.ModeFull, ContentHash: hash, Nodes: []graphsnapshot.Node{node}}); graphErr != nil {
		t.Fatal(graphErr)
	}
	task, found, err := s.ClaimOldestQueuedGraphTask(context.Background())
	if err != nil || !found {
		t.Fatalf("claim task=%#v found=%v err=%v", task, found, err)
	}
	if err = service.Dispatch(context.Background(), task, failingGraphEmbedder{}); err != nil {
		t.Fatal(err)
	}
	storedTask, found, err := s.LookupGraphTask(context.Background(), task.ID)
	if err != nil || !found || storedTask.State != graphsnapshot.TaskSucceeded {
		t.Fatalf("task=%#v found=%v err=%v", storedTask, found, err)
	}
	snapshot, found, err := s.LookupGraphSnapshot(context.Background(), "project", "revision")
	if err != nil || !found || snapshot.Status != graphsnapshot.SnapshotReady || len(snapshot.Warnings) != 1 || snapshot.Components[2].State != graphsnapshot.ComponentFailed {
		t.Fatalf("snapshot=%#v found=%v err=%v", snapshot, found, err)
	}
}

func TestServiceDispatchMakesRequiredComponentFailureTerminal(t *testing.T) {
	s, err := New(t.TempDir()+"/rag.db", 4)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	node := graphsnapshot.Node{ID: "node", Type: "kind", Label: "label", Text: "text", Properties: []byte(`{}`), Provenance: []byte(`{}`)}
	_, hash, err := graphsnapshot.CanonicalHash([]graphsnapshot.Node{node}, nil)
	if err != nil {
		t.Fatal(err)
	}
	service := graphsnapshot.NewService(s, func() (string, error) { return "required-failure-task", nil })
	if _, graphErr := service.Put(context.Background(), "project", "required-failure", graphsnapshot.Request{SchemaVersion: graphsnapshot.SchemaVersionV1, Mode: graphsnapshot.ModeFull, ContentHash: hash, Nodes: []graphsnapshot.Node{node}}); graphErr != nil {
		t.Fatal(graphErr)
	}
	if _, err = s.DB().Exec(`CREATE TRIGGER fail_required_graph_component BEFORE INSERT ON graph_nodes WHEN NEW.version='required-failure' BEGIN SELECT RAISE(ABORT,'injected graph failure'); END`); err != nil {
		t.Fatal(err)
	}
	task, found, err := s.ClaimOldestQueuedGraphTask(context.Background())
	if err != nil || !found {
		t.Fatalf("claim task=%#v found=%v err=%v", task, found, err)
	}
	if err = service.Dispatch(context.Background(), task, graphEmbedderFake{}); err != nil {
		t.Fatal(err)
	}
	storedTask, found, err := s.LookupGraphTask(context.Background(), task.ID)
	if err != nil || !found || storedTask.State != graphsnapshot.TaskFailed || storedTask.Error == nil || storedTask.Error.Code != graphsnapshot.CodeInternalError {
		t.Fatalf("task=%#v found=%v err=%v", storedTask, found, err)
	}
	snapshot, found, err := s.LookupGraphSnapshot(context.Background(), "project", "required-failure")
	if err != nil || !found || snapshot.Status != graphsnapshot.SnapshotFailed || snapshot.QueryReady || snapshot.Components[0].State != graphsnapshot.ComponentFailed {
		t.Fatalf("snapshot=%#v found=%v err=%v", snapshot, found, err)
	}
}
