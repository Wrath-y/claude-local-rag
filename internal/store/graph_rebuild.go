package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Wrath-y/local-rag/internal/graphsnapshot"
	"github.com/Wrath-y/local-rag/internal/observe"
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
	observe.GraphTaskTransitions.WithLabelValues("snapshot_rebuild", string(graphsnapshot.TaskQueued)).Inc()
	s.observeGraphTaskQueue(ctx)
	observe.GraphEvent("task_accepted", "snapshot_rebuild", "", taskID, submissionRequestID, "")
	return graphsnapshot.Task{ID: taskID, Namespace: namespace, Version: version, State: graphsnapshot.TaskQueued, Phase: "queued"}, false, nil
}

// ReadTrustedGraphRebuildSource materializes exactly one immutable snapshot
// and verifies its canonical lifecycle identity before any derived component
// can be built. It intentionally has no dependency on legacy chunks,
// activation, retrieval submission, or remote source systems.
func (s *Store) ReadTrustedGraphRebuildSource(ctx context.Context, namespace, version string) (GraphSnapshotRecord, error) {
	record, err := s.ReadGraphSnapshot(ctx, namespace, version)
	if errors.Is(err, ErrGraphSnapshotNotFound) {
		return GraphSnapshotRecord{}, operability.ErrSnapshotNotFound
	}
	if err != nil {
		return GraphSnapshotRecord{}, fmt.Errorf("read rebuild source: %w", err)
	}
	if len(record.Nodes) != record.NodeCount || len(record.Edges) != record.EdgeCount {
		return GraphSnapshotRecord{}, operability.ErrReimportRequired
	}
	nodes := make(map[string]struct{}, len(record.Nodes))
	for _, node := range record.Nodes {
		nodes[node.ID] = struct{}{}
	}
	for _, edge := range record.Edges {
		if _, ok := nodes[edge.From]; !ok {
			return GraphSnapshotRecord{}, operability.ErrReimportRequired
		}
		if _, ok := nodes[edge.To]; !ok {
			return GraphSnapshotRecord{}, operability.ErrReimportRequired
		}
	}
	_, hash, err := graphsnapshot.CanonicalHash(record.Nodes, record.Edges)
	if err != nil || hash != record.ContentHash {
		return GraphSnapshotRecord{}, operability.ErrReimportRequired
	}
	return record, nil
}

