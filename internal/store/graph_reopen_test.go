package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/Wrath-y/local-rag/internal/graphsnapshot"
)

func TestGraphReadyStateSurvivesCloseAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rag.db")
	s, err := New(path, 4)
	if err != nil {
		t.Fatal(err)
	}
	node := graphsnapshot.Node{ID: "node", Type: "kind", Label: "label", Text: "text", Properties: []byte(`{}`), Provenance: []byte(`{}`)}
	_, hash, err := graphsnapshot.CanonicalHash([]graphsnapshot.Node{node}, nil)
	if err != nil {
		t.Fatal(err)
	}
	service := graphsnapshot.NewService(s, func() (string, error) { return "reopen-task", nil })
	if _, graphErr := service.Put(context.Background(), "project", "revision", graphsnapshot.Request{SchemaVersion: graphsnapshot.SchemaVersionV1, Mode: graphsnapshot.ModeFull, ContentHash: hash, Nodes: []graphsnapshot.Node{node}}); graphErr != nil {
		t.Fatal(graphErr)
	}
	if err = s.PromoteGraphComponent(context.Background(), "reopen-task"); err != nil {
		t.Fatal(err)
	}
	if err = s.PopulateGraphSearchDocuments(context.Background(), "reopen-task"); err != nil {
		t.Fatal(err)
	}
	if err = s.MarkGraphVectorUnavailable(context.Background(), "reopen-task", "no provider"); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = New(path, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	snapshot, found, err := s.LookupGraphSnapshot(context.Background(), "project", "revision")
	if err != nil || !found || snapshot.Status != graphsnapshot.SnapshotReady || !snapshot.QueryReady || snapshot.TaskID != "reopen-task" {
		t.Fatalf("snapshot=%#v found=%v err=%v", snapshot, found, err)
	}
	var tasks int
	if err = s.DB().QueryRow(`SELECT count(*) FROM graph_tasks WHERE namespace='project' AND version='revision'`).Scan(&tasks); err != nil || tasks != 1 {
		t.Fatalf("tasks=%d err=%v", tasks, err)
	}
}

func TestGraphLifecycleBoundariesSurviveCloseAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rag.db")
	s, err := New(path, 4)
	if err != nil {
		t.Fatal(err)
	}
	node := graphsnapshot.Node{ID: "node", Type: "kind", Label: "label", Text: "text", Properties: []byte(`{}`), Provenance: []byte(`{}`)}
	_, hash, err := graphsnapshot.CanonicalHash([]graphsnapshot.Node{node}, nil)
	if err != nil {
		t.Fatal(err)
	}
	service := graphsnapshot.NewService(s, func() (string, error) { return "boundary-task", nil })
	if _, graphErr := service.Put(context.Background(), "project", "boundary", graphsnapshot.Request{SchemaVersion: graphsnapshot.SchemaVersionV1, Mode: graphsnapshot.ModeFull, ContentHash: hash, Nodes: []graphsnapshot.Node{node}}); graphErr != nil {
		t.Fatal(graphErr)
	}
	if err = s.PromoteGraphComponent(context.Background(), "boundary-task"); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = New(path, 4)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RecoverGraphTasks(context.Background()); err != nil {
		t.Fatal(err)
	}
	snapshot, found, err := s.LookupGraphSnapshot(context.Background(), "project", "boundary")
	if err != nil || !found || snapshot.ContentHash != hash || snapshot.Components[0].State != graphsnapshot.ComponentReady || snapshot.Components[1].State != graphsnapshot.ComponentPending || snapshot.Components[2].State != graphsnapshot.ComponentPending {
		t.Fatalf("graph boundary snapshot=%#v found=%v err=%v", snapshot, found, err)
	}
	var documents, tasks int
	if err = s.DB().QueryRow(`SELECT count(*) FROM graph_search_documents WHERE namespace='project' AND version='boundary'`).Scan(&documents); err != nil || documents != 0 {
		t.Fatalf("graph boundary documents=%d err=%v", documents, err)
	}
	if err = s.DB().QueryRow(`SELECT count(*) FROM graph_tasks WHERE namespace='project' AND version='boundary'`).Scan(&tasks); err != nil || tasks != 1 {
		t.Fatalf("graph boundary tasks=%d err=%v", tasks, err)
	}
	if err = s.PopulateGraphSearchDocuments(context.Background(), "boundary-task"); err != nil {
		t.Fatal(err)
	}
	if err = s.BuildGraphVectors(context.Background(), "boundary-task", graphEmbedderFake{}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ActivateGraphSnapshot(context.Background(), "project", "boundary"); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = New(path, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	snapshot, found, err = s.LookupGraphSnapshot(context.Background(), "project", "boundary")
	if err != nil || !found || snapshot.ContentHash != hash || snapshot.Status != graphsnapshot.SnapshotReady || !snapshot.QueryReady || snapshot.Components[2].State != graphsnapshot.ComponentReady {
		t.Fatalf("ready boundary snapshot=%#v found=%v err=%v", snapshot, found, err)
	}
	var active string
	if err = s.DB().QueryRow(`SELECT active_version FROM graph_namespace_heads WHERE namespace='project'`).Scan(&active); err != nil || active != "boundary" {
		t.Fatalf("active=%q err=%v", active, err)
	}
}

func TestFailedGraphTaskSurvivesCloseAndReopenWithoutActivePointer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rag.db")
	s, err := New(path, 4)
	if err != nil {
		t.Fatal(err)
	}
	node := graphsnapshot.Node{ID: "node", Type: "kind", Label: "label", Text: "text", Properties: []byte(`{}`), Provenance: []byte(`{}`)}
	_, hash, err := graphsnapshot.CanonicalHash([]graphsnapshot.Node{node}, nil)
	if err != nil {
		t.Fatal(err)
	}
	service := graphsnapshot.NewService(s, func() (string, error) { return "failed-reopen-task", nil })
	if _, graphErr := service.Put(context.Background(), "project", "failed", graphsnapshot.Request{SchemaVersion: graphsnapshot.SchemaVersionV1, Mode: graphsnapshot.ModeFull, ContentHash: hash, Nodes: []graphsnapshot.Node{node}}); graphErr != nil {
		t.Fatal(graphErr)
	}
	task, found, err := s.ClaimOldestQueuedGraphTask(context.Background())
	if err != nil || !found {
		t.Fatalf("claim task=%#v found=%v err=%v", task, found, err)
	}
	if err = s.FailRequiredGraphComponent(context.Background(), task.ID, graphsnapshot.ComponentGraph, graphsnapshot.NewError(graphsnapshot.CodeInternalError, nil, errors.New("injected failure"))); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = New(path, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	snapshot, found, err := s.LookupGraphSnapshot(context.Background(), "project", "failed")
	if err != nil || !found || snapshot.ContentHash != hash || snapshot.Status != graphsnapshot.SnapshotFailed || snapshot.QueryReady || snapshot.Components[0].State != graphsnapshot.ComponentFailed {
		t.Fatalf("failed snapshot=%#v found=%v err=%v", snapshot, found, err)
	}
	task, found, err = s.LookupGraphTask(context.Background(), "failed-reopen-task")
	if err != nil || !found || task.State != graphsnapshot.TaskFailed || task.Error == nil {
		t.Fatalf("failed task=%#v found=%v err=%v", task, found, err)
	}
	var active string
	err = s.DB().QueryRow(`SELECT active_version FROM graph_namespace_heads WHERE namespace='project'`).Scan(&active)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("failed snapshot unexpectedly changed active pointer=%q err=%v", active, err)
	}
}
