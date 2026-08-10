package store

import (
	"context"
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
