package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Wrath-y/local-rag/internal/graphsnapshot"
)

var (
	// ErrGraphSnapshotNotFound is intentionally storage-level. The /v1
	// lifecycle service maps it to its stable SNAPSHOT_NOT_FOUND response.
	ErrGraphSnapshotNotFound = errors.New("graph snapshot not found")
	ErrGraphTaskNotFound     = errors.New("graph task not found")
	ErrGraphSnapshotNotReady = errors.New("graph snapshot not ready")
	ErrGraphSnapshotActive   = errors.New("active graph snapshot cannot be deleted")
	ErrGraphSnapshotWriting  = errors.New("graph snapshot write is in progress")
	ErrInvalidGraphIdentity  = errors.New("graph namespace and version are required")
)

// GraphSnapshotRecord is the complete immutable graph source for exactly one
// namespace/version. It deliberately contains no selected derived index rows,
// so delta materialization and later graph consumers cannot accidentally read
// unscoped legacy data or private build state.
type GraphSnapshotRecord struct {
	Namespace     string
	Version       string
	BaseVersion   *string
	SchemaVersion string
	ContentHash   string
	NodeCount     int
	EdgeCount     int
	TaskID        string
	Status        graphsnapshot.SnapshotStatus
	QueryReady    bool
	Nodes         []graphsnapshot.Node
	Edges         []graphsnapshot.Edge
	Generations   []GraphGenerationRecord
}

// GraphGenerationRecord is safe derived-index metadata for a scoped graph
// source; it never includes node text, vectors, or provider payloads.
type GraphGenerationRecord struct {
	Component     string
	Generation    string
	State         string
	Selected      bool
	Algorithm     string
	Provider      string
	Model         string
	Dimensions    int
	Tokenizer     string
	ContentDigest string
}

