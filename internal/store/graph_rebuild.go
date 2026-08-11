package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Wrath-y/local-rag/internal/graphsnapshot"
	"github.com/Wrath-y/local-rag/internal/operability"
)

// AdmitGraphRebuild is the short, transactional durable admission boundary.
// It deliberately reads only snapshot metadata and immutable row counts; full
// source verification remains worker work and never involves legacy chunks.
func (s *Store) AdmitGraphRebuild(ctx context.Context, namespace, version, idempotencyKey, fingerprint, submissionRequestID, taskID string, components []operability.Component) (graphsnapshot.Task, bool, error) {
	if err := s.GraphUnavailable(); err != nil {
		return graphsnapshot.Task{}, false, err
	}
	normalized, err := operability.NormalizeComponents(components)
	if err != nil {
		return graphsnapshot.Task{}, false, err
	}
	componentsJSON, err := json.Marshal(normalized)
	if err != nil {
		return graphsnapshot.Task{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return graphsnapshot.Task{}, false, err
	}
	defer tx.Rollback()

	var sourceHash string
	var expectedNodes, expectedEdges, actualNodes, actualEdges int
	err = tx.QueryRowContext(ctx, `SELECT content_hash,node_count,edge_count FROM graph_snapshots WHERE namespace=? AND version=?`, namespace, version).Scan(&sourceHash, &expectedNodes, &expectedEdges)
	if errors.Is(err, sql.ErrNoRows) {
		return graphsnapshot.Task{}, false, operability.ErrSnapshotNotFound
	}
	if err != nil {
		return graphsnapshot.Task{}, false, fmt.Errorf("read rebuild snapshot: %w", err)
	}
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM graph_nodes WHERE namespace=? AND version=?`, namespace, version).Scan(&actualNodes); err != nil {
		return graphsnapshot.Task{}, false, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM graph_edges WHERE namespace=? AND version=?`, namespace, version).Scan(&actualEdges); err != nil {
		return graphsnapshot.Task{}, false, err
	}
	var graphState string
	if err = tx.QueryRowContext(ctx, `SELECT state FROM graph_snapshot_components WHERE namespace=? AND version=? AND component='graph'`, namespace, version).Scan(&graphState); err != nil || graphState != string(graphsnapshot.ComponentReady) || actualNodes != expectedNodes || actualEdges != expectedEdges {
		return graphsnapshot.Task{}, false, operability.ErrReimportRequired
	}

	var existingID, existingFingerprint, existingHash string
	var existingState graphsnapshot.TaskState
	err = tx.QueryRowContext(ctx, `SELECT i.task_id,i.request_fingerprint,i.source_hash,t.state FROM graph_rebuild_idempotency i JOIN graph_tasks t ON t.id=i.task_id WHERE i.namespace=? AND i.version=? AND i.operation='snapshot_rebuild' AND i.idempotency_key=?`, namespace, version, idempotencyKey).Scan(&existingID, &existingFingerprint, &existingHash, &existingState)
	if err == nil {
		if existingFingerprint != fingerprint || existingHash != sourceHash {
			return graphsnapshot.Task{}, false, operability.ErrIdempotencyConflict
		}
		if err = tx.Commit(); err != nil {
			return graphsnapshot.Task{}, false, err
		}
		return graphsnapshot.Task{ID: existingID, Namespace: namespace, Version: version, State: existingState}, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return graphsnapshot.Task{}, false, fmt.Errorf("lookup rebuild idempotency: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err = tx.ExecContext(ctx, `INSERT INTO graph_tasks(id,namespace,version,operation,requested_components_json,source_hash,submission_request_id,state,phase,progress,warnings_json,created_at,updated_at) VALUES(?,?,?,?,?,?,?,'queued','queued',0,'[]',?,?)`, taskID, namespace, version, "snapshot_rebuild", string(componentsJSON), sourceHash, submissionRequestID, now, now); err != nil {
		return graphsnapshot.Task{}, false, fmt.Errorf("create rebuild task: %w", err)
	}
	for _, component := range normalized {
		if _, err = tx.ExecContext(ctx, `INSERT INTO graph_task_steps(task_id,component,state,updated_at) VALUES(?,?,'pending',?)`, taskID, string(component), now); err != nil {
			return graphsnapshot.Task{}, false, fmt.Errorf("create rebuild step: %w", err)
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO graph_rebuild_idempotency(namespace,version,operation,idempotency_key,request_fingerprint,source_hash,task_id,created_at) VALUES(?,?, 'snapshot_rebuild',?,?,?,?,?)`, namespace, version, idempotencyKey, fingerprint, sourceHash, taskID, now); err != nil {
		return graphsnapshot.Task{}, false, fmt.Errorf("record rebuild idempotency: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return graphsnapshot.Task{}, false, err
	}
	return graphsnapshot.Task{ID: taskID, Namespace: namespace, Version: version, State: graphsnapshot.TaskQueued, Phase: "queued"}, false, nil
}
