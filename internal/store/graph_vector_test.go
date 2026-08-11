package store

import (
	"context"
	"fmt"
	"testing"

	"github.com/Wrath-y/local-rag/internal/graphsnapshot"
)

type batchingGraphEmbedder struct{ batchSizes []int }

func (e *batchingGraphEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	e.batchSizes = append(e.batchSizes, len(texts))
	vectors := make([][]float32, len(texts))
	for i := range vectors {
		vectors[i] = []float32{1, 0, 0, 0}
	}
	return vectors, nil
}

func TestBuildGraphVectorsUsesBoundedBatchesAndCoversEveryDocument(t *testing.T) {
	s, err := New(t.TempDir()+"/rag.db", 4)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	nodes := make([]graphsnapshot.Node, graphEmbeddingBatchSize+1)
	for i := range nodes {
		nodes[i] = graphsnapshot.Node{ID: fmt.Sprintf("node-%03d", i), Type: "kind", Label: "label", Text: "text", Properties: []byte(`{}`), Provenance: []byte(`{}`)}
	}
	_, hash, err := graphsnapshot.CanonicalHash(nodes, nil)
	if err != nil {
		t.Fatal(err)
	}
	service := graphsnapshot.NewService(s, func() (string, error) { return "batching-task", nil })
	if _, graphErr := service.Put(context.Background(), "project", "batching", graphsnapshot.Request{SchemaVersion: graphsnapshot.SchemaVersionV1, Mode: graphsnapshot.ModeFull, ContentHash: hash, Nodes: nodes}); graphErr != nil {
		t.Fatal(graphErr)
	}
	if err = s.PromoteGraphComponent(context.Background(), "batching-task"); err != nil {
		t.Fatal(err)
	}
	if err = s.PopulateGraphSearchDocuments(context.Background(), "batching-task"); err != nil {
		t.Fatal(err)
	}
	embedder := &batchingGraphEmbedder{}
	if err = s.BuildGraphVectors(context.Background(), "batching-task", embedder); err != nil {
		t.Fatal(err)
	}
	if len(embedder.batchSizes) != 2 || embedder.batchSizes[0] != graphEmbeddingBatchSize || embedder.batchSizes[1] != 1 {
		t.Fatalf("embedding batch sizes=%v", embedder.batchSizes)
	}
	var items int
	if err = s.DB().QueryRow(`SELECT count(*) FROM graph_vector_items WHERE namespace='project' AND version='batching' AND generation='vector-batching-task'`).Scan(&items); err != nil || items != len(nodes) {
		t.Fatalf("vector items=%d err=%v", items, err)
	}
}
