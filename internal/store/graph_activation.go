package store

import (
	"context"
	"fmt"
)

// ActivateGraphSnapshot switches one namespace head only after confirming the
// requested immutable snapshot is query-ready. No graph/index rows move.
func (s *Store) ActivateGraphSnapshot(ctx context.Context, namespace, version string) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var ready int
	if err = tx.QueryRowContext(ctx, `SELECT query_ready FROM graph_snapshots WHERE namespace=? AND version=?`, namespace, version).Scan(&ready); err != nil {
		return false, err
	}
	if ready != 1 {
		return false, fmt.Errorf("graph snapshot is not ready")
	}
	var current string
	err = tx.QueryRowContext(ctx, `SELECT active_version FROM graph_namespace_heads WHERE namespace=?`, namespace).Scan(&current)
	if err == nil && current == version {
		return false, tx.Commit()
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO graph_namespace_heads(namespace,active_version) VALUES(?,?) ON CONFLICT(namespace) DO UPDATE SET active_version=excluded.active_version`, namespace, version); err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
