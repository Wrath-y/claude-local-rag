package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Wrath-y/local-rag/internal/graphquery"
	"github.com/Wrath-y/local-rag/internal/graphretrieval"
	"github.com/Wrath-y/local-rag/internal/graphsnapshot"
)

// WithRetrievalRead fixes snapshot selection and both selected index
// generations in one read transaction. WAL readers therefore observe either
// the complete pre-cleanup generation or the complete post-cleanup state.
func (s *Store) WithRetrievalRead(ctx context.Context, namespace, requestedVersion string, fn func(graphretrieval.ReadView) error) error {
	if err := s.GraphUnavailable(); err != nil {
		return fmt.Errorf("%w: %v", graphquery.ErrStoreUnavailable, err)
	}
	if namespace == "" {
		return ErrInvalidGraphIdentity
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return fmt.Errorf("%w: begin graph retrieval read: %v", graphquery.ErrStoreUnavailable, err)
	}
	defer tx.Rollback()
	version, contentHash, err := resolveRetrievalSnapshot(ctx, tx, namespace, requestedVersion)
	if err != nil {
		return err
	}
	base := &graphReadView{tx: tx, namespace: namespace, version: version, contentHash: contentHash}
	view := &graphRetrievalReadView{graphReadView: base, generations: map[graphretrieval.Stage]retrievalGeneration{}}
	for _, item := range []struct {
		stage     graphretrieval.Stage
		component string
	}{{graphretrieval.StageBM25, "fts"}, {graphretrieval.StageVector, "vector"}} {
		generation, outcome, loadErr := loadRetrievalGeneration(ctx, tx, namespace, version, item.component)
		if loadErr != nil {
			return loadErr
		}
		view.generations[item.stage] = retrievalGeneration{identity: generation, outcome: outcome}
	}
	if err = fn(view); err != nil {
		return err
	}
	return tx.Commit()
}

func resolveRetrievalSnapshot(ctx context.Context, tx *sql.Tx, namespace, requestedVersion string) (string, string, error) {
	version := requestedVersion
	if version == "" {
		if err := tx.QueryRowContext(ctx, `SELECT active_version FROM graph_namespace_heads WHERE namespace=?`, namespace).Scan(&version); errors.Is(err, sql.ErrNoRows) {
			return "", "", graphquery.ErrNoActiveSnapshot
		} else if err != nil {
			return "", "", fmt.Errorf("resolve active graph snapshot: %w", err)
		}
	}
	var status graphsnapshot.SnapshotStatus
	var ready int
	var contentHash string
	err := tx.QueryRowContext(ctx, `SELECT status,query_ready,content_hash FROM graph_snapshots WHERE namespace=? AND version=?`, namespace, version).Scan(&status, &ready, &contentHash)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", graphquery.ErrSnapshotNotFound
	}
	if err != nil {
		return "", "", fmt.Errorf("resolve graph snapshot: %w", err)
	}
	if status != graphsnapshot.SnapshotReady || ready != 1 {
		return "", "", graphquery.ErrSnapshotNotReady
	}
	return version, contentHash, nil
}

type retrievalGeneration struct {
	identity *graphretrieval.GenerationIdentity
	outcome  graphretrieval.StageOutcome
}

func loadRetrievalGeneration(ctx context.Context, tx *sql.Tx, namespace, version, component string) (*graphretrieval.GenerationIdentity, graphretrieval.StageOutcome, error) {
	var identity graphretrieval.GenerationIdentity
	var provider, model, tokenizer sql.NullString
	var dimensions sql.NullInt64
	err := tx.QueryRowContext(ctx, `
SELECT generation,algorithm,provider,model,dimensions,tokenizer,content_digest
FROM graph_retrieval_generations
WHERE namespace=? AND version=? AND component=? AND selected=1 AND state='selected'`, namespace, version, component).Scan(
		&identity.Generation, &identity.Algorithm, &provider, &model, &dimensions, &tokenizer, &identity.ContentDigest)
	if err == nil {
		identity.Component = component
		identity.Provider, identity.Model, identity.Tokenizer = provider.String, model.String, tokenizer.String
		if dimensions.Valid {
			dimension := int(dimensions.Int64)
			identity.Dimensions = &dimension
		}
		return &identity, graphretrieval.StageUsed, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, graphretrieval.StageTransientFailure, fmt.Errorf("load %s retrieval generation: %w", component, err)
	}
	var state string
	err = tx.QueryRowContext(ctx, `
SELECT state FROM graph_retrieval_generations
WHERE namespace=? AND version=? AND component=?
ORDER BY created_at DESC,generation ASC LIMIT 1`, namespace, version, component).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, graphretrieval.StageUnavailable, nil
	}
	if err != nil {
		return nil, graphretrieval.StageTransientFailure, fmt.Errorf("load %s retrieval state: %w", component, err)
	}
	if state == "evicted" {
		return nil, graphretrieval.StageIndexEvicted, nil
	}
	return nil, graphretrieval.StageUnavailable, nil
}

type graphRetrievalReadView struct {
	*graphReadView
	generations map[graphretrieval.Stage]retrievalGeneration
}

