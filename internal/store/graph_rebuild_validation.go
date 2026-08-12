package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Wrath-y/local-rag/internal/graphsnapshot"
	"github.com/Wrath-y/local-rag/internal/operability"
)

// validatePrivateGraphRebuildGeneration is the shared gate between a
// component's private writes and its durable validated step. It deliberately
// checks the generation through the same namespace/version scope used by read
// views, so a private row can never become promotable merely because a count
// in another snapshot happens to match.
func validatePrivateGraphRebuildGeneration(ctx context.Context, tx *sql.Tx, record GraphSnapshotRecord, component operability.Component, generation, digest string, vectorIdentity *graphsnapshot.EmbeddingIdentity) error {
	var storedDigest string
	var selected int
	if err := tx.QueryRowContext(ctx, `SELECT content_digest,selected FROM graph_retrieval_generations WHERE namespace=? AND version=? AND component=? AND generation=? AND state='private'`, record.Namespace, record.Version, string(component), generation).Scan(&storedDigest, &selected); err != nil {
		return fmt.Errorf("validate private %s generation: %w", component, err)
	}
	if selected != 0 || storedDigest != digest {
		return fmt.Errorf("validate private %s generation identity mismatch", component)
	}

	switch component {
	case operability.ComponentGraphIndexes:
		if digest != graphIndexDigest(record.Edges) {
			return fmt.Errorf("validate graph-index digest mismatch")
		}
		var count, dangling int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM graph_index_adjacency WHERE namespace=? AND version=? AND generation=?`, record.Namespace, record.Version, generation).Scan(&count); err != nil {
			return err
		}
		if count != len(record.Edges)*2 {
			return fmt.Errorf("validate graph-index coverage mismatch")
		}
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM graph_index_adjacency AS adjacency LEFT JOIN graph_edges AS edge ON edge.namespace=adjacency.namespace AND edge.version=adjacency.version AND edge.edge_id=adjacency.edge_id WHERE adjacency.namespace=? AND adjacency.version=? AND adjacency.generation=? AND edge.edge_id IS NULL`, record.Namespace, record.Version, generation).Scan(&dangling); err != nil {
			return err
		}
		if dangling != 0 {
			return fmt.Errorf("validate graph-index edge coverage mismatch")
		}
	case operability.ComponentFTS:
		expectedDigestParts := []string{"fts5", graphsnapshot.SearchDocumentFormatV1, generation}
		expected := len(record.Nodes) + len(record.Edges)
		var count, ftsRows int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM graph_search_documents WHERE namespace=? AND version=? AND generation=?`, record.Namespace, record.Version, generation).Scan(&count); err != nil {
			return err
		}
		if count != expected {
			return fmt.Errorf("validate FTS coverage mismatch")
		}
		for _, node := range record.Nodes {
			text, err := graphsnapshot.SearchDocument(&node, nil)
			if err != nil {
				return err
			}
			expectedDigestParts = append(expectedDigestParts, "node", node.ID, text)
			var actual string
			if err = tx.QueryRowContext(ctx, `SELECT search_text FROM graph_search_documents WHERE namespace=? AND version=? AND generation=? AND entity_kind='node' AND entity_id=?`, record.Namespace, record.Version, generation, node.ID).Scan(&actual); err != nil {
				return fmt.Errorf("validate FTS node document: %w", err)
			}
			if actual != text {
				return fmt.Errorf("validate FTS node document mismatch")
			}
		}
		for _, edge := range record.Edges {
			text, err := graphsnapshot.SearchDocument(nil, &edge)
			if err != nil {
				return err
			}
			expectedDigestParts = append(expectedDigestParts, "edge", edge.ID, text)
			var actual string
			if err = tx.QueryRowContext(ctx, `SELECT search_text FROM graph_search_documents WHERE namespace=? AND version=? AND generation=? AND entity_kind='edge' AND entity_id=?`, record.Namespace, record.Version, generation, edge.ID).Scan(&actual); err != nil {
				return fmt.Errorf("validate FTS edge document: %w", err)
			}
			if actual != text {
				return fmt.Errorf("validate FTS edge document mismatch")
			}
		}
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM graph_search_fts AS fts JOIN graph_search_documents AS document ON document.id=fts.rowid WHERE document.namespace=? AND document.version=? AND document.generation=?`, record.Namespace, record.Version, generation).Scan(&ftsRows); err != nil {
			return err
		}
		if ftsRows != expected || graphDerivedDigest(expectedDigestParts...) != digest {
			return fmt.Errorf("validate FTS digest or index coverage mismatch")
		}
	case operability.ComponentVector:
		if vectorIdentity == nil {
			return fmt.Errorf("validate vector identity is required")
		}
		var algorithm string
		var provider, model sql.NullString
		var dimensions int
		if err := tx.QueryRowContext(ctx, `SELECT algorithm,provider,model,dimensions FROM graph_retrieval_generations WHERE namespace=? AND version=? AND component='vector' AND generation=?`, record.Namespace, record.Version, generation).Scan(&algorithm, &provider, &model, &dimensions); err != nil {
			return err
		}
		if algorithm != vectorIdentity.Algorithm || provider.String != vectorIdentity.Provider || model.String != vectorIdentity.Model || dimensions != vectorIdentity.Dimensions {
			return fmt.Errorf("validate vector identity mismatch")
		}
		var items, vectors, wrongDimensions int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM graph_vector_items WHERE namespace=? AND version=? AND generation=?`, record.Namespace, record.Version, generation).Scan(&items); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM graph_vectors AS vector JOIN graph_vector_items AS item ON item.id=vector.item_id WHERE item.namespace=? AND item.version=? AND item.generation=?`, record.Namespace, record.Version, generation).Scan(&vectors); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM graph_vector_items WHERE namespace=? AND version=? AND generation=? AND dimensions<>?`, record.Namespace, record.Version, generation, vectorIdentity.Dimensions).Scan(&wrongDimensions); err != nil {
			return err
		}
		if items != len(record.Nodes)+len(record.Edges) || vectors != items || wrongDimensions != 0 {
			return fmt.Errorf("validate vector coverage mismatch")
		}
	default:
		return fmt.Errorf("unknown rebuild component %q", component)
	}
	return nil
}
