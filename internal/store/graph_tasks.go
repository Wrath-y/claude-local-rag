package store

import (
	"context"
	"fmt"
	"time"

	"github.com/Wrath-y/local-rag/internal/graphsnapshot"
)

// ClaimOldestQueuedGraphTask atomically changes exactly one oldest queued task
// to running. The transaction commits before the caller dispatches work, so
// provider calls can never hold a SQLite write transaction open.
func (s *Store) ClaimOldestQueuedGraphTask(ctx context.Context) (graphsnapshot.Task, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return graphsnapshot.Task{}, false, err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := tx.ExecContext(ctx, `UPDATE graph_tasks SET state='running',phase='running',started_at=? WHERE id=(SELECT id FROM graph_tasks WHERE state='queued' ORDER BY created_at,id LIMIT 1) AND state='queued'`, now)
	if err != nil {
		return graphsnapshot.Task{}, false, fmt.Errorf("claim graph task: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return graphsnapshot.Task{}, false, err
	}
	if changed == 0 {
		return graphsnapshot.Task{}, false, tx.Commit()
	}
	var task graphsnapshot.Task
	var created, started string
	if err := tx.QueryRowContext(ctx, `SELECT id,namespace,version,state,phase,progress,created_at,started_at FROM graph_tasks WHERE state='running' AND started_at=? ORDER BY id LIMIT 1`, now).Scan(&task.ID, &task.Namespace, &task.Version, &task.State, &task.Phase, &task.Progress, &created, &started); err != nil {
		return graphsnapshot.Task{}, false, err
	}
	if task.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
		return graphsnapshot.Task{}, false, err
	}
	parsed, err := time.Parse(time.RFC3339Nano, started)
	if err != nil {
		return graphsnapshot.Task{}, false, err
	}
	task.StartedAt = &parsed
	if err := tx.Commit(); err != nil {
		return graphsnapshot.Task{}, false, err
	}
	return task, true, nil
}

// AdvanceGraphTaskProgress applies a monotonic durable phase/progress update
// only while the claimed task is still running.
func (s *Store) AdvanceGraphTaskProgress(ctx context.Context, id, phase string, progress int) (bool, error) {
	if progress < 0 || progress > 100 || phase == "" {
		return false, fmt.Errorf("invalid graph task progress")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE graph_tasks SET phase=?,progress=? WHERE id=? AND state='running' AND progress<=?`, phase, progress, id, progress)
	if err != nil {
		return false, err
	}
	changed, err := result.RowsAffected()
	return changed == 1, err
}
