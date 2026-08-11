package store

import "context"

// RecoverGraphTasks makes an interrupted process restart safe without
// inventing a fifth task state. Private vector generations are removed unless
// the snapshot component explicitly selected that generation.
func (s *Store) RecoverGraphTasks(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE graph_tasks SET state='queued',phase='queued',started_at=NULL WHERE state='running'`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM graph_vector_items AS item WHERE NOT EXISTS (SELECT 1 FROM graph_snapshot_components AS component WHERE component.namespace=item.namespace AND component.version=item.version AND component.component='vector' AND component.generation=item.generation)`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM graph_index_adjacency AS adjacency WHERE NOT EXISTS (SELECT 1 FROM graph_retrieval_generations AS generation WHERE generation.namespace=adjacency.namespace AND generation.version=adjacency.version AND generation.component='graph_indexes' AND generation.generation=adjacency.generation AND generation.selected=1 AND generation.state='selected')`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM graph_retrieval_generations WHERE component='graph_indexes' AND selected=0 AND state IN ('private','validated','discarded')`); err != nil {
		return err
	}
	return tx.Commit()
}
