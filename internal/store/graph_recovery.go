package store

import (
	"context"
	"time"

	"github.com/Wrath-y/local-rag/internal/observe"
)

// RecoverGraphTasks makes an interrupted process restart safe without
// inventing a fifth task state. Private vector generations are removed unless
// the snapshot component explicitly selected that generation.
func (s *Store) RecoverGraphTasks(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err = tx.ExecContext(ctx, `UPDATE graph_tasks SET state='queued',phase='queued',started_at=NULL,updated_at=? WHERE state='running'`, now); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM graph_vector_items AS item WHERE NOT EXISTS (SELECT 1 FROM graph_retrieval_generations AS generation WHERE generation.namespace=item.namespace AND generation.version=item.version AND generation.component='vector' AND generation.generation=item.generation AND generation.selected=1 AND generation.state='selected')`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM graph_search_documents AS document WHERE NOT EXISTS (SELECT 1 FROM graph_retrieval_generations AS generation WHERE generation.namespace=document.namespace AND generation.version=document.version AND generation.component='fts' AND generation.generation=document.generation AND generation.selected=1 AND generation.state='selected')`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM graph_index_adjacency AS adjacency WHERE NOT EXISTS (SELECT 1 FROM graph_retrieval_generations AS generation WHERE generation.namespace=adjacency.namespace AND generation.version=adjacency.version AND generation.component='graph_indexes' AND generation.generation=adjacency.generation AND generation.selected=1 AND generation.state='selected')`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM graph_retrieval_generations WHERE selected=0 AND state IN ('private','validated','discarded')`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE graph_task_steps SET state='pending',generation=NULL,content_digest=NULL,updated_at=? WHERE task_id IN (SELECT id FROM graph_tasks WHERE state='queued' AND operation='snapshot_rebuild') AND state IN ('building','validated','discarded')`, now); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	observe.GraphRecoveryTotal.Inc()
	return nil
}
