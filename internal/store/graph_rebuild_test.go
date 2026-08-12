package store

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Wrath-y/local-rag/internal/graphsnapshot"
	"github.com/Wrath-y/local-rag/internal/operability"
)

func TestAdmitGraphRebuildReplaysAcrossStatesAndSerializesSameKey(t *testing.T) {
	s, err := New(t.TempDir()+"/rag.db", 4)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	seedTrustedRebuildSnapshot(t, s, "admission", "v1")
	components := []operability.Component{operability.ComponentVector, operability.ComponentFTS}
	fingerprint := operability.RequestFingerprint(components)

	first, replayed, err := s.AdmitGraphRebuild(context.Background(), "admission", "v1", "same-key", fingerprint, "submit-id", "task-original", components)
	if err != nil || replayed || first.ID != "task-original" || first.State != graphsnapshot.TaskQueued {
		t.Fatalf("first=%+v replayed=%v err=%v", first, replayed, err)
	}
	for _, state := range []graphsnapshot.TaskState{graphsnapshot.TaskQueued, graphsnapshot.TaskRunning, graphsnapshot.TaskSucceeded} {
		if state != graphsnapshot.TaskQueued {
			if _, err = s.DB().Exec(`UPDATE graph_tasks SET state=?,phase=CASE WHEN ?='succeeded' THEN 'completed' ELSE 'building_fts' END WHERE id='task-original'`, state, state); err != nil {
				t.Fatal(err)
			}
		}
		replay, wasReplayed, replayErr := s.AdmitGraphRebuild(context.Background(), "admission", "v1", "same-key", fingerprint, "another-request", "ignored-"+string(state), []operability.Component{operability.ComponentFTS, operability.ComponentVector})
		if replayErr != nil || !wasReplayed || replay.ID != first.ID || replay.State != state {
			t.Fatalf("state=%s replay=%+v replayed=%v err=%v", state, replay, wasReplayed, replayErr)
		}
	}

	const callers = 12
	var group sync.WaitGroup
	errs := make(chan error, callers)
	ids := make(chan string, callers)
	for caller := 0; caller < callers; caller++ {
		group.Add(1)
		go func(caller int) {
			defer group.Done()
			task, wasReplayed, admitErr := s.AdmitGraphRebuild(context.Background(), "admission", "v1", "concurrent-key", fingerprint, "request", fmt.Sprintf("candidate-%d", caller), components)
			if admitErr != nil {
				errs <- admitErr
				return
			}
			if task.ID == "" {
				errs <- fmt.Errorf("unexpected concurrent admission task=%+v replayed=%v", task, wasReplayed)
				return
			}
			ids <- task.ID
		}(caller)
	}
	group.Wait()
	close(errs)
	close(ids)
	for admitErr := range errs {
		t.Fatal(admitErr)
	}
	seen := map[string]bool{}
	for id := range ids {
		seen[id] = true
	}
	if len(seen) != 1 {
		t.Fatalf("concurrent admission created task IDs %v", seen)
	}
	var tasks, steps, generations int
	if err = s.DB().QueryRow(`SELECT count(*) FROM graph_tasks WHERE namespace='admission' AND version='v1' AND operation='snapshot_rebuild'`).Scan(&tasks); err != nil || tasks != 2 {
		t.Fatalf("tasks=%d err=%v", tasks, err)
	}
	if err = s.DB().QueryRow(`SELECT count(*) FROM graph_task_steps WHERE task_id=(SELECT task_id FROM graph_rebuild_idempotency WHERE namespace='admission' AND version='v1' AND idempotency_key='concurrent-key')`).Scan(&steps); err != nil || steps != 2 {
		t.Fatalf("steps=%d err=%v", steps, err)
	}
	if err = s.DB().QueryRow(`SELECT count(*) FROM graph_retrieval_generations WHERE namespace='admission' AND version='v1' AND selected=0`).Scan(&generations); err != nil || generations != 0 {
		t.Fatalf("unaccepted generations=%d err=%v", generations, err)
	}
}

