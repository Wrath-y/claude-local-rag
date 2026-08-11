package store

import (
	"context"
	"testing"

	"github.com/Wrath-y/local-rag/internal/graphsnapshot"
)

type graphEmbedderFake struct{}

func (graphEmbedderFake) Embed(_ context.Context, texts []string) ([][]float32, error) {
	vectors := make([][]float32, len(texts))
	for i := range vectors {
		vectors[i] = []float32{1, 0, 0, 0}
	}
	return vectors, nil
}

func TestPromoteGraphComponentMaterializesVerifiedStaging(t *testing.T) {
	s, err := New(t.TempDir()+"/rag.db", 4)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	node := graphsnapshot.Node{ID: "node", Type: "kind", Label: "label", Text: "searchable text", Properties: []byte(`{}`), Provenance: []byte(`{}`)}
	edge := graphsnapshot.Edge{ID: "edge", From: "node", To: "node", Type: "kind", RelationKind: "explicit", Confidence: "1", Properties: []byte(`{}`), Provenance: []byte(`{}`)}
	_, hash, err := graphsnapshot.CanonicalHash([]graphsnapshot.Node{node}, []graphsnapshot.Edge{edge})
	if err != nil {
		t.Fatal(err)
	}
	service := graphsnapshot.NewService(s, func() (string, error) { return "promotion-task", nil })
	if _, graphErr := service.Put(context.Background(), "project", "revision", graphsnapshot.Request{SchemaVersion: graphsnapshot.SchemaVersionV1, Mode: graphsnapshot.ModeFull, ContentHash: hash, Nodes: []graphsnapshot.Node{node}, Edges: []graphsnapshot.Edge{edge}}); graphErr != nil {
		t.Fatal(graphErr)
	}
	if err := s.PromoteGraphComponent(context.Background(), "promotion-task"); err != nil {
		t.Fatal(err)
	}
	var nodes int
	if err := s.DB().QueryRow(`SELECT count(*) FROM graph_nodes WHERE namespace='project' AND version='revision'`).Scan(&nodes); err != nil || nodes != 1 {
		t.Fatalf("nodes=%d err=%v", nodes, err)
	}
	var state string
	if err := s.DB().QueryRow(`SELECT state FROM graph_snapshot_components WHERE namespace='project' AND version='revision' AND component='graph'`).Scan(&state); err != nil || state != "ready" {
		t.Fatalf("state=%q err=%v", state, err)
	}
	if err := s.PopulateGraphSearchDocuments(context.Background(), "promotion-task"); err != nil {
		t.Fatal(err)
	}
	var documents, fts int
	if err := s.DB().QueryRow(`SELECT count(*) FROM graph_search_documents WHERE namespace='project' AND version='revision'`).Scan(&documents); err != nil || documents != 2 {
		t.Fatalf("documents=%d err=%v", documents, err)
	}
	if err := s.DB().QueryRow(`SELECT count(*) FROM graph_search_fts WHERE graph_search_fts MATCH 'searchable'`).Scan(&fts); err != nil || fts != 1 {
		t.Fatalf("fts=%d err=%v", fts, err)
	}
	if err := s.DB().QueryRow(`SELECT state FROM graph_snapshot_components WHERE namespace='project' AND version='revision' AND component='fts'`).Scan(&state); err != nil || state != "ready" {
		t.Fatalf("fts state=%q err=%v", state, err)
	}
	if err := s.BuildGraphVectors(context.Background(), "promotion-task", graphEmbedderFake{}); err != nil {
		t.Fatal(err)
	}
	var vectors int
	if err := s.DB().QueryRow(`SELECT count(*) FROM graph_vector_items WHERE namespace='project' AND version='revision'`).Scan(&vectors); err != nil || vectors != 2 {
		t.Fatalf("vectors=%d err=%v", vectors, err)
	}
	if err := s.DB().QueryRow(`SELECT state FROM graph_snapshot_components WHERE namespace='project' AND version='revision' AND component='vector'`).Scan(&state); err != nil || state != "ready" {
		t.Fatalf("vector state=%q err=%v", state, err)
	}
	if snapshot, found, err := s.LookupGraphSnapshot(context.Background(), "project", "revision"); err != nil || !found || snapshot.Status != graphsnapshot.SnapshotReady || !snapshot.QueryReady {
		t.Fatalf("snapshot=%#v found=%v err=%v", snapshot, found, err)
	}
	if _, err := s.DB().Exec(`UPDATE graph_tasks SET state='running',phase='vector' WHERE id='promotion-task'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`INSERT INTO graph_vector_items(namespace,version,generation,entity_kind,entity_id,dimensions) VALUES('project','revision','private-crash','node','private-node',4)`); err != nil {
		t.Fatal(err)
	}
	if err := s.RecoverGraphTasks(context.Background()); err != nil {
		t.Fatal(err)
	}
	var taskState string
	if err := s.DB().QueryRow(`SELECT state FROM graph_tasks WHERE id='promotion-task'`).Scan(&taskState); err != nil || taskState != "queued" {
		t.Fatalf("task state=%q err=%v", taskState, err)
	}
	var selected, private int
	if err := s.DB().QueryRow(`SELECT count(*) FROM graph_vector_items WHERE generation='vector-promotion-task'`).Scan(&selected); err != nil || selected != 2 {
		t.Fatalf("selected=%d err=%v", selected, err)
	}
	if err := s.DB().QueryRow(`SELECT count(*) FROM graph_vector_items WHERE generation='private-crash'`).Scan(&private); err != nil || private != 0 {
		t.Fatalf("private=%d err=%v", private, err)
	}
}

func TestPromoteGraphComponentRollsBackAllRowsOnWriteFailure(t *testing.T) {
	s, err := New(t.TempDir()+"/rag.db", 4)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	node := graphsnapshot.Node{ID: "node", Type: "kind", Label: "label", Text: "text", Properties: []byte(`{}`), Provenance: []byte(`{}`)}
	edge := graphsnapshot.Edge{ID: "edge", From: "node", To: "node", Type: "kind", RelationKind: "explicit", Confidence: "1", Properties: []byte(`{}`), Provenance: []byte(`{}`)}
	_, hash, err := graphsnapshot.CanonicalHash([]graphsnapshot.Node{node}, []graphsnapshot.Edge{edge})
	if err != nil {
		t.Fatal(err)
	}
	service := graphsnapshot.NewService(s, func() (string, error) { return "rollback-task", nil })
	if _, graphErr := service.Put(context.Background(), "project", "rollback", graphsnapshot.Request{SchemaVersion: graphsnapshot.SchemaVersionV1, Mode: graphsnapshot.ModeFull, ContentHash: hash, Nodes: []graphsnapshot.Node{node}, Edges: []graphsnapshot.Edge{edge}}); graphErr != nil {
		t.Fatal(graphErr)
	}
	if _, err = s.DB().Exec(`CREATE TRIGGER fail_graph_promotion BEFORE INSERT ON graph_edges WHEN NEW.version='rollback' BEGIN SELECT RAISE(ABORT,'injected graph promotion failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err = s.PromoteGraphComponent(context.Background(), "rollback-task"); err == nil {
		t.Fatal("promotion unexpectedly succeeded")
	}
	var nodes, edges int
	if err = s.DB().QueryRow(`SELECT count(*) FROM graph_nodes WHERE namespace='project' AND version='rollback'`).Scan(&nodes); err != nil || nodes != 0 {
		t.Fatalf("partial nodes=%d err=%v", nodes, err)
	}
	if err = s.DB().QueryRow(`SELECT count(*) FROM graph_edges WHERE namespace='project' AND version='rollback'`).Scan(&edges); err != nil || edges != 0 {
		t.Fatalf("partial edges=%d err=%v", edges, err)
	}
	var state string
	if err = s.DB().QueryRow(`SELECT state FROM graph_snapshot_components WHERE namespace='project' AND version='rollback' AND component='graph'`).Scan(&state); err != nil || state != "pending" {
		t.Fatalf("graph component state=%q err=%v", state, err)
	}
}
