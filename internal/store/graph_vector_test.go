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
	var ftsGeneration, ftsAlgorithm, ftsTokenizer, ftsDigest string
	if err = s.DB().QueryRow(`SELECT generation,algorithm,tokenizer,content_digest FROM graph_retrieval_generations WHERE namespace='project' AND version='batching' AND component='fts' AND selected=1`).Scan(&ftsGeneration, &ftsAlgorithm, &ftsTokenizer, &ftsDigest); err != nil {
		t.Fatal(err)
	}
	if ftsGeneration != "fts-batching-task" || ftsAlgorithm != graphsnapshot.SearchDocumentFormatV1+"/fts5" || ftsTokenizer != "unicode61" || len(ftsDigest) != 64 {
		t.Fatalf("fts metadata generation=%q algorithm=%q tokenizer=%q digest=%q", ftsGeneration, ftsAlgorithm, ftsTokenizer, ftsDigest)
	}
	var vectorGeneration, vectorAlgorithm, vectorDigest string
	var dimensions int
	if err = s.DB().QueryRow(`SELECT generation,algorithm,dimensions,content_digest FROM graph_retrieval_generations WHERE namespace='project' AND version='batching' AND component='vector' AND selected=1`).Scan(&vectorGeneration, &vectorAlgorithm, &dimensions, &vectorDigest); err != nil {
		t.Fatal(err)
	}
	if vectorGeneration != "vector-batching-task" || vectorAlgorithm != graphsnapshot.SearchDocumentFormatV1+"/embedding" || dimensions != 4 || len(vectorDigest) != 64 {
		t.Fatalf("vector metadata generation=%q algorithm=%q dimensions=%d digest=%q", vectorGeneration, vectorAlgorithm, dimensions, vectorDigest)
	}
}
