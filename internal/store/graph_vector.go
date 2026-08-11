package store

import (
	"context"
	"fmt"

	"github.com/Wrath-y/local-rag/internal/graphsnapshot"
)

type graphSearchInput struct{ kind, id, text string }

const graphEmbeddingBatchSize = 128

// BuildGraphVectors writes one private generation only after embedding has
// completed outside SQLite. A provider failure therefore leaves no selected
// generation and no partial vector rows.
func (s *Store) BuildGraphVectors(ctx context.Context, taskID string, embedder graphsnapshot.Embedder) error {
	if embedder == nil {
		return fmt.Errorf("graph embedder is unavailable")
	}
	var namespace, version string
	if err := s.db.QueryRowContext(ctx, `SELECT namespace,version FROM graph_tasks WHERE id=?`, taskID).Scan(&namespace, &version); err != nil {
		return err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT entity_kind,entity_id,search_text FROM graph_search_documents WHERE namespace=? AND version=? ORDER BY entity_kind,entity_id`, namespace, version)
	if err != nil {
		return err
	}
	defer rows.Close()
	var inputs []graphSearchInput
	var texts []string
	for rows.Next() {
		var item graphSearchInput
		if err := rows.Scan(&item.kind, &item.id, &item.text); err != nil {
			return err
		}
		inputs = append(inputs, item)
		texts = append(texts, item.text)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	vectors := make([][]float32, 0, len(inputs))
	for start := 0; start < len(texts); start += graphEmbeddingBatchSize {
		end := start + graphEmbeddingBatchSize
		if end > len(texts) {
			end = len(texts)
		}
		batch, embedErr := embedder.Embed(ctx, texts[start:end])
		if embedErr != nil {
			return embedErr
		}
		if len(batch) != end-start {
			return fmt.Errorf("graph vector coverage mismatch")
		}
		for _, vector := range batch {
			if len(vector) != s.dims {
				return fmt.Errorf("graph vector dimension mismatch")
			}
		}
		vectors = append(vectors, batch...)
	}
	generation := "vector-" + taskID
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
	digestParts := []string{identity.Algorithm, identity.Provider, identity.Model, generation}
	for i := range inputs {
		digestParts = append(digestParts, inputs[i].kind, inputs[i].id, string(Float32ToBytes(vectors[i])))
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM graph_vector_items WHERE namespace=? AND version=? AND generation=?`, namespace, version, generation); err != nil {
		return err
	}
	for i, input := range inputs {
		result, e := tx.ExecContext(ctx, `INSERT INTO graph_vector_items(namespace,version,generation,entity_kind,entity_id,dimensions) VALUES(?,?,?,?,?,?)`, namespace, version, generation, input.kind, input.id, s.dims)
		if e != nil {
			return e
		}
		id, e := result.LastInsertId()
		if e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO graph_vectors(item_id,embedding) VALUES(?,?)`, id, Float32ToBytes(vectors[i])); e != nil {
			return e
		}
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM graph_vector_items WHERE namespace=? AND version=? AND generation=?`, namespace, version, generation).Scan(&count); err != nil {
		return err
	}
	if count != len(inputs) {
		return fmt.Errorf("graph vector generation coverage mismatch")
	}
	dimensions := identity.Dimensions
	if err = upsertSelectedGraphRetrievalGenerationForSnapshot(tx, namespace, version, graphRetrievalGeneration{
		Component: "vector", Generation: generation, Algorithm: identity.Algorithm, Provider: identity.Provider, Model: identity.Model, Dimensions: &dimensions, Digest: graphDerivedDigest(digestParts...),
	}); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE graph_snapshot_components SET state='ready',generation=? WHERE namespace=? AND version=? AND component='vector'`, generation, namespace, version); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if err = s.ReconcileGraphSnapshot(ctx, namespace, version); err != nil {
		return err
	}
	return s.ApplyGraphRetrievalRetention(ctx, namespace)
}
