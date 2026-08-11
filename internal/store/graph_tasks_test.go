package store

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/Wrath-y/local-rag/internal/graphsnapshot"
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

func TestClaimOldestQueuedGraphTaskClaimsEachTaskOnceUnderContention(t *testing.T) {
	s, err := New(t.TempDir()+"/rag.db", 4)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, version := range []string{"one", "two"} {
		seedGraphSnapshot(t, s, "claim", version, version)
	}
	if _, err := s.DB().Exec(`INSERT INTO graph_tasks(id,namespace,version,state,phase,progress,created_at) VALUES('task-claim-one','claim','one','queued','queued',0,'2026-08-10T00:00:00Z'),('task-claim-two','claim','two','queued','queued',0,'2026-08-10T00:00:01Z')`); err != nil {
		t.Fatal(err)
	}
	results := make(chan string, 2)
	errors := make(chan error, 2)
	var group sync.WaitGroup
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			task, found, claimErr := s.ClaimOldestQueuedGraphTask(context.Background())
			if claimErr != nil {
				errors <- claimErr
				return
			}
			if found {
				results <- task.ID
			}
		}()
	}
	group.Wait()
	close(results)
	close(errors)
	for claimErr := range errors {
		t.Fatal(claimErr)
	}
	seen := map[string]bool{}
	for id := range results {
		if seen[id] {
			t.Fatalf("task %q was claimed twice", id)
		}
		seen[id] = true
	}
	if len(seen) != 2 || !seen["task-claim-one"] || !seen["task-claim-two"] {
		t.Fatalf("claimed tasks = %#v", seen)
	}
}

func TestLookupGraphTaskReturnsDurableTerminalResource(t *testing.T) {
	s, err := New(t.TempDir()+"/rag.db", 4)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	seedGraphSnapshot(t, s, "tasks", "terminal", "terminal")
	if _, err := s.DB().Exec(`INSERT INTO graph_tasks(id,namespace,version,state,phase,progress,error_json,created_at,started_at,finished_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, "task-tasks-terminal", "tasks", "terminal", "failed", "fts", 60, `{"code":"INTERNAL_ERROR","message":"Graph lifecycle operation failed","retryable":false,"details":{}}`, "2026-08-10T00:00:00Z", "2026-08-10T00:00:01Z", "2026-08-10T00:00:02Z"); err != nil {
		t.Fatal(err)
	}
	task, found, err := s.LookupGraphTask(context.Background(), "task-tasks-terminal")
	if err != nil || !found || task.State != graphsnapshot.TaskFailed || task.Error == nil || task.Error.Code != graphsnapshot.CodeInternalError || task.StartedAt == nil || task.FinishedAt == nil {
		t.Fatalf("task=%#v found=%v err=%v", task, found, err)
	}
	if _, found, err := s.LookupGraphTask(context.Background(), "missing"); err != nil || found {
		t.Fatalf("missing found=%v err=%v", found, err)
	}
	if strings.Contains(task.Error.Message, "secret") {
		t.Fatal("unexpected unsafe task error message")
	}
}
