package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/Wrath-y/local-rag/internal/graphsnapshot"
)

// BenchmarkGraphSnapshot10k100k is an opt-in integration benchmark for the
// reference sync envelope. It uses no embedder, proving provider work is not
// kept inside the graph/FTS SQLite transactions while reporting each phase.
func BenchmarkGraphSnapshot10k100k(b *testing.B) {
	nodes, edges := benchmarkGraph(10_000, 100_000)
	for i := 0; i < b.N; i++ {
		path := filepath.Join(b.TempDir(), "graph.db")
		st, err := New(path, 4)
		if err != nil {
			b.Fatal(err)
		}
		started := time.Now()
		canonicalStarted := time.Now()
		_, hash, err := graphsnapshot.CanonicalHash(nodes, edges)
		if err != nil {
			b.Fatal(err)
		}
		canonicalElapsed := time.Since(canonicalStarted)
		service := graphsnapshot.NewService(st, func() (string, error) { return fmt.Sprintf("benchmark-task-%d", i), nil })
		if _, graphErr := service.Put(context.Background(), "benchmark", fmt.Sprintf("v-%d", i), graphsnapshot.Request{SchemaVersion: graphsnapshot.SchemaVersionV1, Mode: graphsnapshot.ModeFull, ContentHash: hash, Nodes: nodes, Edges: edges}); graphErr != nil {
			b.Fatal(graphErr)
		}
		task, found, err := st.ClaimOldestQueuedGraphTask(context.Background())
		if err != nil || !found {
			b.Fatalf("claim task found=%v err=%v", found, err)
		}
		graphStarted := time.Now()
		if err = st.PromoteGraphComponent(context.Background(), task.ID); err != nil {
			b.Fatal(err)
		}
		graphElapsed := time.Since(graphStarted)
		ftsStarted := time.Now()
		if err = st.PopulateGraphSearchDocuments(context.Background(), task.ID); err != nil {
			b.Fatal(err)
		}
		ftsElapsed := time.Since(ftsStarted)
		if err = st.MarkGraphVectorUnavailable(context.Background(), task.ID, "benchmark has no provider"); err != nil {
			b.Fatal(err)
		}
		snapshot, found, err := st.LookupGraphSnapshot(context.Background(), "benchmark", fmt.Sprintf("v-%d", i))
		if err != nil || !found || !snapshot.QueryReady {
			b.Fatalf("snapshot=%#v found=%v err=%v", snapshot, found, err)
		}
		b.ReportMetric(float64(canonicalElapsed.Milliseconds()), "canonicalization_ms")
		b.ReportMetric(float64(graphElapsed.Milliseconds()), "materialization_ms")
		b.ReportMetric(float64(ftsElapsed.Milliseconds()), "fts_ms")
		b.ReportMetric(float64(time.Since(started).Milliseconds()), "total_sync_ms")
		if err = st.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkGraphSnapshotDelta10k100k measures the equivalent full-size delta
// materialization path before it enters the same durable worker boundaries.
func BenchmarkGraphSnapshotDelta10k100k(b *testing.B) {
	nodes, edges := benchmarkGraph(10_000, 100_000)
	base := graphsnapshot.Manifest{SchemaVersion: graphsnapshot.SchemaVersionV1, Nodes: nodes, Edges: edges}
	request := graphsnapshot.Request{SchemaVersion: graphsnapshot.SchemaVersionV1, Mode: graphsnapshot.ModeDelta, BaseVersion: "base"}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		started := time.Now()
		manifest, graphErr := graphsnapshot.ApplyDelta(base, request)
		if graphErr != nil {
			b.Fatal(graphErr)
		}
		_, _, err := graphsnapshot.CanonicalHash(manifest.Nodes, manifest.Edges)
		if err != nil {
			b.Fatal(err)
		}
		b.ReportMetric(float64(time.Since(started).Milliseconds()), "delta_canonicalization_ms")
	}
}

func benchmarkGraph(nodeCount, edgeCount int) ([]graphsnapshot.Node, []graphsnapshot.Edge) {
	nodes := make([]graphsnapshot.Node, nodeCount)
	for i := range nodes {
		nodes[i] = graphsnapshot.Node{ID: fmt.Sprintf("node-%05d", i), Type: "asset", Label: fmt.Sprintf("Asset %d", i), Text: "benchmark graph node", Properties: []byte(`{}`), Provenance: []byte(`{"source":"benchmark"}`)}
	}
	edges := make([]graphsnapshot.Edge, edgeCount)
	for i := range edges {
		edges[i] = graphsnapshot.Edge{ID: fmt.Sprintf("edge-%06d", i), From: nodes[i%nodeCount].ID, To: nodes[(i+1)%nodeCount].ID, Type: "related_to", RelationKind: "explicit", Confidence: "1", Properties: []byte(`{}`), Provenance: []byte(`{"source":"benchmark"}`)}
	}
	return nodes, edges
}
