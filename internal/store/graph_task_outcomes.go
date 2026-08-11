package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Wrath-y/local-rag/internal/graphsnapshot"
)

// FailRequiredGraphComponent records a terminal required-component failure
// and its durable task outcome together. The public error is catalogued JSON;
// the wrapped provider or SQLite cause is never persisted in error_json.
func (s *Store) FailRequiredGraphComponent(ctx context.Context, taskID string, component graphsnapshot.ComponentName, graphErr *graphsnapshot.Error) error {
	if component != graphsnapshot.ComponentGraph && component != graphsnapshot.ComponentFTS {
		return fmt.Errorf("%w: %s", ErrInvalidGraphIdentity, component)
	}
	if graphErr == nil {
		graphErr = graphsnapshot.NewError(graphsnapshot.CodeInternalError, nil, nil)
	}
	errorJSON, err := json.Marshal(graphErr)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var namespace, version string
	if err = tx.QueryRowContext(ctx, `SELECT namespace,version FROM graph_tasks WHERE id=?`, taskID).Scan(&namespace, &version); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE graph_snapshot_components SET state='failed',error_json=? WHERE namespace=? AND version=? AND component=?`, string(errorJSON), namespace, version, component); err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err = tx.ExecContext(ctx, `UPDATE graph_snapshots SET status='failed',query_ready=0,updated_at=? WHERE namespace=? AND version=?`, now, namespace, version); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE graph_tasks SET state='failed',phase=?,error_json=?,finished_at=? WHERE id=? AND state='running'`, string(component), string(errorJSON), now, taskID); err != nil {
		return err
	}
	return tx.Commit()
}

// MarkGraphVectorFailed treats the optional vector component as a degraded
// outcome. It preserves graph+FTS readiness and lets reconciliation complete
// the durable task once the vector attempt has ended.
func (s *Store) MarkGraphVectorFailed(ctx context.Context, taskID string, graphErr *graphsnapshot.Error, warning string) error {
	if graphErr == nil {
		graphErr = graphsnapshot.NewError(graphsnapshot.CodeInternalError, nil, nil)
	}
	errorJSON, err := json.Marshal(graphErr)
	if err != nil {
		return err
	}
	var namespace, version string
	if err = s.db.QueryRowContext(ctx, `SELECT namespace,version FROM graph_tasks WHERE id=?`, taskID).Scan(&namespace, &version); err != nil {
		return err
	}
	if _, err = s.db.ExecContext(ctx, `UPDATE graph_snapshot_components SET state='failed',error_json=?,warning=? WHERE namespace=? AND version=? AND component='vector'`, string(errorJSON), warning, namespace, version); err != nil {
		return err
	}
	return s.ReconcileGraphSnapshot(ctx, namespace, version)
}
