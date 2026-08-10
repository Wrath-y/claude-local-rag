package store

import (
	"context"
	"testing"

	"github.com/Wrath-y/local-rag/internal/graphsnapshot"
)

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
}
