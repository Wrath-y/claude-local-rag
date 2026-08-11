package store

import (
	"context"
	"fmt"
)

// DeleteGraphSnapshot removes one inactive, quiescent snapshot and its
// dependent graph/FTS/task metadata atomically. Vector virtual rows are
// explicitly removed by graph_vector_items_ad during the cascade.
func (s *Store) DeleteGraphSnapshot(ctx context.Context, namespace, version string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var active string
	if err = tx.QueryRowContext(ctx, `SELECT active_version FROM graph_namespace_heads WHERE namespace=?`, namespace).Scan(&active); err == nil && active == version {
		return fmt.Errorf("active graph snapshot cannot be deleted")
	}
	var writers int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM graph_tasks WHERE namespace=? AND version=? AND state IN ('queued','running')`, namespace, version).Scan(&writers); err != nil {
		return err
	}
	if writers > 0 {
		return fmt.Errorf("graph snapshot write is in progress")
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM graph_snapshots WHERE namespace=? AND version=?`, namespace, version)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed == 0 {
		return fmt.Errorf("graph snapshot not found")
	}
	return tx.Commit()
}
