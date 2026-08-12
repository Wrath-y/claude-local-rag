package store

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Wrath-y/local-rag/internal/graphquery"
	"github.com/Wrath-y/local-rag/internal/graphretrieval"
	"github.com/Wrath-y/local-rag/internal/operability"
)

// TestGraphRebuildPreservesFixedQueryAndRetrievalResults keeps the comparison
// deliberately at the public service boundary. Generation identifiers are
// expected to change; the immutable query/retrieval result data must not.
func TestGraphRebuildPreservesFixedQueryAndRetrievalResults(t *testing.T) {
	s, err := New(t.TempDir()+"/rag.db", 2)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	putReadyRetrievalSnapshot(t, s, "equivalence-a", "v1", "initial-a")
	putReadyRetrievalSnapshot(t, s, "equivalence-b", "v1", "initial-b")
	if _, err = s.DB().Exec(`UPDATE graph_tasks SET state='succeeded',phase='completed',progress=10000 WHERE id IN ('initial-a','initial-b')`); err != nil {
		t.Fatal(err)
	}
	before := fixedGraphResults(t, s, "equivalence-a")
	selectedBefore := selectedGraphGenerations(t, s, "equivalence-a", "v1")
	otherBefore := selectedGraphGenerations(t, s, "equivalence-b", "v1")

	all := []operability.Component{operability.ComponentGraphIndexes, operability.ComponentFTS, operability.ComponentVector}
	if _, _, err = s.AdmitGraphRebuild(context.Background(), "equivalence-a", "v1", "all-components", operability.RequestFingerprint(all), "request", "rebuild-all", all); err != nil {
		t.Fatal(err)
	}
	task, found, err := s.ClaimOldestQueuedGraphTask(context.Background())
	if err != nil || !found {
		t.Fatalf("task=%+v found=%v err=%v", task, found, err)
	}
	if err = s.ProcessGraphRebuild(context.Background(), task, retrievalEmbedder{}); err != nil {
		t.Fatal(err)
	}
	after := fixedGraphResults(t, s, "equivalence-a")
	beforeJSON, marshalErr := json.Marshal(before)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	afterJSON, marshalErr := json.Marshal(after)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if string(beforeJSON) != string(afterJSON) {
		t.Fatalf("rebuild changed fixed results:\nbefore=%#v\nafter=%#v", before, after)
	}
	selectedAfter := selectedGraphGenerations(t, s, "equivalence-a", "v1")
	for _, component := range []string{"graph_indexes", "fts", "vector"} {
		if selectedAfter[component] == selectedBefore[component] {
			t.Fatalf("%s was not atomically replaced: before=%v after=%v", component, selectedBefore, selectedAfter)
		}
	}
	if !equalGraphGenerations(otherBefore, selectedGraphGenerations(t, s, "equivalence-b", "v1")) {
		t.Fatalf("same IDs in another namespace changed its generation selection")
	}

	indexesOnly := []operability.Component{operability.ComponentGraphIndexes}
	if _, _, err = s.AdmitGraphRebuild(context.Background(), "equivalence-a", "v1", "indexes-only", operability.RequestFingerprint(indexesOnly), "request", "rebuild-indexes", indexesOnly); err != nil {
		t.Fatal(err)
	}
	task, found, err = s.ClaimOldestQueuedGraphTask(context.Background())
	if err != nil || !found {
		t.Fatalf("task=%+v found=%v err=%v", task, found, err)
	}
	if err = s.ProcessGraphRebuild(context.Background(), task, retrievalEmbedder{}); err != nil {
		t.Fatal(err)
	}
	selectedIndexes := selectedGraphGenerations(t, s, "equivalence-a", "v1")
	if selectedIndexes["graph_indexes"] == selectedAfter["graph_indexes"] || selectedIndexes["fts"] != selectedAfter["fts"] || selectedIndexes["vector"] != selectedAfter["vector"] {
		t.Fatalf("unrequested components changed: all=%v indexes=%v", selectedAfter, selectedIndexes)
	}
}