// BuildPrivateGraphIndexes derives a task-owned adjacency generation from a
// verified immutable source. The generation remains private until a later
// all-components promotion transaction selects it.
func (s *Store) BuildPrivateGraphIndexes(ctx context.Context, taskID string) (string, error) {
	var namespace, version, operation string
	if err := s.db.QueryRowContext(ctx, `SELECT namespace,version,operation FROM graph_tasks WHERE id=?`, taskID).Scan(&namespace, &version, &operation); err != nil {
		return "", err
	}
	if operation != "snapshot_rebuild" {
		return "", fmt.Errorf("graph-index rebuild requires snapshot_rebuild task")
	}
	record, err := s.ReadTrustedGraphRebuildSource(ctx, namespace, version)
	if err != nil {
		return "", err
	}
	generation := "graph-indexes-" + taskID
	digest := graphIndexDigest(record.Edges)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM graph_index_adjacency WHERE namespace=? AND version=? AND generation=?`, namespace, version, generation); err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM graph_retrieval_generations WHERE namespace=? AND version=? AND component='graph_indexes' AND generation=? AND selected=0`, namespace, version, generation); err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO graph_retrieval_generations(namespace,version,component,generation,state,selected,algorithm,content_digest,created_at) VALUES(?,?, 'graph_indexes',?,'private',0,'edge-adjacency-v1',?,?)`, namespace, version, generation, digest, now); err != nil {
		return "", err
	}
	for _, edge := range record.Edges {
		for _, item := range []struct{ direction, nodeID string }{{"outgoing", edge.From}, {"incoming", edge.To}} {
			if _, err = tx.ExecContext(ctx, `INSERT INTO graph_index_adjacency(namespace,version,generation,direction,node_id,edge_id,relation_kind,edge_type) VALUES(?,?,?,?,?,?,?,?)`, namespace, version, generation, item.direction, item.nodeID, edge.ID, edge.RelationKind, edge.Type); err != nil {
				return "", err
			}
		}
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM graph_index_adjacency WHERE namespace=? AND version=? AND generation=?`, namespace, version, generation).Scan(&count); err != nil {
		return "", err
	}
	if count != len(record.Edges)*2 {
		return "", fmt.Errorf("graph-index adjacency coverage mismatch")
	}
	if err = s.graphRebuildBoundary("before_validation_" + string(operability.ComponentGraphIndexes)); err != nil {
		return "", err
	}
	if err = validatePrivateGraphRebuildGeneration(ctx, tx, record, operability.ComponentGraphIndexes, generation, digest, nil); err != nil {
		return "", err
	}
	if err = s.graphRebuildBoundary("after_validation_" + string(operability.ComponentGraphIndexes)); err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE graph_task_steps SET state='validated',generation=?,content_digest=?,updated_at=? WHERE task_id=? AND component='graph_indexes' AND state IN ('pending','building','validated')`, generation, digest, now, taskID); err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return generation, nil
}

func (s *Store) BuildPrivateGraphFTS(ctx context.Context, taskID string) (string, error) {
	var namespace, version, operation string
	if err := s.db.QueryRowContext(ctx, `SELECT namespace,version,operation FROM graph_tasks WHERE id=?`, taskID).Scan(&namespace, &version, &operation); err != nil {
		return "", err
	}
	if operation != "snapshot_rebuild" {
		return "", fmt.Errorf("FTS rebuild requires snapshot_rebuild task")
	}
	record, err := s.ReadTrustedGraphRebuildSource(ctx, namespace, version)
	if err != nil {
		return "", err
	}
	generation := "fts-" + taskID
	digestParts := []string{"fts5", graphsnapshot.SearchDocumentFormatV1, generation}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM graph_search_documents WHERE namespace=? AND version=? AND generation=?`, namespace, version, generation); err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM graph_retrieval_generations WHERE namespace=? AND version=? AND component='fts' AND generation=? AND selected=0`, namespace, version, generation); err != nil {
		return "", err
	}
	for _, node := range record.Nodes {
		text, e := graphsnapshot.SearchDocument(&node, nil)
		if e != nil {
			return "", e
		}
		digestParts = append(digestParts, "node", node.ID, text)
		if _, err = tx.ExecContext(ctx, `INSERT INTO graph_search_documents(namespace,version,generation,entity_kind,entity_id,search_text) VALUES(?,?,?, 'node',?,?)`, namespace, version, generation, node.ID, text); err != nil {
			return "", err
		}
	}
	for _, edge := range record.Edges {
		text, e := graphsnapshot.SearchDocument(nil, &edge)
		if e != nil {
			return "", e
		}
		digestParts = append(digestParts, "edge", edge.ID, text)
		if _, err = tx.ExecContext(ctx, `INSERT INTO graph_search_documents(namespace,version,generation,entity_kind,entity_id,search_text) VALUES(?,?,?, 'edge',?,?)`, namespace, version, generation, edge.ID, text); err != nil {
			return "", err
		}
	}
	digest := graphDerivedDigest(digestParts...)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err = tx.ExecContext(ctx, `INSERT INTO graph_retrieval_generations(namespace,version,component,generation,state,selected,algorithm,tokenizer,content_digest,created_at) VALUES(?,?, 'fts',?,'private',0,?,'unicode61',?,?)`, namespace, version, generation, graphsnapshot.SearchDocumentFormatV1+"/fts5", digest, now); err != nil {
		return "", err
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM graph_search_documents WHERE namespace=? AND version=? AND generation=?`, namespace, version, generation).Scan(&count); err != nil {
		return "", err
	}
	if count != len(record.Nodes)+len(record.Edges) {
		return "", fmt.Errorf("private FTS coverage mismatch")
	}
	if err = s.graphRebuildBoundary("before_validation_" + string(operability.ComponentFTS)); err != nil {
		return "", err
	}
	if err = validatePrivateGraphRebuildGeneration(ctx, tx, record, operability.ComponentFTS, generation, digest, nil); err != nil {
		return "", err
	}
	if err = s.graphRebuildBoundary("after_validation_" + string(operability.ComponentFTS)); err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE graph_task_steps SET state='validated',generation=?,content_digest=?,updated_at=? WHERE task_id=? AND component='fts' AND state IN ('pending','building','validated')`, generation, digest, now, taskID); err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return generation, nil
}

func (s *Store) BuildPrivateGraphVectors(ctx context.Context, taskID string, embedder graphsnapshot.Embedder) (string, error) {
	if embedder == nil {
		return "", fmt.Errorf("graph vector provider is unavailable")
	}
	var namespace, version, operation string
	if err := s.db.QueryRowContext(ctx, `SELECT namespace,version,operation FROM graph_tasks WHERE id=?`, taskID).Scan(&namespace, &version, &operation); err != nil {
		return "", err
	}
	if operation != "snapshot_rebuild" {
		return "", fmt.Errorf("vector rebuild requires snapshot_rebuild task")
	}
	record, err := s.ReadTrustedGraphRebuildSource(ctx, namespace, version)
	if err != nil {
		return "", err
	}
	inputs := make([]graphSearchInput, 0, len(record.Nodes)+len(record.Edges))
	for _, node := range record.Nodes {
		text, e := graphsnapshot.SearchDocument(&node, nil)
		if e != nil {
			return "", e
		}
		inputs = append(inputs, graphSearchInput{"node", node.ID, text})
	}
	for _, edge := range record.Edges {
		text, e := graphsnapshot.SearchDocument(nil, &edge)
		if e != nil {
			return "", e
		}
		inputs = append(inputs, graphSearchInput{"edge", edge.ID, text})
	}
	identity := graphsnapshot.EmbeddingIdentity{Algorithm: graphsnapshot.SearchDocumentFormatV1 + "/embedding", Dimensions: s.dims}
	if identified, ok := embedder.(graphsnapshot.IdentifiedEmbedder); ok {
		identity = identified.EmbeddingIdentity()
	}
	if identity.Algorithm == "" {
		identity.Algorithm = graphsnapshot.SearchDocumentFormatV1 + "/embedding"
	}
	if identity.Dimensions == 0 {
		identity.Dimensions = s.dims
	}
	var provider, model sql.NullString
	var dimensions sql.NullInt64
	if err = s.db.QueryRowContext(ctx, `SELECT provider,model,dimensions FROM graph_retrieval_generations WHERE namespace=? AND version=? AND component='vector' AND selected=1`, namespace, version).Scan(&provider, &model, &dimensions); err == nil {
		if (provider.Valid && provider.String != "" && provider.String != identity.Provider) || (model.Valid && model.String != "" && model.String != identity.Model) || (dimensions.Valid && int(dimensions.Int64) > 0 && int(dimensions.Int64) != identity.Dimensions) {
			return "", fmt.Errorf("recorded vector identity is incompatible")
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	vectors := make([][]float32, 0, len(inputs))
	texts := make([]string, len(inputs))
	for i := range inputs {
		texts[i] = inputs[i].text
	}
	for start := 0; start < len(texts); start += graphEmbeddingBatchSize {
		end := start + graphEmbeddingBatchSize
		if end > len(texts) {
			end = len(texts)
		}
		batch, e := embedder.Embed(ctx, texts[start:end])
		if e != nil {
			return "", e
		}
		if len(batch) != end-start {
			return "", fmt.Errorf("private vector coverage mismatch")
		}
		for _, vector := range batch {
			if len(vector) != identity.Dimensions {
				return "", fmt.Errorf("private vector dimension mismatch")
			}
		}
		vectors = append(vectors, batch...)
	}
	generation := "vector-" + taskID
	digestParts := []string{identity.Algorithm, identity.Provider, identity.Model, generation}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM graph_vector_items WHERE namespace=? AND version=? AND generation=?`, namespace, version, generation); err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM graph_retrieval_generations WHERE namespace=? AND version=? AND component='vector' AND generation=? AND selected=0`, namespace, version, generation); err != nil {
		return "", err
	}
	for i, input := range inputs {
		result, e := tx.ExecContext(ctx, `INSERT INTO graph_vector_items(namespace,version,generation,entity_kind,entity_id,dimensions) VALUES(?,?,?,?,?,?)`, namespace, version, generation, input.kind, input.id, identity.Dimensions)
		if e != nil {
			return "", e
		}
		id, e := result.LastInsertId()
		if e != nil {
			return "", e
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO graph_vectors(item_id,embedding) VALUES(?,?)`, id, Float32ToBytes(vectors[i])); e != nil {
			return "", e
		}
		digestParts = append(digestParts, input.kind, input.id, string(Float32ToBytes(vectors[i])))
	}
	digest := graphDerivedDigest(digestParts...)
	if _, err = tx.ExecContext(ctx, `INSERT INTO graph_retrieval_generations(namespace,version,component,generation,state,selected,algorithm,provider,model,dimensions,content_digest,created_at) VALUES(?,?, 'vector',?,'private',0,?,?,?,?,?,?)`, namespace, version, generation, identity.Algorithm, identity.Provider, identity.Model, identity.Dimensions, digest, now); err != nil {
		return "", err
	}
	if err = s.graphRebuildBoundary("before_validation_" + string(operability.ComponentVector)); err != nil {
		return "", err
	}
	if err = validatePrivateGraphRebuildGeneration(ctx, tx, record, operability.ComponentVector, generation, digest, &identity); err != nil {
		return "", err
	}
	if err = s.graphRebuildBoundary("after_validation_" + string(operability.ComponentVector)); err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE graph_task_steps SET state='validated',generation=?,content_digest=?,updated_at=? WHERE task_id=? AND component='vector' AND state IN ('pending','building','validated')`, generation, digest, now, taskID); err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return generation, nil
}

// ProcessGraphRebuild dispatches one durable rebuild at component boundaries.
// Graph-index-only work is fully supported here; multi-component tasks retain
// their queued/failed durable contract until their builders are added.
func (s *Store) ProcessGraphRebuild(ctx context.Context, task graphsnapshot.Task, embedder graphsnapshot.Embedder) error {
	var componentsJSON string
	if err := s.db.QueryRowContext(ctx, `SELECT requested_components_json FROM graph_tasks WHERE id=? AND state='running' AND operation='snapshot_rebuild'`, task.ID).Scan(&componentsJSON); err != nil {
		return err
	}
	var components []operability.Component
	if err := json.Unmarshal([]byte(componentsJSON), &components); err != nil {
		return err
	}
	for index, component := range components {
		progress := 3000 + (5000*(index+1))/len(components)
		started := time.Now()
		if err := s.graphRebuildBoundary("before_build_" + string(component)); err != nil {
			return s.failGraphRebuild(ctx, task.ID, rebuildGraphError(err))
		}
		switch component {
		case operability.ComponentGraphIndexes:
			if _, err := s.AdvanceGraphTaskProgress(ctx, task.ID, "building_graph_indexes", progress); err != nil {
				return err
			}
			if _, err := s.BuildPrivateGraphIndexes(ctx, task.ID); err != nil {
				observe.GraphRebuildComponentOutcomes.WithLabelValues(string(component), "failed").Inc()
				observe.GraphRebuildComponentDuration.WithLabelValues(string(component), "failed").Observe(time.Since(started).Seconds())
				return s.failGraphRebuild(ctx, task.ID, rebuildGraphError(err))
			}
		case operability.ComponentFTS:
			if _, err := s.AdvanceGraphTaskProgress(ctx, task.ID, "building_fts", progress); err != nil {
				return err
			}
			if _, err := s.BuildPrivateGraphFTS(ctx, task.ID); err != nil {
				observe.GraphRebuildComponentOutcomes.WithLabelValues(string(component), "failed").Inc()
				observe.GraphRebuildComponentDuration.WithLabelValues(string(component), "failed").Observe(time.Since(started).Seconds())
				return s.failGraphRebuild(ctx, task.ID, rebuildGraphError(err))
			}
		case operability.ComponentVector:
			if _, err := s.AdvanceGraphTaskProgress(ctx, task.ID, "building_vector", progress); err != nil {
				return err
			}
			if _, err := s.BuildPrivateGraphVectors(ctx, task.ID, embedder); err != nil {
				observe.GraphRebuildComponentOutcomes.WithLabelValues(string(component), "failed").Inc()
				observe.GraphRebuildComponentDuration.WithLabelValues(string(component), "failed").Observe(time.Since(started).Seconds())
				return s.failGraphRebuild(ctx, task.ID, rebuildGraphError(err))
			}
		default:
			return s.failGraphRebuild(ctx, task.ID, graphsnapshot.NewError(graphsnapshot.CodeInternalError, map[string]any{"reason": "COMPONENT_NOT_IMPLEMENTED"}, nil))
		}
		if err := s.graphRebuildBoundary("after_build_" + string(component)); err != nil {
			return s.failGraphRebuild(ctx, task.ID, rebuildGraphError(err))
		}
		observe.GraphEvent("rebuild_component_complete", "snapshot_rebuild", string(component), task.ID, task.SubmissionRequestID, "")
		observe.GraphRebuildComponentOutcomes.WithLabelValues(string(component), "succeeded").Inc()
		observe.GraphRebuildComponentDuration.WithLabelValues(string(component), "succeeded").Observe(time.Since(started).Seconds())
	}
	if err := s.graphRebuildBoundary("before_promotion"); err != nil {
		return s.failGraphRebuild(ctx, task.ID, rebuildGraphError(err))
	}
	err := s.promoteGraphIndexRebuild(ctx, task.ID)
	if err == nil {
		err = s.graphRebuildBoundary("after_promotion")
	}
	return err
}

func (s *Store) graphRebuildBoundary(name string) error {
	if s.graphRebuildFailpoint == nil {
		return nil
	}
	return s.graphRebuildFailpoint(name)
}

func (s *Store) promoteGraphIndexRebuild(ctx context.Context, taskID string) error {
	// Read and verify immutable source before opening the short selector-write
	// transaction. The transaction below rechecks its hash, so this never
	// promotes a generation built from a racing or untrusted source.
	var sourceNamespace, sourceVersion string
	if err := s.db.QueryRowContext(ctx, `SELECT namespace,version FROM graph_tasks WHERE id=? AND state='running'`, taskID).Scan(&sourceNamespace, &sourceVersion); err != nil {
		return err
	}
	record, err := s.ReadTrustedGraphRebuildSource(ctx, sourceNamespace, sourceVersion)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var namespace, version, sourceHash, componentsJSON string
	if err = tx.QueryRowContext(ctx, `SELECT namespace,version,source_hash,requested_components_json FROM graph_tasks WHERE id=? AND state='running'`, taskID).Scan(&namespace, &version, &sourceHash, &componentsJSON); err != nil {
		return err
	}
	var requested []operability.Component
	if err = json.Unmarshal([]byte(componentsJSON), &requested); err != nil {
		return fmt.Errorf("decode requested rebuild components: %w", err)
	}
	requested, err = operability.NormalizeComponents(requested)
	if err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT component,generation,content_digest FROM graph_task_steps WHERE task_id=? AND state='validated' ORDER BY component`, taskID)
	if err != nil {
		return err
	}
	defer rows.Close()
	var generations []map[string]string
	for rows.Next() {
		var component, generation, digest string
		if err = rows.Scan(&component, &generation, &digest); err != nil {
			return err
		}
		generations = append(generations, map[string]string{"component": component, "generation": generation, "content_digest": digest})
	}
	if err = rows.Err(); err != nil || len(generations) == 0 {
		if err != nil {
			return err
		}
		return fmt.Errorf("no validated rebuild generations")
	}
	if len(generations) != len(requested) {
		return fmt.Errorf("requested rebuild components are not all validated")
	}
	requestedSet := make(map[string]struct{}, len(requested))
	for _, component := range requested {
		requestedSet[string(component)] = struct{}{}
	}
	for _, item := range generations {
		if _, ok := requestedSet[item["component"]]; !ok || item["generation"] == "" || item["content_digest"] == "" {
			return fmt.Errorf("validated rebuild component does not match request")
		}
		var storedDigest string
		var selected int
		if err = tx.QueryRowContext(ctx, `SELECT content_digest,selected FROM graph_retrieval_generations WHERE namespace=? AND version=? AND component=? AND generation=? AND state='private'`, namespace, version, item["component"], item["generation"]).Scan(&storedDigest, &selected); err != nil || selected != 0 || storedDigest != item["content_digest"] {
			if err != nil {
				return fmt.Errorf("validate promotion generation: %w", err)
			}
			return fmt.Errorf("validate promotion generation identity mismatch")
		}
		component := operability.Component(item["component"])
		var vectorIdentity *graphsnapshot.EmbeddingIdentity
		if component == operability.ComponentVector {
			var identity graphsnapshot.EmbeddingIdentity
			if err = tx.QueryRowContext(ctx, `SELECT algorithm,COALESCE(provider,''),COALESCE(model,''),dimensions FROM graph_retrieval_generations WHERE namespace=? AND version=? AND component='vector' AND generation=?`, namespace, version, item["generation"]).Scan(&identity.Algorithm, &identity.Provider, &identity.Model, &identity.Dimensions); err != nil {
				return fmt.Errorf("load vector promotion identity: %w", err)
			}
			vectorIdentity = &identity
		}
		if err = validatePrivateGraphRebuildGeneration(ctx, tx, record, component, item["generation"], item["content_digest"], vectorIdentity); err != nil {
			return err
		}
	}
	var currentHash string
	if err = tx.QueryRowContext(ctx, `SELECT content_hash FROM graph_snapshots WHERE namespace=? AND version=?`, namespace, version).Scan(&currentHash); err != nil || currentHash != sourceHash {
		return operability.ErrReimportRequired
	}
	for _, item := range generations {
		if _, err = tx.ExecContext(ctx, `UPDATE graph_retrieval_generations SET selected=0,state='evicted' WHERE namespace=? AND version=? AND component=? AND selected=1`, namespace, version, item["component"]); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE graph_retrieval_generations SET selected=1,state='selected' WHERE namespace=? AND version=? AND component=? AND generation=? AND state='private'`, namespace, version, item["component"], item["generation"]); err != nil {
			return err
		}
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, _ := json.Marshal(map[string]any{"generations": generations})
	if _, err = tx.ExecContext(ctx, `UPDATE graph_task_steps SET state='selected',updated_at=? WHERE task_id=? AND state='validated'`, now, taskID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE graph_tasks SET state='succeeded',phase='completed',progress=10000,result_json=?,finished_at=?,updated_at=? WHERE id=? AND state='running'`, string(result), now, now, taskID); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	observe.GraphTaskTransitions.WithLabelValues("snapshot_rebuild", string(graphsnapshot.TaskSucceeded)).Inc()
	s.observeGraphTaskTerminal(ctx, taskID, graphsnapshot.TaskSucceeded)
	observe.GraphEvent("rebuild_promoted", "snapshot_rebuild", "", taskID, "", "")
	observe.GraphEvent("task_terminal", "snapshot_rebuild", "", taskID, "", "SUCCEEDED")
	return nil
}

