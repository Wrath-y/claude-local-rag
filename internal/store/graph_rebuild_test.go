package store

import (
	"context"
	"errors"
	"testing"

	"github.com/Wrath-y/local-rag/internal/operability"
)

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