func TestGraphRebuildPromotionKeepsConcurrentReaderOnOneGenerationSet(t *testing.T) {
	s, err := New(t.TempDir()+"/rag.db", 2)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	putReadyRetrievalSnapshot(t, s, "promotion-reader", "v1", "initial-reader")
	if _, err = s.DB().Exec(`UPDATE graph_tasks SET state='succeeded',phase='completed',progress=10000 WHERE id='initial-reader'`); err != nil {
		t.Fatal(err)
	}
	old := selectedGraphGenerations(t, s, "promotion-reader", "v1")
	components := []operability.Component{operability.ComponentGraphIndexes, operability.ComponentFTS, operability.ComponentVector}
	if _, _, err = s.AdmitGraphRebuild(context.Background(), "promotion-reader", "v1", "concurrent-reader", operability.RequestFingerprint(components), "request", "reader-rebuild", components); err != nil {
		t.Fatal(err)
	}
	task, found, err := s.ClaimOldestQueuedGraphTask(context.Background())
	if err != nil || !found {
		t.Fatalf("task=%+v found=%v err=%v", task, found, err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	readerDone := make(chan error, 1)
	go func() {
		readerDone <- s.WithRetrievalRead(context.Background(), "promotion-reader", "v1", func(view graphretrieval.ReadView) error {
			fts, ftsState := view.Generation(graphretrieval.StageBM25)
			vector, vectorState := view.Generation(graphretrieval.StageVector)
			if ftsState != graphretrieval.StageUsed || vectorState != graphretrieval.StageUsed || fts.Generation != old["fts"] || vector.Generation != old["vector"] {
				return context.Canceled
			}
			close(started)
			<-release
			if _, state, err := view.BM25(context.Background(), "alpha", nil, 10); err != nil || state != graphretrieval.StageUsed {
				return context.Canceled
			}
			if _, state, err := view.Vector(context.Background(), []float32{1, 0}, nil, 10); err != nil || state != graphretrieval.StageUsed {
				return context.Canceled
			}
			return nil
		})
	}()
	<-started
	if err = s.ProcessGraphRebuild(context.Background(), task, retrievalEmbedder{}); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err = <-readerDone; err != nil {
		t.Fatalf("reader observed a mixed generation set: %v", err)
	}
	current := selectedGraphGenerations(t, s, "promotion-reader", "v1")
	for _, component := range []string{"graph_indexes", "fts", "vector"} {
		if current[component] == old[component] {
			t.Fatalf("%s was not promoted: old=%v current=%v", component, old, current)
		}
	}
}

type fixedResults struct {
	traverse graphquery.TraverseResult
	paths    graphquery.PathsResult
	retrieve graphretrieval.RetrievalResult
}

func fixedGraphResults(t *testing.T, s *Store, namespace string) fixedResults {
	t.Helper()
	query := graphquery.Service{Repository: s}
	traverse, graphErr := query.Traverse(context.Background(), namespace, graphquery.TraverseRequest{SnapshotVersion: "v1", StartNodeIDs: []string{"node-a"}, Direction: graphquery.DirectionOutgoing})
	if graphErr != nil {
		t.Fatal(graphErr)
	}
	paths, graphErr := query.Paths(context.Background(), namespace, graphquery.PathsRequest{SnapshotVersion: "v1", SourceNodeIDs: []string{"node-a"}, TargetNodeIDs: []string{"node-b"}, Direction: graphquery.DirectionOutgoing})
	if graphErr != nil {
		t.Fatal(graphErr)
	}
	retrieval := graphretrieval.Service{
		Repository: s,
		Embedder:   graphretrieval.EmbeddingAdapter{Provider: retrievalEmbedder{}},
	}
	result, graphErr := retrieval.Retrieve(context.Background(), namespace, graphretrieval.Request{SnapshotVersion: "v1", Query: "alpha"})
	if graphErr != nil {
		t.Fatal(graphErr)
	}
	// IDs and derived digests intentionally identify each fresh generation.
	result.FTSGeneration, result.VectorGeneration = nil, nil
	return fixedResults{traverse: traverse, paths: paths, retrieve: result}
}
