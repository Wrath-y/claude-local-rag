package store

import "testing"

// Graph tables are additive: legacy chunk retrieval, durable sync, and index
// rebuild snapshots continue to operate while multiple namespaces coexist.
func TestLegacyChunkSyncAndRebuildRemainIsolatedFromGraphNamespaces(t *testing.T) {
	s, err := New(t.TempDir()+"/rag.db", 2)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	seedGraphSnapshot(t, s, "graph-a", "v1", "a")
	seedGraphSnapshot(t, s, "graph-b", "v1", "b")
	if _, err = s.InsertChunk("legacy redis cache", "legacy", "legacy-md5", "", "", []float32{1, 0}); err != nil {
		t.Fatal(err)
	}
	results, err := s.Retrieve([]float32{1, 0}, "redis", RetrieveOpts{TopK: 3, CandidateMultiplier: 3, VectorWeight: .7, BM25Weight: .3})
	if err != nil || len(results) != 1 || results[0].Source != "legacy" {
		t.Fatalf("results=%#v err=%v", results, err)
	}
	runSync(t, s, testSnapshot("legacy-sync", testDocument("doc", SyncChunk{Key: "one", Content: "sync content"})))
	chunks, err := s.SnapshotChunks()
	if err != nil || len(chunks) != 2 {
		t.Fatalf("chunks=%d err=%v", len(chunks), err)
	}
	var namespaces int
	if err = s.DB().QueryRow(`SELECT count(*) FROM graph_namespaces`).Scan(&namespaces); err != nil || namespaces != 2 {
		t.Fatalf("namespaces=%d err=%v", namespaces, err)
	}
}
