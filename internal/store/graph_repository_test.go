package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Wrath-y/local-rag/internal/graphsnapshot"
)

func TestReadGraphSnapshotScopesEveryReadByNamespaceAndVersion(t *testing.T) {
	s, err := New(t.TempDir()+"/rag.db", 4)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	seedGraphSnapshot(t, s, "namespace-a", "version-1", "a-v1")
	seedGraphSnapshot(t, s, "namespace-a", "version-2", "a-v2")
	seedGraphSnapshot(t, s, "namespace-b", "version-1", "b-v1")

	record, err := s.ReadGraphSnapshot(context.Background(), "namespace-a", "version-1")
	if err != nil {
		t.Fatal(err)
	}
	if record.Namespace != "namespace-a" || record.Version != "version-1" || record.NodeCount != 2 || record.EdgeCount != 1 {
		t.Fatalf("record identity/counts = %#v", record)
	}
	if len(record.Nodes) != 2 || record.Nodes[0].ID != "node-a" || record.Nodes[0].Label != "a-v1 first" || record.Nodes[1].ID != "node-z" || record.Nodes[1].Label != "a-v1 second" {
		t.Fatalf("scoped nodes = %#v", record.Nodes)
	}
	if len(record.Edges) != 1 || record.Edges[0].ID != "edge-shared" || record.Edges[0].From != "node-a" || record.Edges[0].To != "node-z" {
		t.Fatalf("scoped edges = %#v", record.Edges)
	}
	if strings.Contains(record.Nodes[0].Label, "a-v2") || strings.Contains(record.Nodes[0].Label, "b-v1") {
		t.Fatalf("record leaked another namespace or version: %#v", record.Nodes)
	}
	if _, err := s.ReadGraphSnapshot(context.Background(), "namespace-a", "missing"); !errors.Is(err, ErrGraphSnapshotNotFound) {
		t.Fatalf("missing graph error=%v", err)
	}
	if _, err := s.ReadGraphSnapshot(context.Background(), "", "version-1"); !errors.Is(err, ErrInvalidGraphIdentity) {
		t.Fatalf("invalid identity error=%v", err)
	}
}

func TestLookupGraphSnapshotReturnsScopedReplayResource(t *testing.T) {
	s, err := New(t.TempDir()+"/rag.db", 4)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	seedGraphSnapshot(t, s, "namespace", "version", "marker")
	if _, err := s.DB().Exec(`INSERT INTO graph_snapshot_components(namespace,version,component,state,generation,warning) VALUES(?,?,?,?,?,?)`, "namespace", "version", "graph", "pending", nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`INSERT INTO graph_snapshot_components(namespace,version,component,state,generation,warning) VALUES(?,?,?,?,?,?)`, "namespace", "version", "fts", "pending", nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`INSERT INTO graph_snapshot_components(namespace,version,component,state,generation,warning) VALUES(?,?,?,?,?,?)`, "namespace", "version", "vector", "unavailable", nil, "vector is not configured"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`UPDATE graph_snapshot_components SET error_json=? WHERE namespace=? AND version=? AND component='fts'`, `{"code":"INTERNAL_ERROR","message":"Graph lifecycle operation failed","retryable":false,"details":{}}`, "namespace", "version"); err != nil {
		t.Fatal(err)
	}
	snapshot, found, err := s.LookupGraphSnapshot(context.Background(), "namespace", "version")
	if err != nil || !found || snapshot.NodeCount != 2 || snapshot.EdgeCount != 1 || snapshot.ContentHash == "" || snapshot.TaskID != "task-namespace-version" || len(snapshot.Components) != 3 || snapshot.Components[0].Name != "graph" || snapshot.Components[0].Generation != "" || snapshot.Components[1].Error == nil || len(snapshot.Warnings) != 1 {
		t.Fatalf("snapshot=%#v found=%v err=%v", snapshot, found, err)
	}
	if _, found, err := s.LookupGraphSnapshot(context.Background(), "namespace", "missing"); err != nil || found {
		t.Fatalf("missing found=%v err=%v", found, err)
	}
}