func (v *graphRetrievalReadView) Generation(stage graphretrieval.Stage) (*graphretrieval.GenerationIdentity, graphretrieval.StageOutcome) {
	generation, ok := v.generations[stage]
	if !ok {
		return nil, graphretrieval.StageUnavailable
	}
	return generation.identity, generation.outcome
}

func (v *graphRetrievalReadView) BM25(ctx context.Context, query string, nodeTypes []string, limit int) ([]graphretrieval.Candidate, graphretrieval.StageOutcome, error) {
	identity, outcome := v.Generation(graphretrieval.StageBM25)
	if outcome != graphretrieval.StageUsed || identity == nil {
		return nil, outcome, nil
	}
	statement := `SELECT document.entity_id,document.search_text,bm25(graph_search_fts)
FROM graph_search_fts
JOIN graph_search_documents AS document ON document.id=graph_search_fts.rowid
JOIN graph_nodes AS node ON node.namespace=document.namespace AND node.version=document.version AND node.node_id=document.entity_id
WHERE graph_search_fts MATCH ? AND document.namespace=? AND document.version=? AND document.generation=? AND document.entity_kind='node'`
	args := []any{query, v.namespace, v.version, identity.Generation}
	statement, args = graphRetrievalNodeTypeFilter(statement, args, nodeTypes, "node.node_type")
	statement += ` ORDER BY bm25(graph_search_fts) ASC,document.entity_id ASC LIMIT ?`
	args = append(args, limit)
	rows, err := v.tx.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, graphretrieval.StageTransientFailure, fmt.Errorf("search graph BM25: %w", err)
	}
	defer rows.Close()
	result := []graphretrieval.Candidate{}
	for rows.Next() {
		var candidate graphretrieval.Candidate
		if err := rows.Scan(&candidate.NodeID, &candidate.SearchText, &candidate.RawScore); err != nil {
			return nil, graphretrieval.StageTransientFailure, fmt.Errorf("scan graph BM25: %w", err)
		}
		candidate.Rank = len(result) + 1
		result = append(result, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, graphretrieval.StageTransientFailure, fmt.Errorf("iterate graph BM25: %w", err)
	}
	return result, graphretrieval.StageUsed, nil
}

func (v *graphRetrievalReadView) Vector(ctx context.Context, vector []float32, nodeTypes []string, limit int) ([]graphretrieval.Candidate, graphretrieval.StageOutcome, error) {
	identity, outcome := v.Generation(graphretrieval.StageVector)
	if outcome != graphretrieval.StageUsed || identity == nil {
		return nil, outcome, nil
	}
	if identity.Dimensions == nil || *identity.Dimensions != len(vector) {
		return nil, graphretrieval.StagePermanentFailure, nil
	}
	statement := `SELECT item.entity_id,document.search_text,graph_vectors.distance
FROM graph_vectors
JOIN graph_vector_items AS item ON item.id=graph_vectors.item_id
JOIN graph_search_documents AS document ON document.namespace=item.namespace AND document.version=item.version AND document.entity_kind=item.entity_kind AND document.entity_id=item.entity_id
JOIN graph_nodes AS node ON node.namespace=item.namespace AND node.version=item.version AND node.node_id=item.entity_id
WHERE graph_vectors.embedding MATCH ? AND graph_vectors.k=? AND item.namespace=? AND item.version=? AND item.generation=? AND item.entity_kind='node' AND document.generation=(SELECT generation FROM graph_retrieval_generations WHERE namespace=item.namespace AND version=item.version AND component='fts' AND selected=1 AND state='selected')`
	args := []any{Float32ToBytes(vector), limit, v.namespace, v.version, identity.Generation}
	statement, args = graphRetrievalNodeTypeFilter(statement, args, nodeTypes, "node.node_type")
	statement += ` ORDER BY graph_vectors.distance ASC,item.entity_id ASC LIMIT ?`
	args = append(args, limit)
	rows, err := v.tx.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, graphretrieval.StageTransientFailure, fmt.Errorf("search graph vectors: %w", err)
	}
	defer rows.Close()
	result := []graphretrieval.Candidate{}
	for rows.Next() {
		var candidate graphretrieval.Candidate
		if err := rows.Scan(&candidate.NodeID, &candidate.SearchText, &candidate.RawScore); err != nil {
			return nil, graphretrieval.StageTransientFailure, fmt.Errorf("scan graph vectors: %w", err)
		}
		candidate.Rank = len(result) + 1
		result = append(result, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, graphretrieval.StageTransientFailure, fmt.Errorf("iterate graph vectors: %w", err)
	}
	return result, graphretrieval.StageUsed, nil
}

func graphRetrievalNodeTypeFilter(statement string, args []any, nodeTypes []string, column string) (string, []any) {
	if len(nodeTypes) == 0 {
		return statement, args
	}
	marks := make([]string, len(nodeTypes))
	for i, nodeType := range nodeTypes {
		marks[i] = "?"
		args = append(args, nodeType)
	}
	return statement + ` AND ` + column + ` IN (` + joinMarks(marks) + `)`, args
}
