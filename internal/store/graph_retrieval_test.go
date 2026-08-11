package store

import (
	"context"
	"errors"
	"testing"

	"github.com/Wrath-y/local-rag/internal/graphquery"
	"github.com/Wrath-y/local-rag/internal/graphretrieval"
	"github.com/Wrath-y/local-rag/internal/graphsnapshot"
)

type retrievalEmbedder struct{}

func (retrievalEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	vectors := make([][]float32, len(texts))
	for i := range vectors {
		vectors[i] = []float32{1, 0}
	}
	return vectors, nil
}

func putReadyRetrievalSnapshot(t *testing.T, s *Store, namespace, version, taskID string) {
	t.Helper()
	nodes := []graphsnapshot.Node{
		{ID: "node-a", Type: "person", Label: "A", Text: "alpha searchable", Properties: []byte(`{}`), Provenance: []byte(`{}`)},
		{ID: "node-b", Type: "place", Label: "B", Text: "beta searchable", Properties: []byte(`{}`), Provenance: []byte(`{}`)},
	}
	edges := []graphsnapshot.Edge{{ID: "edge-a-b", From: "node-a", To: "node-b", Type: "contains", RelationKind: "explicit", Confidence: "1", Properties: []byte(`{}`), Provenance: []byte(`{}`)}}
	_, hash, err := graphsnapshot.CanonicalHash(nodes, edges)
	if err != nil {
		t.Fatal(err)
	}
	service := graphsnapshot.NewService(s, func() (string, error) { return taskID, nil })
	if _, graphErr := service.Put(context.Background(), namespace, version, graphsnapshot.Request{SchemaVersion: graphsnapshot.SchemaVersionV1, Mode: graphsnapshot.ModeFull, ContentHash: hash, Nodes: nodes, Edges: edges}); graphErr != nil {
		t.Fatal(graphErr)
	}
	if err = s.PromoteGraphComponent(context.Background(), taskID); err != nil {
		t.Fatal(err)
	}
	if err = s.PopulateGraphSearchDocuments(context.Background(), taskID); err != nil {
		t.Fatal(err)
	}
	if err = s.BuildGraphVectors(context.Background(), taskID, retrievalEmbedder{}); err != nil {
		t.Fatal(err)
	}
}

func TestGraphRetrievalReadBindsSelectedGenerationsAndScopesSeeds(t *testing.T) {
	s, err := New(t.TempDir()+"/rag.db", 2)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	putReadyRetrievalSnapshot(t, s, "one", "v1", "task-one")
	putReadyRetrievalSnapshot(t, s, "two", "v1", "task-two")

	err = s.WithRetrievalRead(context.Background(), "one", "v1", func(view graphretrieval.ReadView) error {
		if view.Version() != "v1" || view.ContentHash() == "" {
			t.Fatalf("resolved version/hash=%q/%q", view.Version(), view.ContentHash())
		}
		fts, ftsState := view.Generation(graphretrieval.StageBM25)
		vector, vectorState := view.Generation(graphretrieval.StageVector)
		if ftsState != graphretrieval.StageUsed || vectorState != graphretrieval.StageUsed || fts.Generation != "fts-task-one" || vector.Generation != "vector-task-one" {
			t.Fatalf("generations fts=%+v/%s vector=%+v/%s", fts, ftsState, vector, vectorState)
		}
		bm25, outcome, readErr := view.BM25(context.Background(), "alpha", []string{"person"}, 10)
		if readErr != nil || outcome != graphretrieval.StageUsed || len(bm25) != 1 || bm25[0].NodeID != "node-a" || bm25[0].Rank != 1 {
			t.Fatalf("bm25=%+v outcome=%s err=%v", bm25, outcome, readErr)
		}
		if empty, emptyOutcome, emptyErr := view.BM25(context.Background(), "missing", nil, 10); emptyErr != nil || emptyOutcome != graphretrieval.StageUsed || len(empty) != 0 {
			t.Fatalf("empty bm25=%+v outcome=%s err=%v", empty, emptyOutcome, emptyErr)
		}
		vectors, outcome, readErr := view.Vector(context.Background(), []float32{1, 0}, nil, 10)
		if readErr != nil || outcome != graphretrieval.StageUsed || len(vectors) != 2 || vectors[0].NodeID != "node-a" || vectors[1].NodeID != "node-b" {
			t.Fatalf("vectors=%+v outcome=%s err=%v", vectors, outcome, readErr)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestGraphRetrievalReadReportsEvictedAndDimensionMismatchWithoutCrossReads(t *testing.T) {
	s, err := New(t.TempDir()+"/rag.db", 2)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	putReadyRetrievalSnapshot(t, s, "project", "v1", "task-project")
	err = s.WithRetrievalRead(context.Background(), "project", "v1", func(view graphretrieval.ReadView) error {
		if vectors, state, readErr := view.Vector(context.Background(), []float32{1}, nil, 10); readErr != nil || state != graphretrieval.StagePermanentFailure || len(vectors) != 0 {
			t.Fatalf("mismatched vector=%v state=%s err=%v", vectors, state, readErr)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB().Exec(`UPDATE graph_retrieval_generations SET state='evicted',selected=0 WHERE namespace='project' AND version='v1' AND component='vector'`); err != nil {
		t.Fatal(err)
	}
	err = s.WithRetrievalRead(context.Background(), "project", "v1", func(view graphretrieval.ReadView) error {
		if _, state := view.Generation(graphretrieval.StageVector); state != graphretrieval.StageIndexEvicted {
			t.Fatalf("vector state=%s", state)
		}
		if vectors, state, readErr := view.Vector(context.Background(), []float32{1, 0}, nil, 10); readErr != nil || state != graphretrieval.StageIndexEvicted || len(vectors) != 0 {
			t.Fatalf("evicted vector=%v state=%s err=%v", vectors, state, readErr)
		}
		if vectors, state, readErr := view.Vector(context.Background(), []float32{1}, nil, 10); readErr != nil || state != graphretrieval.StageIndexEvicted || len(vectors) != 0 {
			t.Fatalf("mismatched evicted vector=%v state=%s err=%v", vectors, state, readErr)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestGraphRetrievalReadResolvesActiveOnceAndUsesStableSnapshotErrors(t *testing.T) {
	s, err := New(t.TempDir()+"/rag.db", 2)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	putReadyRetrievalSnapshot(t, s, "project", "v1", "task-v1")
	putReadyRetrievalSnapshot(t, s, "project", "v2", "task-v2")
	if _, err = s.ActivateGraphSnapshot(context.Background(), "project", "v2"); err != nil {
		t.Fatal(err)
	}
	if err = s.WithRetrievalRead(context.Background(), "project", "", func(view graphretrieval.ReadView) error {
		if view.Version() != "v2" {
			t.Fatalf("active resolution=%q", view.Version())
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err = s.WithRetrievalRead(context.Background(), "project", "missing", func(graphretrieval.ReadView) error { return nil }); !errors.Is(err, graphquery.ErrSnapshotNotFound) {
		t.Fatalf("missing error=%v", err)
	}
	if _, err = s.DB().Exec(`UPDATE graph_snapshots SET status='building',query_ready=0 WHERE namespace='project' AND version='v1'`); err != nil {
		t.Fatal(err)
	}
	if err = s.WithRetrievalRead(context.Background(), "project", "v1", func(graphretrieval.ReadView) error { return nil }); !errors.Is(err, graphquery.ErrSnapshotNotReady) {
		t.Fatalf("not-ready error=%v", err)
	}
}