// LookupGraphTask returns the durable task resource without inferring state
// from process-local worker data. A task ID remains readable across restarts.
func (s *Store) LookupGraphTask(ctx context.Context, id string) (graphsnapshot.Task, bool, error) {
	if id == "" {
		return graphsnapshot.Task{}, false, nil
	}
	if err := s.GraphUnavailable(); err != nil {
		return graphsnapshot.Task{}, false, err
	}
	var task graphsnapshot.Task
	var createdAt string
	var progress int
	var startedAt, finishedAt, errorJSON, resultJSON, warningsJSON, sourceHash, submissionRequestID sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT id,namespace,version,operation,phase,progress,created_at,started_at,finished_at,warnings_json,error_json,result_json,source_hash,submission_request_id,state FROM graph_tasks WHERE id=?`, id).Scan(
		&task.ID, &task.Namespace, &task.Version, &task.Operation, &task.Phase, &progress, &createdAt, &startedAt, &finishedAt, &warningsJSON, &errorJSON, &resultJSON, &sourceHash, &submissionRequestID, &task.State,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return graphsnapshot.Task{}, false, nil
	}
	if err != nil {
		return graphsnapshot.Task{}, false, fmt.Errorf("lookup graph task: %w", err)
	}
	task.Progress = float64(progress) / 10000
	var parseErr error
	if task.CreatedAt, parseErr = time.Parse(time.RFC3339Nano, createdAt); parseErr != nil {
		return graphsnapshot.Task{}, false, fmt.Errorf("parse graph task created_at: %w", parseErr)
	}
	if task.StartedAt, parseErr = graphTaskTime(startedAt); parseErr != nil {
		return graphsnapshot.Task{}, false, parseErr
	}
	if task.FinishedAt, parseErr = graphTaskTime(finishedAt); parseErr != nil {
		return graphsnapshot.Task{}, false, parseErr
	}
	if errorJSON.Valid {
		var graphErr graphsnapshot.Error
		if err := json.Unmarshal([]byte(errorJSON.String), &graphErr); err != nil {
			return graphsnapshot.Task{}, false, fmt.Errorf("decode graph task error: %w", err)
		}
		task.Error = &graphErr
	}
	if warningsJSON.Valid {
		if err := json.Unmarshal([]byte(warningsJSON.String), &task.Warnings); err != nil {
			return graphsnapshot.Task{}, false, fmt.Errorf("decode graph task warnings: %w", err)
		}
	}
	if resultJSON.Valid {
		task.Result = json.RawMessage(resultJSON.String)
	}
	if sourceHash.Valid {
		task.SourceHash = sourceHash.String
	}
	if submissionRequestID.Valid {
		task.SubmissionRequestID = submissionRequestID.String
	}
	return task, true, nil
}

func graphTaskTime(value sql.NullString) (*time.Time, error) {
	if !value.Valid {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value.String)
	if err != nil {
		return nil, fmt.Errorf("parse graph task timestamp: %w", err)
	}
	return &parsed, nil
}

// LookupGraphSnapshot builds the public lifecycle resource for an already
// accepted snapshot. It is deliberately read-only and scoped so PUT replay
// handling can decide before it attempts any delta materialization or writes.
func (s *Store) LookupGraphSnapshot(ctx context.Context, namespace, version string) (graphsnapshot.Snapshot, bool, error) {
	if namespace == "" || version == "" {
		return graphsnapshot.Snapshot{}, false, ErrInvalidGraphIdentity
	}
	if err := s.GraphUnavailable(); err != nil {
		return graphsnapshot.Snapshot{}, false, err
	}
	var snapshot graphsnapshot.Snapshot
	var baseVersion sql.NullString
	var queryReady int
	err := s.db.QueryRowContext(ctx, `SELECT namespace,version,base_version,schema_version,content_hash,node_count,edge_count,task_id,status,query_ready FROM graph_snapshots WHERE namespace=? AND version=?`, namespace, version).Scan(
		&snapshot.Namespace,
		&snapshot.Version,
		&baseVersion,
		&snapshot.SchemaVersion,
		&snapshot.ContentHash,
		&snapshot.NodeCount,
		&snapshot.EdgeCount,
		&snapshot.TaskID,
		&snapshot.Status,
		&queryReady,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return graphsnapshot.Snapshot{}, false, nil
	}
	if err != nil {
		return graphsnapshot.Snapshot{}, false, fmt.Errorf("lookup graph snapshot: %w", err)
	}
	if baseVersion.Valid {
		value := baseVersion.String
		snapshot.BaseVersion = &value
	}
	snapshot.QueryReady = queryReady == 1
	rows, err := s.db.QueryContext(ctx, `SELECT component,state,generation,error_json,warning FROM graph_snapshot_components WHERE namespace=? AND version=? ORDER BY CASE component WHEN 'graph' THEN 0 WHEN 'fts' THEN 1 WHEN 'vector' THEN 2 ELSE 3 END`, namespace, version)
	if err != nil {
		return graphsnapshot.Snapshot{}, false, fmt.Errorf("lookup graph components: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var component graphsnapshot.Component
		var generation sql.NullString
		var errorJSON sql.NullString
		var warning sql.NullString
		if err := rows.Scan(&component.Name, &component.State, &generation, &errorJSON, &warning); err != nil {
			return graphsnapshot.Snapshot{}, false, fmt.Errorf("scan graph component: %w", err)
		}
		if generation.Valid {
			component.Generation = generation.String
		}
		if errorJSON.Valid {
			var graphError graphsnapshot.Error
			if err := json.Unmarshal([]byte(errorJSON.String), &graphError); err != nil {
				return graphsnapshot.Snapshot{}, false, fmt.Errorf("decode graph component error: %w", err)
			}
			component.Error = &graphError
		}
		if warning.Valid && warning.String != "" {
			snapshot.Warnings = append(snapshot.Warnings, warning.String)
		}
		snapshot.Components = append(snapshot.Components, component)
	}
	if err := rows.Err(); err != nil {
		return graphsnapshot.Snapshot{}, false, fmt.Errorf("iterate graph components: %w", err)
	}
	return snapshot, true, nil
}

func (s *Store) ReadGraphSnapshotBase(ctx context.Context, namespace, version string) (graphsnapshot.SnapshotBase, bool, error) {
	record, err := s.ReadGraphSnapshot(ctx, namespace, version)
	if errors.Is(err, ErrGraphSnapshotNotFound) {
		return graphsnapshot.SnapshotBase{}, false, nil
	}
	if err != nil {
		return graphsnapshot.SnapshotBase{}, false, err
	}
	return graphsnapshot.SnapshotBase{Status: record.Status, Manifest: graphsnapshot.Manifest{SchemaVersion: record.SchemaVersion, Nodes: record.Nodes, Edges: record.Edges}}, true, nil
}

// AcceptGraphSnapshot persists only an already normalized, hash-verified
// manifest. The graph/FTS/vector rows remain private staging work until the
// durable worker promotes them in a later lifecycle step.
func (s *Store) AcceptGraphSnapshot(ctx context.Context, accepted graphsnapshot.AcceptedSnapshot) (graphsnapshot.Snapshot, error) {
	if err := s.GraphUnavailable(); err != nil {
		return graphsnapshot.Snapshot{}, err
	}
	canonical, hash, err := graphsnapshot.CanonicalHash(accepted.Manifest.Nodes, accepted.Manifest.Edges)
	if err != nil {
		return graphsnapshot.Snapshot{}, fmt.Errorf("canonical graph snapshot: %w", err)
	}
	if hash != accepted.ContentHash {
		return graphsnapshot.Snapshot{}, fmt.Errorf("graph snapshot hash changed before acceptance")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return graphsnapshot.Snapshot{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO graph_namespaces(namespace,created_at) VALUES(?,?) ON CONFLICT(namespace) DO NOTHING`, accepted.Namespace, now); err != nil {
		return graphsnapshot.Snapshot{}, fmt.Errorf("create graph namespace: %w", err)
	}
	var baseVersion any
	if accepted.BaseVersion != "" {
		baseVersion = accepted.BaseVersion
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO graph_snapshots(namespace,version,base_version,schema_version,content_hash,node_count,edge_count,task_id,status,query_ready,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,'building',0,?,?)`, accepted.Namespace, accepted.Version, baseVersion, graphsnapshot.SchemaVersionV1, accepted.ContentHash, len(accepted.Manifest.Nodes), len(accepted.Manifest.Edges), accepted.TaskID, now, now); err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed: graph_snapshots.namespace, graph_snapshots.version") {
			return graphsnapshot.Snapshot{}, fmt.Errorf("%w: %v", graphsnapshot.ErrSnapshotAlreadyAccepted, err)
		}
		return graphsnapshot.Snapshot{}, fmt.Errorf("create graph snapshot: %w", err)
	}
	for _, component := range []graphsnapshot.ComponentName{graphsnapshot.ComponentGraph, graphsnapshot.ComponentFTS, graphsnapshot.ComponentVector} {
		if _, err = tx.ExecContext(ctx, `INSERT INTO graph_snapshot_components(namespace,version,component,state) VALUES(?,?,?,'pending')`, accepted.Namespace, accepted.Version, component); err != nil {
			return graphsnapshot.Snapshot{}, fmt.Errorf("create graph component: %w", err)
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO graph_snapshot_staging(namespace,version,manifest_json) VALUES(?,?,?)`, accepted.Namespace, accepted.Version, string(canonical)); err != nil {
		return graphsnapshot.Snapshot{}, fmt.Errorf("create graph staging: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO graph_tasks(id,namespace,version,submission_request_id,state,phase,progress,created_at) VALUES(?,?,?,?,'queued','queued',0,?)`, accepted.TaskID, accepted.Namespace, accepted.Version, nullableGraphRequestID(accepted.SubmissionRequestID), now); err != nil {
		return graphsnapshot.Snapshot{}, fmt.Errorf("create graph task: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return graphsnapshot.Snapshot{}, fmt.Errorf("commit graph acceptance: %w", err)
	}
	return graphsnapshot.Snapshot{Namespace: accepted.Namespace, Version: accepted.Version, BaseVersion: stringPointer(accepted.BaseVersion), SchemaVersion: graphsnapshot.SchemaVersionV1, ContentHash: accepted.ContentHash, NodeCount: len(accepted.Manifest.Nodes), EdgeCount: len(accepted.Manifest.Edges), TaskID: accepted.TaskID, Status: graphsnapshot.SnapshotBuilding, Components: []graphsnapshot.Component{{Name: graphsnapshot.ComponentGraph, State: graphsnapshot.ComponentPending}, {Name: graphsnapshot.ComponentFTS, State: graphsnapshot.ComponentPending}, {Name: graphsnapshot.ComponentVector, State: graphsnapshot.ComponentPending}}}, nil
}

func nullableGraphRequestID(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func stringPointer(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

// ReadGraphSnapshot obtains a stable read transaction and loads the snapshot,
// nodes, and edges with namespace and version predicates on every query.
// Identical entity IDs in another project or version therefore cannot enter
// this record even while other graph lifecycle work is writing to SQLite.
func (s *Store) ReadGraphSnapshot(ctx context.Context, namespace, version string) (GraphSnapshotRecord, error) {
	if namespace == "" || version == "" {
		return GraphSnapshotRecord{}, ErrInvalidGraphIdentity
	}
	if err := s.GraphUnavailable(); err != nil {
		return GraphSnapshotRecord{}, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return GraphSnapshotRecord{}, fmt.Errorf("begin graph snapshot read: %w", err)
	}
	defer tx.Rollback()

	var record GraphSnapshotRecord
	var baseVersion sql.NullString
	var queryReady int
	err = tx.QueryRowContext(ctx, `SELECT namespace,version,base_version,schema_version,content_hash,node_count,edge_count,task_id,status,query_ready FROM graph_snapshots WHERE namespace=? AND version=?`, namespace, version).Scan(
		&record.Namespace,
		&record.Version,
		&baseVersion,
		&record.SchemaVersion,
		&record.ContentHash,
		&record.NodeCount,
		&record.EdgeCount,
		&record.TaskID,
		&record.Status,
		&queryReady,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return GraphSnapshotRecord{}, ErrGraphSnapshotNotFound
	}
	if err != nil {
		return GraphSnapshotRecord{}, fmt.Errorf("read graph snapshot: %w", err)
	}
	if baseVersion.Valid {
		value := baseVersion.String
		record.BaseVersion = &value
	}
	record.QueryReady = queryReady == 1

	nodes, err := tx.QueryContext(ctx, `SELECT node_id,node_type,label,text,properties_json,provenance_json FROM graph_nodes WHERE namespace=? AND version=? ORDER BY node_id`, namespace, version)
	if err != nil {
		return GraphSnapshotRecord{}, fmt.Errorf("read graph nodes: %w", err)
	}
	for nodes.Next() {
		var node graphsnapshot.Node
		var properties, provenance []byte
		if err := nodes.Scan(&node.ID, &node.Type, &node.Label, &node.Text, &properties, &provenance); err != nil {
			nodes.Close()
			return GraphSnapshotRecord{}, fmt.Errorf("scan graph node: %w", err)
		}
		node.Properties = append(json.RawMessage(nil), properties...)
		node.Provenance = append(json.RawMessage(nil), provenance...)
		record.Nodes = append(record.Nodes, node)
	}
	if err := nodes.Err(); err != nil {
		nodes.Close()
		return GraphSnapshotRecord{}, fmt.Errorf("iterate graph nodes: %w", err)
	}
	if err := nodes.Close(); err != nil {
		return GraphSnapshotRecord{}, fmt.Errorf("close graph nodes: %w", err)
	}

	edges, err := tx.QueryContext(ctx, `SELECT edge_id,from_node_id,to_node_id,edge_type,relation_kind,confidence,properties_json,provenance_json FROM graph_edges WHERE namespace=? AND version=? ORDER BY edge_id`, namespace, version)
	if err != nil {
		return GraphSnapshotRecord{}, fmt.Errorf("read graph edges: %w", err)
	}
	for edges.Next() {
		var edge graphsnapshot.Edge
		var confidence string
		var properties, provenance []byte
		if err := edges.Scan(&edge.ID, &edge.From, &edge.To, &edge.Type, &edge.RelationKind, &confidence, &properties, &provenance); err != nil {
			edges.Close()
			return GraphSnapshotRecord{}, fmt.Errorf("scan graph edge: %w", err)
		}
		edge.Confidence = json.Number(confidence)
		edge.Properties = append(json.RawMessage(nil), properties...)
		edge.Provenance = append(json.RawMessage(nil), provenance...)
		record.Edges = append(record.Edges, edge)
	}
	if err := edges.Err(); err != nil {
		edges.Close()
		return GraphSnapshotRecord{}, fmt.Errorf("iterate graph edges: %w", err)
	}
	if err := edges.Close(); err != nil {
		return GraphSnapshotRecord{}, fmt.Errorf("close graph edges: %w", err)
	}
	generations, err := tx.QueryContext(ctx, `SELECT component,generation,state,selected,algorithm,COALESCE(provider,''),COALESCE(model,''),COALESCE(dimensions,0),COALESCE(tokenizer,''),content_digest FROM graph_retrieval_generations WHERE namespace=? AND version=? ORDER BY component,generation`, namespace, version)
	if err != nil {
		return GraphSnapshotRecord{}, fmt.Errorf("read graph generations: %w", err)
	}
	defer generations.Close()
	for generations.Next() {
		var generation GraphGenerationRecord
		var selected int
		if err := generations.Scan(&generation.Component, &generation.Generation, &generation.State, &selected, &generation.Algorithm, &generation.Provider, &generation.Model, &generation.Dimensions, &generation.Tokenizer, &generation.ContentDigest); err != nil {
			return GraphSnapshotRecord{}, fmt.Errorf("scan graph generation: %w", err)
		}
		generation.Selected = selected == 1
		record.Generations = append(record.Generations, generation)
	}
	if err := generations.Err(); err != nil {
		return GraphSnapshotRecord{}, fmt.Errorf("iterate graph generations: %w", err)
	}
	return record, nil
}