func TestTrustedRebuildSourceRejectionsDoNotCreateDurableWork(t *testing.T) {
	t.Run("missing snapshot", func(t *testing.T) {
		s, err := New(t.TempDir()+"/rag.db", 4)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		components := []operability.Component{operability.ComponentFTS}
		if _, _, err = s.AdmitGraphRebuild(context.Background(), "missing", "v1", "key", operability.RequestFingerprint(components), "request", "task", components); !errors.Is(err, operability.ErrSnapshotNotFound) {
			t.Fatalf("admission err=%v", err)
		}
		var tasks, generations int
		if err = s.DB().QueryRow(`SELECT count(*) FROM graph_tasks WHERE operation='snapshot_rebuild'`).Scan(&tasks); err != nil || tasks != 0 {
			t.Fatalf("tasks=%d err=%v", tasks, err)
		}
		if err = s.DB().QueryRow(`SELECT count(*) FROM graph_retrieval_generations`).Scan(&generations); err != nil || generations != 0 {
			t.Fatalf("generations=%d err=%v", generations, err)
		}
	})

	tests := []struct {
		name   string
		mutate func(*testing.T, *Store)
	}{
		{
			name: "deleted source node",
			mutate: func(t *testing.T, s *Store) {
				t.Helper()
				if _, err := s.DB().Exec(`DELETE FROM graph_nodes WHERE namespace='source-check' AND version='v1' AND node_id='node'`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "dangling edge endpoint",
			mutate: func(t *testing.T, s *Store) {
				t.Helper()
				conn, err := s.DB().Conn(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				if _, err = conn.ExecContext(context.Background(), `PRAGMA foreign_keys=OFF`); err != nil {
					t.Fatal(err)
				}
				if _, err = conn.ExecContext(context.Background(), `DELETE FROM graph_edges WHERE namespace='source-check' AND version='v1' AND edge_id='edge'`); err != nil {
					t.Fatal(err)
				}
				if _, err = conn.ExecContext(context.Background(), `INSERT INTO graph_edges(namespace,version,edge_id,from_node_id,to_node_id,edge_type,relation_kind,confidence,properties_json,provenance_json) VALUES('source-check','v1','edge','node','missing-node','kind','explicit','1','{}','{}')`); err != nil {
					t.Fatal(err)
				}
				if _, err = conn.ExecContext(context.Background(), `PRAGMA foreign_keys=ON`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "canonical hash mismatch",
			mutate: func(t *testing.T, s *Store) {
				t.Helper()
				if _, err := s.DB().Exec(`DELETE FROM graph_nodes WHERE namespace='source-check' AND version='v1' AND node_id='node'`); err != nil {
					t.Fatal(err)
				}
				if _, err := s.DB().Exec(`INSERT INTO graph_nodes(namespace,version,node_id,node_type,label,text,properties_json,provenance_json) VALUES('source-check','v1','node','kind','tampered','text','{}','{}')`); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			s, err := New(t.TempDir()+"/rag.db", 4)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			seedTrustedRebuildSnapshot(t, s, "source-check", "v1")
			testCase.mutate(t, s)
			if _, err = s.ReadTrustedGraphRebuildSource(context.Background(), "source-check", "v1"); !errors.Is(err, operability.ErrReimportRequired) {
				t.Fatalf("read err=%v", err)
			}
			var tasks, generations int
			if err = s.DB().QueryRow(`SELECT count(*) FROM graph_tasks WHERE operation='snapshot_rebuild'`).Scan(&tasks); err != nil || tasks != 0 {
				t.Fatalf("tasks=%d err=%v", tasks, err)
			}
			if err = s.DB().QueryRow(`SELECT count(*) FROM graph_retrieval_generations WHERE namespace='source-check' AND version='v1' AND selected=0`).Scan(&generations); err != nil || generations != 0 {
				t.Fatalf("generations=%d err=%v", generations, err)
			}
		})
	}
}

func seedTrustedRebuildSnapshot(t *testing.T, s *Store, namespace, version string) {
	t.Helper()
	node := graphsnapshot.Node{ID: "node", Type: "kind", Label: "label", Text: "text", Properties: []byte(`{}`), Provenance: []byte(`{}`)}
	edge := graphsnapshot.Edge{ID: "edge", From: "node", To: "node", Type: "kind", RelationKind: "explicit", Confidence: "1", Properties: []byte(`{}`), Provenance: []byte(`{}`)}
	_, hash, err := graphsnapshot.CanonicalHash([]graphsnapshot.Node{node}, []graphsnapshot.Edge{edge})
	if err != nil {
		t.Fatal(err)
	}
	service := graphsnapshot.NewService(s, func() (string, error) { return "initial-task", nil })
	if _, graphErr := service.Put(context.Background(), namespace, version, graphsnapshot.Request{SchemaVersion: graphsnapshot.SchemaVersionV1, Mode: graphsnapshot.ModeFull, ContentHash: hash, Nodes: []graphsnapshot.Node{node}, Edges: []graphsnapshot.Edge{edge}}); graphErr != nil {
		t.Fatal(graphErr)
	}
	if err = s.PromoteGraphComponent(context.Background(), "initial-task"); err != nil {
		t.Fatal(err)
	}
}

func TestAdmitGraphRebuildIsIdempotentAndDoesNotWriteForUntrustedSource(t *testing.T) {
	s, err := New(t.TempDir()+"/rag.db", 4)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	seedGraphSnapshot(t, s, "rebuild", "v1", "source")
	if _, err = s.DB().Exec(`INSERT INTO graph_snapshot_components(namespace,version,component,state) VALUES('rebuild','v1','graph','ready')`); err != nil {
		t.Fatal(err)
	}
	components := []operability.Component{operability.ComponentFTS, operability.ComponentVector}
	fingerprint := operability.RequestFingerprint(components)
	task, replayed, err := s.AdmitGraphRebuild(context.Background(), "rebuild", "v1", "key-1", fingerprint, "req-1", "rebuild-task", components)
	if err != nil || replayed || task.ID != "rebuild-task" || task.State != "queued" {
		t.Fatalf("task=%+v replayed=%v err=%v", task, replayed, err)
	}
	if _, replayed, err = s.AdmitGraphRebuild(context.Background(), "rebuild", "v1", "key-1", fingerprint, "req-1", "ignored", components); err != nil || !replayed {
		t.Fatalf("exact replay=%v err=%v", replayed, err)
	}
	if _, _, err = s.AdmitGraphRebuild(context.Background(), "rebuild", "v1", "key-1", operability.RequestFingerprint([]operability.Component{operability.ComponentFTS}), "req-1", "conflict", []operability.Component{operability.ComponentFTS}); !errors.Is(err, operability.ErrIdempotencyConflict) {
		t.Fatalf("conflict err=%v", err)
	}
	var steps, tasks int
	if err = s.DB().QueryRow(`SELECT count(*) FROM graph_task_steps WHERE task_id='rebuild-task'`).Scan(&steps); err != nil || steps != 2 {
		t.Fatalf("steps=%d err=%v", steps, err)
	}
	if _, err = s.DB().Exec(`DELETE FROM graph_edges WHERE namespace='rebuild' AND version='v1' AND edge_id='edge-shared'`); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.AdmitGraphRebuild(context.Background(), "rebuild", "v1", "key-2", fingerprint, "req-2", "missing-source", components); !errors.Is(err, operability.ErrReimportRequired) {
		t.Fatalf("untrusted source err=%v", err)
	}
	if err = s.DB().QueryRow(`SELECT count(*) FROM graph_tasks WHERE operation='snapshot_rebuild'`).Scan(&tasks); err != nil || tasks != 1 {
		t.Fatalf("tasks=%d err=%v", tasks, err)
	}
}

func TestReadTrustedGraphRebuildSourceChecksScopedImmutableHash(t *testing.T) {
	s, err := New(t.TempDir()+"/rag.db", 4)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	seedGraphSnapshot(t, s, "trusted", "v1", "first")
	if _, err = s.ReadTrustedGraphRebuildSource(context.Background(), "trusted", "v1"); !errors.Is(err, operability.ErrReimportRequired) {
		t.Fatalf("fixture hash err=%v", err)
	}
	if _, err = s.ReadTrustedGraphRebuildSource(context.Background(), "other", "v1"); !errors.Is(err, operability.ErrSnapshotNotFound) {
		t.Fatalf("missing err=%v", err)
	}
}

func TestReadTrustedGraphRebuildSourceAcceptsCanonicalSnapshot(t *testing.T) {
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
	service := graphsnapshot.NewService(s, func() (string, error) { return "trusted-task", nil })
	if _, graphErr := service.Put(context.Background(), "trusted", "v1", graphsnapshot.Request{SchemaVersion: graphsnapshot.SchemaVersionV1, Mode: graphsnapshot.ModeFull, ContentHash: hash, Nodes: []graphsnapshot.Node{node}}); graphErr != nil {
		t.Fatal(graphErr)
	}
	if err = s.PromoteGraphComponent(context.Background(), "trusted-task"); err != nil {
		t.Fatal(err)
	}
	record, err := s.ReadTrustedGraphRebuildSource(context.Background(), "trusted", "v1")
	if err != nil || record.ContentHash != hash || len(record.Nodes) != 1 {
		t.Fatalf("record=%+v err=%v", record, err)
	}
}

func TestBuildPrivateGraphIndexesLeavesSelectedGenerationUntouched(t *testing.T) {
	s, err := New(t.TempDir()+"/rag.db", 4)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	node := graphsnapshot.Node{ID: "node", Type: "kind", Label: "label", Text: "text", Properties: []byte(`{}`), Provenance: []byte(`{}`)}
	edge := graphsnapshot.Edge{ID: "edge", From: "node", To: "node", Type: "kind", RelationKind: "explicit", Confidence: "1", Properties: []byte(`{}`), Provenance: []byte(`{}`)}
	_, hash, err := graphsnapshot.CanonicalHash([]graphsnapshot.Node{node}, []graphsnapshot.Edge{edge})
	if err != nil {
		t.Fatal(err)
	}
	service := graphsnapshot.NewService(s, func() (string, error) { return "source-task", nil })
	if _, graphErr := service.Put(context.Background(), "rebuild-index", "v1", graphsnapshot.Request{SchemaVersion: graphsnapshot.SchemaVersionV1, Mode: graphsnapshot.ModeFull, ContentHash: hash, Nodes: []graphsnapshot.Node{node}, Edges: []graphsnapshot.Edge{edge}}); graphErr != nil {
		t.Fatal(graphErr)
	}
	if err = s.PromoteGraphComponent(context.Background(), "source-task"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB().Exec(`INSERT INTO graph_task_steps(task_id,component,state,updated_at) VALUES('rebuild-task','graph_indexes','pending','2026-08-11T00:00:00Z')`); err == nil {
		t.Fatal("step without task accepted")
	}
	components := []operability.Component{operability.ComponentGraphIndexes}
	if _, _, err = s.AdmitGraphRebuild(context.Background(), "rebuild-index", "v1", "indexes", operability.RequestFingerprint(components), "req", "rebuild-task", components); err != nil {
		t.Fatal(err)
	}
	generation, err := s.BuildPrivateGraphIndexes(context.Background(), "rebuild-task")
	if err != nil || generation != "graph-indexes-rebuild-task" {
		t.Fatalf("generation=%q err=%v", generation, err)
	}
	var selected, private, adjacency int
	if err = s.DB().QueryRow(`SELECT count(*) FROM graph_retrieval_generations WHERE namespace='rebuild-index' AND version='v1' AND component='graph_indexes' AND selected=1`).Scan(&selected); err != nil || selected != 1 {
		t.Fatalf("selected=%d err=%v", selected, err)
	}
	if err = s.DB().QueryRow(`SELECT count(*) FROM graph_retrieval_generations WHERE generation=? AND state='private'`, generation).Scan(&private); err != nil || private != 1 {
		t.Fatalf("private=%d err=%v", private, err)
	}
	if err = s.DB().QueryRow(`SELECT count(*) FROM graph_index_adjacency WHERE generation=?`, generation).Scan(&adjacency); err != nil || adjacency != 2 {
		t.Fatalf("adjacency=%d err=%v", adjacency, err)
	}
	if _, err = s.DB().Exec(`UPDATE graph_tasks SET state='running',phase='building_graph_indexes' WHERE id='rebuild-task'`); err != nil {
		t.Fatal(err)
	}
	if err = s.RecoverGraphTasks(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = s.DB().QueryRow(`SELECT count(*) FROM graph_retrieval_generations WHERE generation=?`, generation).Scan(&private); err != nil || private != 0 {
		t.Fatalf("recovered private generation=%d err=%v", private, err)
	}
	var state string
	if err = s.DB().QueryRow(`SELECT state FROM graph_tasks WHERE id='rebuild-task'`).Scan(&state); err != nil || state != "queued" {
		t.Fatalf("recovered task state=%q err=%v", state, err)
	}
}

func TestPromotionRejectsCorruptValidatedPrivateGeneration(t *testing.T) {
	s, err := New(t.TempDir()+"/rag.db", 4)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	seedTrustedRebuildSnapshot(t, s, "validate-private", "v1")
	if _, err = s.DB().Exec(`UPDATE graph_tasks SET state='succeeded',phase='completed',progress=10000 WHERE id='initial-task'`); err != nil {
		t.Fatal(err)
	}
	components := []operability.Component{operability.ComponentGraphIndexes}
	if _, _, err = s.AdmitGraphRebuild(context.Background(), "validate-private", "v1", "validate", operability.RequestFingerprint(components), "request", "validate-task", components); err != nil {
		t.Fatal(err)
	}
	task, found, err := s.ClaimOldestQueuedGraphTask(context.Background())
	if err != nil || !found {
		t.Fatalf("task=%+v found=%v err=%v", task, found, err)
	}
	generation, err := s.BuildPrivateGraphIndexes(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB().Exec(`DELETE FROM graph_index_adjacency WHERE namespace='validate-private' AND version='v1' AND generation=? AND direction='outgoing'`, generation); err != nil {
		t.Fatal(err)
	}
	if err = s.promoteGraphIndexRebuild(context.Background(), task.ID); err == nil {
		t.Fatal("corrupt validated generation promoted")
	}
	var selectedPrivate, taskState int
	if err = s.DB().QueryRow(`SELECT count(*) FROM graph_retrieval_generations WHERE namespace='validate-private' AND version='v1' AND component='graph_indexes' AND generation=? AND selected=1`, generation).Scan(&selectedPrivate); err != nil || selectedPrivate != 0 {
		t.Fatalf("selected private=%d err=%v", selectedPrivate, err)
	}
	if err = s.DB().QueryRow(`SELECT count(*) FROM graph_tasks WHERE id=? AND state='running'`, task.ID).Scan(&taskState); err != nil || taskState != 1 {
		t.Fatalf("task state rows=%d err=%v", taskState, err)
	}
}

func TestFailedRebuildTaskRetainsSubmissionRequestIDInError(t *testing.T) {
	s, err := New(t.TempDir()+"/rag.db", 4)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	seedTrustedRebuildSnapshot(t, s, "request-correlation", "v1")
	if _, err = s.DB().Exec(`UPDATE graph_tasks SET state='succeeded',phase='completed',progress=10000 WHERE id='initial-task'`); err != nil {
		t.Fatal(err)
	}
	components := []operability.Component{operability.ComponentVector}
	if _, _, err = s.AdmitGraphRebuild(context.Background(), "request-correlation", "v1", "request-correlation", operability.RequestFingerprint(components), "submit-request-42", "failed-rebuild", components); err != nil {
		t.Fatal(err)
	}
	task, found, err := s.ClaimOldestQueuedGraphTask(context.Background())
	if err != nil || !found {
		t.Fatalf("task=%+v found=%v err=%v", task, found, err)
	}
	if err = s.ProcessGraphRebuild(context.Background(), task, failingGraphEmbedder{}); err != nil {
		t.Fatal(err)
	}
	stored, found, err := s.LookupGraphTask(context.Background(), task.ID)
	if err != nil || !found || stored.State != graphsnapshot.TaskFailed || stored.Error == nil || stored.Error.RequestID != "submit-request-42" || stored.SubmissionRequestID != "submit-request-42" {
		t.Fatalf("stored=%+v found=%v err=%v", stored, found, err)
	}
}

func TestCancelledRebuildIsRequeuedOnRecovery(t *testing.T) {
	s, err := New(t.TempDir()+"/rag.db", 4)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	seedTrustedRebuildSnapshot(t, s, "cancelled-rebuild", "v1")
	if _, err = s.DB().Exec(`UPDATE graph_tasks SET state='succeeded',phase='completed',progress=10000 WHERE id='initial-task'`); err != nil {
		t.Fatal(err)
	}
	components := []operability.Component{operability.ComponentVector}
	if _, _, err = s.AdmitGraphRebuild(context.Background(), "cancelled-rebuild", "v1", "cancelled", operability.RequestFingerprint(components), "request", "cancelled-task", components); err != nil {
		t.Fatal(err)
	}
	task, found, err := s.ClaimOldestQueuedGraphTask(context.Background())
	if err != nil || !found {
		t.Fatalf("task=%+v found=%v err=%v", task, found, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = s.ProcessGraphRebuild(ctx, task, graphEmbedderFake{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled process err=%v", err)
	}
	if current, found, err := s.LookupGraphTask(context.Background(), task.ID); err != nil || !found || current.State != graphsnapshot.TaskRunning {
		t.Fatalf("current=%+v found=%v err=%v", current, found, err)
	}
	if err = s.RecoverGraphTasks(context.Background()); err != nil {
		t.Fatal(err)
	}
	if current, found, err := s.LookupGraphTask(context.Background(), task.ID); err != nil || !found || current.State != graphsnapshot.TaskQueued || current.Phase != "queued" {
		t.Fatalf("recovered=%+v found=%v err=%v", current, found, err)
	}
}

func TestInjectedPrePromotionFailurePreservesSelectedGeneration(t *testing.T) {
	s, err := New(t.TempDir()+"/rag.db", 4)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	seedTrustedRebuildSnapshot(t, s, "injected", "v1")
	if _, err = s.DB().Exec(`UPDATE graph_tasks SET state='succeeded',phase='completed',progress=10000 WHERE id='initial-task'`); err != nil {
		t.Fatal(err)
	}
	var oldGeneration string
	if err = s.DB().QueryRow(`SELECT generation FROM graph_retrieval_generations WHERE namespace='injected' AND version='v1' AND component='graph_indexes' AND selected=1`).Scan(&oldGeneration); err != nil {
		t.Fatal(err)
	}
	components := []operability.Component{operability.ComponentGraphIndexes}
	if _, _, err = s.AdmitGraphRebuild(context.Background(), "injected", "v1", "inject", operability.RequestFingerprint(components), "request", "injected-task", components); err != nil {
		t.Fatal(err)
	}
	task, found, err := s.ClaimOldestQueuedGraphTask(context.Background())
	if err != nil || !found {
		t.Fatalf("task=%+v found=%v err=%v", task, found, err)
	}
	s.graphRebuildFailpoint = func(boundary string) error {
		if boundary == "before_promotion" {
			return errors.New("injected")
		}
		return nil
	}
	if err = s.ProcessGraphRebuild(context.Background(), task, graphEmbedderFake{}); err != nil {
		t.Fatal(err)
	}
	var selected string
	if err = s.DB().QueryRow(`SELECT generation FROM graph_retrieval_generations WHERE namespace='injected' AND version='v1' AND component='graph_indexes' AND selected=1`).Scan(&selected); err != nil || selected != oldGeneration {
		t.Fatalf("selected=%q old=%q err=%v", selected, oldGeneration, err)
	}
	stored, found, err := s.LookupGraphTask(context.Background(), task.ID)
	if err != nil || !found || stored.State != graphsnapshot.TaskFailed {
		t.Fatalf("task=%+v found=%v err=%v", stored, found, err)
	}
}

type countingGraphEmbedder struct{ calls atomic.Int32 }

func (e *countingGraphEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	e.calls.Add(1)
	return graphEmbedderFake{}.Embed(context.Background(), texts)
}

// TestGraphRebuildFailureBoundaries exercises every durable component and
// validation boundary. A failed boundary may leave private staging data for
// crash recovery, but it can never replace a selected generation. Replaying
// the same idempotency key after reopen returns the original terminal task and
// consequently cannot repeat vector provider work.
func TestGraphRebuildFailureBoundaries(t *testing.T) {
	components := []operability.Component{
		operability.ComponentGraphIndexes,
		operability.ComponentFTS,
		operability.ComponentVector,
	}
	boundaries := []string{
		"before_build_graph_indexes", "after_build_graph_indexes",
		"before_validation_graph_indexes", "after_validation_graph_indexes",
		"before_build_fts", "after_build_fts",
		"before_validation_fts", "after_validation_fts",
		"before_build_vector", "after_build_vector",
		"before_validation_vector", "after_validation_vector",
		"before_promotion", "after_promotion",
	}
	for _, boundary := range boundaries {
		t.Run(boundary, func(t *testing.T) {
			path := t.TempDir() + "/rag.db"
			s, err := New(path, 4)
			if err != nil {
				t.Fatal(err)
			}
			seedTrustedRebuildSnapshot(t, s, "failure-boundary", "v1")
			if err = s.PopulateGraphSearchDocuments(context.Background(), "initial-task"); err != nil {
				t.Fatal(err)
			}
			if err = s.BuildGraphVectors(context.Background(), "initial-task", graphEmbedderFake{}); err != nil {
				t.Fatal(err)
			}
			if _, err = s.DB().Exec(`UPDATE graph_tasks SET state='succeeded',phase='completed',progress=10000 WHERE id='initial-task'`); err != nil {
				t.Fatal(err)
			}
			old := selectedGraphGenerations(t, s, "failure-boundary", "v1")
			fingerprint := operability.RequestFingerprint(components)
			if _, _, err = s.AdmitGraphRebuild(context.Background(), "failure-boundary", "v1", "boundary-"+boundary, fingerprint, "request", "rebuild-task", components); err != nil {
				t.Fatal(err)
			}
			task, found, err := s.ClaimOldestQueuedGraphTask(context.Background())
			if err != nil || !found {
				t.Fatalf("task=%+v found=%v err=%v", task, found, err)
			}
			embedder := &countingGraphEmbedder{}
			s.graphRebuildFailpoint = func(name string) error {
				if name == boundary {
					return errors.New("injected " + boundary)
				}
				return nil
			}
			err = s.ProcessGraphRebuild(context.Background(), task, embedder)
			stored, found, lookupErr := s.LookupGraphTask(context.Background(), task.ID)
			if lookupErr != nil || !found {
				t.Fatalf("stored=%+v found=%v err=%v", stored, found, lookupErr)
			}
			if boundary == "after_promotion" {
				if err == nil || stored.State != graphsnapshot.TaskSucceeded {
					t.Fatalf("post-promotion err=%v task=%+v", err, stored)
				}
			} else if err != nil || stored.State != graphsnapshot.TaskFailed {
				t.Fatalf("err=%v task=%+v", err, stored)
			}
			if boundary != "after_promotion" && !equalGraphGenerations(old, selectedGraphGenerations(t, s, "failure-boundary", "v1")) {
				t.Fatalf("selected generations changed: old=%v now=%v", old, selectedGraphGenerations(t, s, "failure-boundary", "v1"))
			}
			providerCalls := embedder.calls.Load()
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
			s, err = New(path, 4)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if err = s.RecoverGraphTasks(context.Background()); err != nil {
				t.Fatal(err)
			}
			if boundary != "after_promotion" && !equalGraphGenerations(old, selectedGraphGenerations(t, s, "failure-boundary", "v1")) {
				t.Fatalf("restart changed selected generations: old=%v now=%v", old, selectedGraphGenerations(t, s, "failure-boundary", "v1"))
			}
			var private int
			if err = s.DB().QueryRow(`SELECT count(*) FROM graph_retrieval_generations WHERE namespace='failure-boundary' AND version='v1' AND selected=0 AND state IN ('private','validated')`).Scan(&private); err != nil || private != 0 {
				t.Fatalf("private generations=%d err=%v", private, err)
			}
			replayed, replayedOK, err := s.AdmitGraphRebuild(context.Background(), "failure-boundary", "v1", "boundary-"+boundary, fingerprint, "different-request", "ignored", components)
			if err != nil || !replayedOK || replayed.ID != task.ID || replayed.State != stored.State {
				t.Fatalf("replay=%+v replayed=%v err=%v", replayed, replayedOK, err)
			}
			if got := embedder.calls.Load(); got != providerCalls {
				t.Fatalf("provider calls repeated after replay: before=%d after=%d", providerCalls, got)
			}
		})
	}
}

func selectedGraphGenerations(t *testing.T, s *Store, namespace, version string) map[string]string {
	t.Helper()
	rows, err := s.DB().Query(`SELECT component,generation FROM graph_retrieval_generations WHERE namespace=? AND version=? AND selected=1 ORDER BY component`, namespace, version)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	selected := map[string]string{}
	for rows.Next() {
		var component, generation string
		if err = rows.Scan(&component, &generation); err != nil {
			t.Fatal(err)
		}
		selected[component] = generation
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return selected
}

func equalGraphGenerations(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for component, generation := range left {
		if right[component] != generation {
			return false
		}
	}
	return true
}

func TestProcessGraphIndexRebuildPromotesAtomically(t *testing.T) {
	s, err := New(t.TempDir()+"/rag.db", 4)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	node := graphsnapshot.Node{ID: "node", Type: "kind", Label: "label", Text: "text", Properties: []byte(`{}`), Provenance: []byte(`{}`)}
	edge := graphsnapshot.Edge{ID: "edge", From: "node", To: "node", Type: "kind", RelationKind: "explicit", Confidence: "1", Properties: []byte(`{}`), Provenance: []byte(`{}`)}
	_, hash, err := graphsnapshot.CanonicalHash([]graphsnapshot.Node{node}, []graphsnapshot.Edge{edge})
	if err != nil {
		t.Fatal(err)
	}
	service := graphsnapshot.NewService(s, func() (string, error) { return "source-task", nil })
	if _, graphErr := service.Put(context.Background(), "process", "v1", graphsnapshot.Request{SchemaVersion: graphsnapshot.SchemaVersionV1, Mode: graphsnapshot.ModeFull, ContentHash: hash, Nodes: []graphsnapshot.Node{node}, Edges: []graphsnapshot.Edge{edge}}); graphErr != nil {
		t.Fatal(graphErr)
	}
	if err = s.PromoteGraphComponent(context.Background(), "source-task"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB().Exec(`UPDATE graph_tasks SET state='succeeded',phase='completed',progress=10000 WHERE id='source-task'`); err != nil {
		t.Fatal(err)
	}
	components := []operability.Component{operability.ComponentGraphIndexes, operability.ComponentFTS, operability.ComponentVector}
	if _, _, err = s.AdmitGraphRebuild(context.Background(), "process", "v1", "process", operability.RequestFingerprint(components), "req", "rebuild-task", components); err != nil {
		t.Fatal(err)
	}
	task, found, err := s.ClaimOldestQueuedGraphTask(context.Background())
	if err != nil || !found || task.Operation != "snapshot_rebuild" {
		t.Fatalf("task=%+v found=%v err=%v", task, found, err)
	}
	if err = s.ProcessGraphRebuild(context.Background(), task, graphEmbedderFake{}); err != nil {
		t.Fatal(err)
	}
	stored, found, err := s.LookupGraphTask(context.Background(), "rebuild-task")
	if err != nil || !found || stored.State != graphsnapshot.TaskSucceeded || stored.Phase != "completed" {
		t.Fatalf("stored=%+v found=%v err=%v", stored, found, err)
	}
	var selected string
	var selectedCount int
	if err = s.DB().QueryRow(`SELECT generation FROM graph_retrieval_generations WHERE namespace='process' AND version='v1' AND component='graph_indexes' AND selected=1`).Scan(&selected); err != nil || selected != "graph-indexes-rebuild-task" {
		t.Fatalf("selected=%q err=%v", selected, err)
	}
	for _, component := range []string{"fts", "vector"} {
		if err = s.DB().QueryRow(`SELECT count(*) FROM graph_retrieval_generations WHERE namespace='process' AND version='v1' AND component=? AND selected=1`, component).Scan(&selectedCount); err != nil || selectedCount != 1 {
			t.Fatalf("selected %s=%d err=%v", component, selectedCount, err)
		}
	}
}
