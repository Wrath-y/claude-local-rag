package store

import (
	"context"
	"fmt"
	"time"
)

// MarkGraphVectorUnavailable records an optional-component degradation. Graph
// and FTS remain the only required readiness inputs.
func (s *Store) MarkGraphVectorUnavailable(ctx context.Context, taskID, warning string) error {
	var namespace, version string
	if err := s.db.QueryRowContext(ctx, `SELECT namespace,version FROM graph_tasks WHERE id=?`, taskID).Scan(&namespace, &version); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE graph_snapshot_components SET state='unavailable',warning=? WHERE namespace=? AND version=? AND component='vector'`, warning, namespace, version); err != nil {
		return err
	}
	return s.ReconcileGraphSnapshot(ctx, namespace, version)
}

// ReconcileGraphSnapshot derives top-level lifecycle state from components.
func (s *Store) ReconcileGraphSnapshot(ctx context.Context, namespace, version string) error {
	rows, err := s.db.QueryContext(ctx, `SELECT component,state FROM graph_snapshot_components WHERE namespace=? AND version=?`, namespace, version)
	if err != nil {
		return err
	}
	defer rows.Close()
	states := map[string]string{}
	for rows.Next() {
		var component, state string
		if err := rows.Scan(&component, &state); err != nil {
			return err
		}
		states[component] = state
	}
	if err := rows.Err(); err != nil {
		return err
	}
	status, ready := "building", 0
	if states["graph"] == "failed" || states["fts"] == "failed" {
		status = "failed"
	} else if states["graph"] == "ready" && states["fts"] == "ready" {
		status = "ready"
		ready = 1
	}
	if _, err = s.db.ExecContext(ctx, `UPDATE graph_snapshots SET status=?,query_ready=?,updated_at=? WHERE namespace=? AND version=?`, status, ready, time.Now().UTC().Format(time.RFC3339Nano), namespace, version); err != nil {
		return err
	}
	if status == "ready" {
		_, err = s.db.ExecContext(ctx, `UPDATE graph_tasks SET state='succeeded',phase='completed',progress=100,finished_at=? WHERE namespace=? AND version=? AND state='running'`, time.Now().UTC().Format(time.RFC3339Nano), namespace, version)
		return err
	}
	if status == "failed" {
		return fmt.Errorf("required graph component failed")
	}
	return nil
}