func TestConcurrentSameVersionSubmissionKeepsOneSnapshotAndTask(t *testing.T) {
	s, err := New(t.TempDir()+"/rag.db", 4)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	node := graphsnapshot.Node{ID: "node", Type: "kind", Label: "label", Text: "text", Properties: []byte(`{}`), Provenance: []byte(`{}`)}
	_, hash, err := graphsnapshot.CanonicalHash([]graphsnapshot.Node{node}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var taskSequence int64
	service := graphsnapshot.NewService(s, func() (string, error) { return fmt.Sprintf("task-%d", atomic.AddInt64(&taskSequence, 1)), nil })
	request := graphsnapshot.Request{SchemaVersion: graphsnapshot.SchemaVersionV1, Mode: graphsnapshot.ModeFull, ContentHash: hash, Nodes: []graphsnapshot.Node{node}}
	start := make(chan struct{})
	results := make(chan graphsnapshot.SubmissionCheck, 2)
	errors := make(chan error, 2)
	var group sync.WaitGroup
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			result, graphErr := service.Put(context.Background(), "project", "revision", request)
			if graphErr != nil {
				errors <- graphErr
				return
			}
			results <- result
		}()
	}
	close(start)
	group.Wait()
	close(results)
	close(errors)
	for graphErr := range errors {
		t.Fatalf("concurrent submission error: %v", graphErr)
	}
	var taskIDs []string
	for result := range results {
		taskIDs = append(taskIDs, result.Snapshot.TaskID)
	}
	if len(taskIDs) != 2 || taskIDs[0] != taskIDs[1] {
		t.Fatalf("task IDs=%v, want one original task", taskIDs)
	}
	var snapshots, tasks int
	if err := s.DB().QueryRow(`SELECT count(*) FROM graph_snapshots WHERE namespace='project' AND version='revision'`).Scan(&snapshots); err != nil || snapshots != 1 {
		t.Fatalf("snapshots=%d err=%v", snapshots, err)
	}
	if err := s.DB().QueryRow(`SELECT count(*) FROM graph_tasks WHERE namespace='project' AND version='revision'`).Scan(&tasks); err != nil || tasks != 1 {
		t.Fatalf("tasks=%d err=%v", tasks, err)
	}
}

