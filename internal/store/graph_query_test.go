package store

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Wrath-y/local-rag/internal/graphquery"
)

func TestGraphQueryMigrationIndexesAndQueryPlan(t *testing.T) {
	s, err := New(t.TempDir()+"/rag.db", 4)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, index := range []string{"graph_edges_query_outgoing_idx", "graph_edges_query_incoming_idx", "graph_nodes_query_type_idx"} {
		var sqlText string
		if err := s.DB().QueryRow(`SELECT sql FROM sqlite_master WHERE type='index' AND name=?`, index).Scan(&sqlText); err != nil {
			t.Fatalf("index %s: %v", index, err)
		}
		if !strings.Contains(sqlText, "namespace,version") {
			t.Fatalf("unscoped index %s: %s", index, sqlText)
		}
	}
	seedGraphSnapshot(t, s, "query", "v1", "query")
	assertQueryPlanUses(t, s, `SELECT edge_id FROM graph_edges WHERE namespace=? AND version=? AND from_node_id=? AND relation_kind=? AND edge_type=?`, "graph_edges_query_outgoing_idx", "query", "v1", "node-a", "explicit", "kind")
	assertQueryPlanUses(t, s, `SELECT node_id FROM graph_nodes WHERE namespace=? AND version=? AND node_type=?`, "graph_nodes_query_type_idx", "query", "v1", "kind")
}

func TestGraphReadViewCapturesOneReadySnapshotAndScopesEveryRead(t *testing.T) {
	s, err := New(t.TempDir()+"/rag.db", 4)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	seedGraphSnapshot(t, s, "same", "v1", "one")
	seedGraphSnapshot(t, s, "other", "v1", "two")
	if _, err := s.DB().Exec(`UPDATE graph_snapshots SET status='ready',query_ready=1 WHERE namespace='same' AND version='v1'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`UPDATE graph_snapshots SET status='ready',query_ready=1 WHERE namespace='other' AND version='v1'`); err != nil {
		t.Fatal(err)
	}
	if err := s.WithRead(context.Background(), "same", "v1", func(view graphquery.ReadView) error {
		if view.Version() != "v1" || view.ContentHash() == "" {
			t.Fatalf("metadata: %q %q", view.Version(), view.ContentHash())
		}
		nodes, err := view.Nodes(context.Background(), []string{"node-a", "node-z"})
		if err != nil {
			return err
		}
		if len(nodes) != 2 || !strings.Contains(nodes[0].Label+nodes[1].Label, "one") {
			t.Fatalf("scoped nodes=%#v", nodes)
		}
		edges, err := view.Adjacency(context.Background(), []string{"node-a", "node-z"}, graphquery.DirectionBoth)
		if err != nil {
			return err
		}
		if len(edges) != 1 || edges[0].ID != "edge-shared" {
			t.Fatalf("scoped edges=%#v", edges)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestGraphReadViewReturnsStableResolutionErrors(t *testing.T) {
	s, err := New(t.TempDir()+"/rag.db", 4)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.WithRead(context.Background(), "missing", "", func(graphquery.ReadView) error { return nil }); !errors.Is(err, graphquery.ErrNoActiveSnapshot) {
		t.Fatalf("no active err=%v", err)
	}
	seedGraphSnapshot(t, s, "query", "building", "query")
	if err := s.WithRead(context.Background(), "query", "building", func(graphquery.ReadView) error { return nil }); !errors.Is(err, graphquery.ErrSnapshotNotReady) {
		t.Fatalf("not ready err=%v", err)
	}
	if err := s.WithRead(context.Background(), "query", "missing", func(graphquery.ReadView) error { return nil }); !errors.Is(err, graphquery.ErrSnapshotNotFound) {
		t.Fatalf("missing err=%v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.WithRead(context.Background(), "query", "building", func(graphquery.ReadView) error { return nil }); err == nil || !errors.Is(err, graphquery.ErrStoreUnavailable) {
		t.Fatalf("closed err=%v", err)
	}
}

func TestGraphReadViewHonorsCancelledContextBeforeAnyRead(t *testing.T) {
	s, err := New(t.TempDir()+"/rag.db", 4)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.WithRead(ctx, "project", "v1", func(graphquery.ReadView) error { t.Fatal("callback must not run"); return nil }); !errors.Is(err, graphquery.ErrStoreUnavailable) {
		t.Fatalf("cancelled read err=%v", err)
	}
}

func TestGraphTwoMigrationFailureLeavesLifecycleAndLegacyStorageUsable(t *testing.T) {
	path := t.TempDir() + "/rag.db"
	migrations := append([]graphMigration{}, graphMigrations[:1]...)
	migrations = append(migrations, graphMigration{component: "graph", version: 2, sql: `CREATE TABLE graph_query_partial (id INTEGER); invalid sql`})
	s, err := newWithGraphMigrations(path, 4, migrations)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.GraphUnavailable(); !errors.Is(err, ErrGraphUnavailable) {
		t.Fatalf("graph diagnostic=%v", err)
	}
	if _, err := s.InsertChunk("legacy", "fixture", "legacy", "", "", []float32{1, 0, 0, 0}); err != nil {
		t.Fatalf("legacy write=%v", err)
	}
	if err := s.WithRead(context.Background(), "query", "v1", func(graphquery.ReadView) error { return nil }); !errors.Is(err, graphquery.ErrStoreUnavailable) {
		t.Fatalf("query unavailable err=%v", err)
	}
	var partial int
	if err := s.DB().QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='graph_query_partial'`).Scan(&partial); err != nil || partial != 0 {
		t.Fatalf("partial migration table=%d err=%v", partial, err)
	}
}

func TestGraphReadViewRemainsOnCapturedActiveSnapshotDuringActivation(t *testing.T) {
	s, err := New(t.TempDir()+"/rag.db", 4)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	seedGraphSnapshot(t, s, "project", "v1", "first")
	seedGraphSnapshot(t, s, "project", "v2", "second")
	if _, err := s.DB().Exec(`UPDATE graph_snapshots SET status='ready',query_ready=1 WHERE namespace='project'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`INSERT INTO graph_namespace_heads(namespace,active_version) VALUES('project','v1')`); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- s.WithRead(context.Background(), "project", "", func(view graphquery.ReadView) error {
			if view.Version() != "v1" {
				t.Errorf("captured version=%q", view.Version())
			}
			close(started)
			<-release
			nodes, err := view.Nodes(context.Background(), []string{"node-a"})
			if err != nil {
				return err
			}
			if len(nodes) != 1 || !strings.Contains(nodes[0].Label, "first") {
				t.Errorf("mixed read=%#v", nodes)
			}
			return nil
		})
	}()
	<-started
	if _, err := s.ActivateGraphSnapshot(context.Background(), "project", "v2"); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
