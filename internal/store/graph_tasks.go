package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/Wrath-y/local-rag/internal/graphsnapshot"
	"github.com/Wrath-y/local-rag/internal/observe"
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
	var task graphsnapshot.Task
	var progress int
	var created, started string
	err = tx.QueryRowContext(ctx, `UPDATE graph_tasks
SET state='running',phase='running',started_at=?
WHERE id=(SELECT id FROM graph_tasks WHERE state='queued' ORDER BY created_at,id LIMIT 1)
  AND state='queued'
		RETURNING id,namespace,version,operation,state,phase,progress,created_at,started_at`, now).Scan(&task.ID, &task.Namespace, &task.Version, &task.Operation, &task.State, &task.Phase, &progress, &created, &started)
	if err == sql.ErrNoRows {
		return graphsnapshot.Task{}, false, tx.Commit()
	}
	if err != nil {
		return graphsnapshot.Task{}, false, fmt.Errorf("claim graph task: %w", err)
	}
	if created == "" || started == "" {
		return graphsnapshot.Task{}, false, fmt.Errorf("claim graph task returned incomplete timestamps")
	}
	task.Progress = float64(progress) / 10000
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
	observe.GraphTaskTransitions.WithLabelValues(task.Operation, string(graphsnapshot.TaskRunning)).Inc()
	s.observeGraphTaskQueue(ctx)
	observe.GraphEvent("task_claimed", task.Operation, "", task.ID, "", "")
	return task, true, nil
}

func (s *Store) observeGraphTaskQueue(ctx context.Context) {
	var queued int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM graph_tasks WHERE state='queued'`).Scan(&queued); err == nil {
		observe.GraphTaskQueueDepth.Set(float64(queued))
	}
}

// AddGraphTaskWarning persists one safe, canonical warning while a task is
// mutable. Sorting makes independent component boundaries deterministic and
// avoids duplicate warnings across restart/retry paths.
func (s *Store) AddGraphTaskWarning(ctx context.Context, id, warning string) (bool, error) {
	if warning == "" {
		return false, fmt.Errorf("graph task warning is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var encoded string
	if err = tx.QueryRowContext(ctx, `SELECT warnings_json FROM graph_tasks WHERE id=? AND state IN ('queued','running')`, id).Scan(&encoded); err == sql.ErrNoRows {
		return false, tx.Commit()
	} else if err != nil {
		return false, err
	}
	var warnings []string
	if err = json.Unmarshal([]byte(encoded), &warnings); err != nil {
		return false, err
	}
	for _, item := range warnings {
		if item == warning {
			return false, tx.Commit()
		}
	}
	warnings = append(warnings, warning)
	sort.Strings(warnings)
	encodedBytes, err := json.Marshal(warnings)
	if err != nil {
		return false, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE graph_tasks SET warnings_json=?,updated_at=? WHERE id=? AND state IN ('queued','running')`, string(encodedBytes), time.Now().UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return false, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return changed == 1, nil
}

// AdvanceGraphTaskProgress applies a monotonic durable phase/progress update
// only while the claimed task is still running.
func (s *Store) AdvanceGraphTaskProgress(ctx context.Context, id, phase string, progress int) (bool, error) {
	if progress < 0 || progress > 10000 || phase == "" {
		return false, fmt.Errorf("invalid graph task progress")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE graph_tasks SET phase=?,progress=? WHERE id=? AND state='running' AND progress<=?`, phase, progress, id, progress)
	if err != nil {
		return false, err
	}
	changed, err := result.RowsAffected()
	return changed == 1, err
}