func TestSnapshotAcceptanceKeepsBasesImmutableAndDeltaEquivalentToFull(t *testing.T) {
	s, err := New(t.TempDir()+"/rag.db", 4)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	seedGraphSnapshot(t, s, "project", "base", "base")
	if _, err := s.DB().Exec(`UPDATE graph_snapshots SET status='ready',query_ready=1 WHERE namespace='project' AND version='base'`); err != nil {
		t.Fatal(err)
	}
	base, found, err := s.ReadGraphSnapshotBase(context.Background(), "project", "base")
	if err != nil || !found || base.Status != graphsnapshot.SnapshotReady {
		t.Fatalf("base=%#v found=%v err=%v", base, found, err)
	}
	extra := graphsnapshot.Node{ID: "node-extra", Type: "kind", Label: "extra", Text: "extra", Properties: []byte(`{}`), Provenance: []byte(`{}`)}
	finalNodes := append(append([]graphsnapshot.Node(nil), base.Manifest.Nodes...), extra)
	_, hash, err := graphsnapshot.CanonicalHash(finalNodes, base.Manifest.Edges)
	if err != nil {
		t.Fatal(err)
	}
	var sequence int64
	service := graphsnapshot.NewService(s, func() (string, error) { return fmt.Sprintf("task-equivalence-%d", atomic.AddInt64(&sequence, 1)), nil })
	if _, graphErr := service.Put(context.Background(), "project", "delta", graphsnapshot.Request{SchemaVersion: graphsnapshot.SchemaVersionV1, Mode: graphsnapshot.ModeDelta, BaseVersion: "base", ContentHash: hash, NodeUpserts: []graphsnapshot.Node{extra}}); graphErr != nil {
		t.Fatalf("delta acceptance error=%#v", graphErr)
	}
	if _, graphErr := service.Put(context.Background(), "project", "full", graphsnapshot.Request{SchemaVersion: graphsnapshot.SchemaVersionV1, Mode: graphsnapshot.ModeFull, ContentHash: hash, Nodes: finalNodes, Edges: base.Manifest.Edges}); graphErr != nil {
		t.Fatalf("full acceptance error=%#v", graphErr)
	}
	delta, deltaFound, err := s.LookupGraphSnapshot(context.Background(), "project", "delta")
	if err != nil || !deltaFound || delta.ContentHash != hash || delta.NodeCount != len(finalNodes) {
		t.Fatalf("delta=%#v found=%v err=%v", delta, deltaFound, err)
	}
	full, fullFound, err := s.LookupGraphSnapshot(context.Background(), "project", "full")
	if err != nil || !fullFound || full.ContentHash != delta.ContentHash || full.NodeCount != delta.NodeCount || full.EdgeCount != delta.EdgeCount {
		t.Fatalf("full=%#v found=%v err=%v", full, fullFound, err)
	}
	baseAfter, err := s.ReadGraphSnapshot(context.Background(), "project", "base")
	if err != nil || len(baseAfter.Nodes) != len(base.Manifest.Nodes) {
		t.Fatalf("base mutated=%#v err=%v", baseAfter, err)
	}
	if _, graphErr := service.Put(context.Background(), "missing-project", "target", graphsnapshot.Request{SchemaVersion: graphsnapshot.SchemaVersionV1, Mode: graphsnapshot.ModeDelta, BaseVersion: "missing", ContentHash: hash}); graphErr == nil || graphErr.Code != graphsnapshot.CodeBaseSnapshotNotFound {
		t.Fatalf("missing base error=%#v", graphErr)
	}
	var namespaces int
	if err := s.DB().QueryRow(`SELECT count(*) FROM graph_namespaces WHERE namespace='missing-project'`).Scan(&namespaces); err != nil || namespaces != 0 {
		t.Fatalf("missing-base namespace count=%d err=%v", namespaces, err)
	}
}

func seedGraphSnapshot(t *testing.T, s *Store, namespace, version, marker string) {
	t.Helper()
	const timestamp = "2026-08-10T00:00:00Z"
	if _, err := s.DB().Exec(`INSERT OR IGNORE INTO graph_namespaces(namespace,created_at) VALUES(?,?)`, namespace, timestamp); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`INSERT INTO graph_snapshots(namespace,version,schema_version,content_hash,node_count,edge_count,task_id,status,query_ready,created_at,updated_at) VALUES(?,?, '1.0', ?,2,1,?,'building',0,?,?)`, namespace, version, strings.Repeat("a", 63)+"1", "task-"+namespace+"-"+version, timestamp, timestamp); err != nil {
		t.Fatal(err)
	}
	for _, node := range []struct{ id, label string }{{"node-z", marker + " second"}, {"node-a", marker + " first"}} {
		if _, err := s.DB().Exec(`INSERT INTO graph_nodes(namespace,version,node_id,node_type,label,text,properties_json,provenance_json) VALUES(?,?,?,?,?,?,?,?)`, namespace, version, node.id, "kind", node.label, node.label, `{}`, `{}`); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.DB().Exec(`INSERT INTO graph_edges(namespace,version,edge_id,from_node_id,to_node_id,edge_type,relation_kind,confidence,properties_json,provenance_json) VALUES(?,?,?,?,?,?,?,?,?,?)`, namespace, version, "edge-shared", "node-a", "node-z", "kind", "explicit", "1", `{}`, `{}`); err != nil {
		t.Fatal(err)
	}
}
