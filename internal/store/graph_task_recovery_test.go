package store

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Wrath-y/local-rag/internal/graphsnapshot"
)

func TestRecoverGraphTasksRequeuesRunningAndPreservesTerminalResources(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rag.db")
	s, err := New(path, 4)
	if err != nil {
		t.Fatal(err)
	}
	seedGraphSnapshot(t, s, "recover-tasks", "v1", "source")
	const created = "2026-08-12T00:00:00Z"
	if _, err = s.DB().Exec(`
INSERT INTO graph_tasks(id,namespace,version,operation,requested_components_json,source_hash,submission_request_id,state,phase,progress,warnings_json,result_json,created_at,started_at,finished_at,updated_at) VALUES
 ('running-rebuild','recover-tasks','v1','snapshot_rebuild','["graph_indexes"]',?, 'submit-running','running','building_graph_indexes',4000,'["first","second"]',NULL,?, ?,NULL,?),
 ('completed-rebuild','recover-tasks','v1','snapshot_rebuild','["graph_indexes"]',?, 'submit-completed','succeeded','completed',10000,'["first","second"]','{"generations":[{"component":"graph_indexes","generation":"selected-index","content_digest":"digest"}]}',?, ?,?,?),
 ('failed-rebuild','recover-tasks','v1','snapshot_rebuild','["fts"]',?, 'submit-failed','failed','completed',6000,'["warning"]',NULL,?, ?,?,?)`,
		testGraphTaskHash(), created, created, created,
		testGraphTaskHash(), created, created, created, created,
		testGraphTaskHash(), created, created, created, created,
	); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB().Exec(`UPDATE graph_tasks SET error_json='{"code":"REIMPORT_REQUIRED","message":"Graph snapshot must be reimported","retryable":false,"details":{}}' WHERE id='failed-rebuild'`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB().Exec(`INSERT INTO graph_task_steps(task_id,component,state,generation,content_digest,updated_at) VALUES('running-rebuild','graph_indexes','validated','private-index',?,?)`, testGraphTaskHash(), created); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB().Exec(`INSERT INTO graph_retrieval_generations(namespace,version,component,generation,state,selected,algorithm,content_digest,created_at) VALUES('recover-tasks','v1','graph_indexes','private-index','private',0,'edge-adjacency-v1',?,?),('recover-tasks','v1','graph_indexes','selected-index','selected',1,'edge-adjacency-v1',?,?)`, testGraphTaskHash(), created, testGraphTaskHash(), created); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB().Exec(`INSERT INTO graph_index_adjacency(namespace,version,generation,direction,node_id,edge_id,relation_kind,edge_type) VALUES('recover-tasks','v1','private-index','outgoing','node-a','edge-shared','explicit','kind'),('recover-tasks','v1','selected-index','outgoing','node-a','edge-shared','explicit','kind')`); err != nil {
		t.Fatal(err)
	}

	beforeSucceeded, found, err := s.LookupGraphTask(context.Background(), "completed-rebuild")
	if err != nil || !found {
		t.Fatalf("succeeded task found=%v err=%v", found, err)
	}
	beforeFailed, found, err := s.LookupGraphTask(context.Background(), "failed-rebuild")
	if err != nil || !found {
		t.Fatalf("failed task found=%v err=%v", found, err)
	}
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

	running, found, err := s.LookupGraphTask(context.Background(), "running-rebuild")
	if err != nil || !found || running.State != graphsnapshot.TaskQueued || running.Phase != "queued" || running.Progress != 0.4 || running.StartedAt != nil {
		t.Fatalf("recovered running task=%+v found=%v err=%v", running, found, err)
	}
	var stepState, stepGeneration string
	if err = s.DB().QueryRow(`SELECT state,COALESCE(generation,'') FROM graph_task_steps WHERE task_id='running-rebuild' AND component='graph_indexes'`).Scan(&stepState, &stepGeneration); err != nil || stepState != "pending" || stepGeneration != "" {
		t.Fatalf("step state=%q generation=%q err=%v", stepState, stepGeneration, err)
	}
	var privateRows, selectedRows int
	if err = s.DB().QueryRow(`SELECT count(*) FROM graph_retrieval_generations WHERE generation='private-index'`).Scan(&privateRows); err != nil || privateRows != 0 {
		t.Fatalf("private rows=%d err=%v", privateRows, err)
	}
	if err = s.DB().QueryRow(`SELECT count(*) FROM graph_retrieval_generations WHERE generation='selected-index' AND selected=1 AND state='selected'`).Scan(&selectedRows); err != nil || selectedRows != 1 {
		t.Fatalf("selected rows=%d err=%v", selectedRows, err)
	}

	afterSucceeded, found, err := s.LookupGraphTask(context.Background(), "completed-rebuild")
	if err != nil || !found || !reflect.DeepEqual(beforeSucceeded, afterSucceeded) {
		t.Fatalf("succeeded before=%+v after=%+v found=%v err=%v", beforeSucceeded, afterSucceeded, found, err)
	}
	afterFailed, found, err := s.LookupGraphTask(context.Background(), "failed-rebuild")
	if err != nil || !found || !reflect.DeepEqual(beforeFailed, afterFailed) {
		t.Fatalf("failed before=%+v after=%+v found=%v err=%v", beforeFailed, afterFailed, found, err)
	}
	if _, found, err = s.LookupGraphTask(context.Background(), "unknown-task"); err != nil || found {
		t.Fatalf("unknown task found=%v err=%v", found, err)
	}
}

func testGraphTaskHash() string {
	return strings.Repeat("a", 64)
}