func (s *Store) failGraphRebuild(ctx context.Context, taskID string, graphErr *graphsnapshot.Error) error {
	var requestID sql.NullString
	if err := s.db.QueryRowContext(ctx, `SELECT submission_request_id FROM graph_tasks WHERE id=?`, taskID).Scan(&requestID); err == nil && requestID.Valid {
		graphErr.WithRequestID(requestID.String)
	}
	payload, err := json.Marshal(graphErr)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = s.db.ExecContext(ctx, `UPDATE graph_tasks SET state='failed',phase='completed',error_json=?,finished_at=?,updated_at=? WHERE id=? AND state='running'`, string(payload), now, now, taskID)
	if err == nil {
		observe.GraphTaskTransitions.WithLabelValues("snapshot_rebuild", string(graphsnapshot.TaskFailed)).Inc()
		s.observeGraphTaskTerminal(ctx, taskID, graphsnapshot.TaskFailed)
		observe.GraphEvent("task_terminal", "snapshot_rebuild", "", taskID, requestID.String, string(graphErr.Code))
	}
	return err
}

func (s *Store) observeGraphTaskTerminal(ctx context.Context, taskID string, state graphsnapshot.TaskState) {
	var operation, created string
	if err := s.db.QueryRowContext(ctx, `SELECT operation,created_at FROM graph_tasks WHERE id=?`, taskID).Scan(&operation, &created); err != nil {
		return
	}
	createdAt, err := time.Parse(time.RFC3339Nano, created)
	if err == nil {
		observe.GraphTaskDuration.WithLabelValues(operation, string(state)).Observe(time.Since(createdAt).Seconds())
	}
}

func rebuildGraphError(err error) *graphsnapshot.Error {
	if errors.Is(err, operability.ErrReimportRequired) {
		return graphsnapshot.NewError(graphsnapshot.CodeReimportRequired, nil, nil)
	}
	if errors.Is(err, operability.ErrSnapshotNotFound) {
		return graphsnapshot.NewError(graphsnapshot.CodeReimportRequired, nil, nil)
	}
	return graphsnapshot.NewError(graphsnapshot.CodeInternalError, nil, err)
}
