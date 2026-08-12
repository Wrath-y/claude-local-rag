package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/Wrath-y/local-rag/internal/graphquery"
	"github.com/Wrath-y/local-rag/internal/graphsnapshot"
	"github.com/Wrath-y/local-rag/internal/operability"
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

// BenchmarkGraphRebuild10k100k measures the private rebuild path separately
// from initial ingestion. Reported allocations provide the memory bound while
// per-phase metrics keep provider work, SQLite transactions, validation, and
// promotion visible without imposing a public latency guarantee.
func BenchmarkGraphRebuild10k100k(b *testing.B) {
	nodes, edges := benchmarkGraph(10_000, 100_000)
	for i := 0; i < b.N; i++ {
		st, err := New(filepath.Join(b.TempDir(), "graph.db"), 4)
		if err != nil {
			b.Fatal(err)
		}
		service := graphsnapshot.NewService(st, func() (string, error) { return "initial", nil })
		_, hash, err := graphsnapshot.CanonicalHash(nodes, edges)
		if err != nil {
			b.Fatal(err)
		}
		if _, graphErr := service.Put(context.Background(), "benchmark", "v1", graphsnapshot.Request{SchemaVersion: graphsnapshot.SchemaVersionV1, Mode: graphsnapshot.ModeFull, ContentHash: hash, Nodes: nodes, Edges: edges}); graphErr != nil {
			b.Fatal(graphErr)
		}
		initial, found, err := st.ClaimOldestQueuedGraphTask(context.Background())
		if err != nil || !found {
			b.Fatalf("claim initial found=%v err=%v", found, err)
		}
		if err = st.PromoteGraphComponent(context.Background(), initial.ID); err != nil {
			b.Fatal(err)
		}
		if err = st.PopulateGraphSearchDocuments(context.Background(), initial.ID); err != nil {
			b.Fatal(err)
		}
		if err = st.BuildGraphVectors(context.Background(), initial.ID, graphEmbedderFake{}); err != nil {
			b.Fatal(err)
		}
		if _, err = st.DB().Exec(`UPDATE graph_tasks SET state='succeeded',phase='completed',progress=10000 WHERE id='initial'`); err != nil {
			b.Fatal(err)
		}
		before, err := benchmarkAdjacency(st)
		if err != nil {
			b.Fatal(err)
		}
		components := []operability.Component{operability.ComponentGraphIndexes, operability.ComponentFTS, operability.ComponentVector}
		if _, _, err = st.AdmitGraphRebuild(context.Background(), "benchmark", "v1", "benchmark", operability.RequestFingerprint(components), "benchmark", "rebuild", components); err != nil {
			b.Fatal(err)
		}
		task, found, err := st.ClaimOldestQueuedGraphTask(context.Background())
		if err != nil || !found {
			b.Fatalf("claim rebuild found=%v err=%v", found, err)
		}
		b.ResetTimer()
		started := time.Now()
		phase := time.Now()
		if _, err = st.ReadTrustedGraphRebuildSource(context.Background(), "benchmark", "v1"); err != nil {
			b.Fatal(err)
		}
		b.ReportMetric(float64(time.Since(phase).Milliseconds()), "source_verify_ms")
		phase = time.Now()
		if _, err = st.BuildPrivateGraphIndexes(context.Background(), task.ID); err != nil {
			b.Fatal(err)
		}
		b.ReportMetric(float64(time.Since(phase).Milliseconds()), "private_indexes_ms")
		phase = time.Now()
		if _, err = st.BuildPrivateGraphFTS(context.Background(), task.ID); err != nil {
			b.Fatal(err)
		}
		b.ReportMetric(float64(time.Since(phase).Milliseconds()), "private_fts_ms")
		phase = time.Now()
		if _, err = st.BuildPrivateGraphVectors(context.Background(), task.ID, graphEmbedderFake{}); err != nil {
			b.Fatal(err)
		}
		b.ReportMetric(float64(time.Since(phase).Milliseconds()), "private_vector_ms")
		phase = time.Now()
		if err = st.promoteGraphIndexRebuild(context.Background(), task.ID); err != nil {
			b.Fatal(err)
		}
		after, err := benchmarkAdjacency(st)
		if err != nil || len(before) != len(after) {
			b.Fatalf("fixed traverse adjacency changed before=%d after=%d err=%v", len(before), len(after), err)
		}
		for index := range before {
			if before[index] != after[index] {
				b.Fatalf("fixed traverse adjacency changed before=%v after=%v", before, after)
			}
		}
		b.ReportMetric(float64(time.Since(phase).Milliseconds()), "promotion_ms")
		b.ReportMetric(float64(time.Since(started).Milliseconds()), "rebuild_total_ms")
		b.StopTimer()
		if err = st.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

func benchmarkAdjacency(st *Store) ([]string, error) {
	var ids []string
	err := st.WithRead(context.Background(), "benchmark", "v1", func(view graphquery.ReadView) error {
		edges, err := view.Adjacency(context.Background(), []string{"node-00000"}, graphquery.DirectionOutgoing)
		if err != nil {
			return err
		}
		for _, edge := range edges {
			ids = append(ids, edge.ID)
		}
		return nil
	})
	return ids, err
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
