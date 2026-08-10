package store

import (
	"context"
	"testing"
)

func TestClaimOldestQueuedGraphTaskAndAdvanceProgressMonotonically(t *testing.T) {
	s, err := New(t.TempDir()+"/rag.db", 4)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	seedGraphSnapshot(t, s, "tasks", "first", "first")
	seedGraphSnapshot(t, s, "tasks", "second", "second")
	if _, err := s.DB().Exec(`INSERT INTO graph_tasks(id,namespace,version,state,phase,progress,created_at) VALUES('task-tasks-first','tasks','first','queued','queued',0,'2026-08-10T00:00:00Z'),('task-tasks-second','tasks','second','queued','queued',0,'2026-08-10T00:00:01Z')`); err != nil {
		t.Fatal(err)
	}
	first, found, err := s.ClaimOldestQueuedGraphTask(context.Background())
	if err != nil || !found || first.ID != "task-tasks-first" || first.State != "running" {
		t.Fatalf("first=%#v found=%v err=%v", first, found, err)
	}
	if changed, err := s.AdvanceGraphTaskProgress(context.Background(), first.ID, "graph", 40); err != nil || !changed {
		t.Fatalf("advance changed=%v err=%v", changed, err)
	}
	if changed, err := s.AdvanceGraphTaskProgress(context.Background(), first.ID, "older", 20); err != nil || changed {
		t.Fatalf("backward changed=%v err=%v", changed, err)
	}
	second, found, err := s.ClaimOldestQueuedGraphTask(context.Background())
	if err != nil || !found || second.ID != "task-tasks-second" {
		t.Fatalf("second=%#v found=%v err=%v", second, found, err)
	}
	if _, found, err := s.ClaimOldestQueuedGraphTask(context.Background()); err != nil || found {
		t.Fatalf("empty found=%v err=%v", found, err)
	}
}
