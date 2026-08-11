package store

import (
	"context"
	"fmt"
	"github.com/Wrath-y/local-rag/internal/graphsnapshot"
)

// PopulateGraphSearchDocuments rebuilds only one snapshot's derived FTS input
// in a transaction. Trigger maintenance keeps virtual rows invisible until
// the transaction commits.
func (s *Store) PopulateGraphSearchDocuments(ctx context.Context, taskID string) error {
	var namespace, version string
	if err := s.db.QueryRowContext(ctx, `SELECT namespace,version FROM graph_tasks WHERE id=?`, taskID).Scan(&namespace, &version); err != nil {
		return err
	}
	graph, err := s.ReadGraphSnapshot(ctx, namespace, version)
	if err != nil {
		return err
	}
	generation := "fts-" + taskID
	digestParts := []string{"fts5", graphsnapshot.SearchDocumentFormatV1, generation}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM graph_search_documents WHERE namespace=? AND version=?`, namespace, version); err != nil {
		return err
	}
	for i := range graph.Nodes {
		text, e := graphsnapshot.SearchDocument(&graph.Nodes[i], nil)
		if e != nil {
			return e
		}
		digestParts = append(digestParts, "node", graph.Nodes[i].ID, text)
		if _, err = tx.ExecContext(ctx, `INSERT INTO graph_search_documents(namespace,version,generation,entity_kind,entity_id,search_text) VALUES(?,?,?, 'node',?,?)`, namespace, version, generation, graph.Nodes[i].ID, text); err != nil {
			return err
		}
	}
	for i := range graph.Edges {
		text, e := graphsnapshot.SearchDocument(nil, &graph.Edges[i])
		if e != nil {
			return e
		}
		digestParts = append(digestParts, "edge", graph.Edges[i].ID, text)
		if _, err = tx.ExecContext(ctx, `INSERT INTO graph_search_documents(namespace,version,generation,entity_kind,entity_id,search_text) VALUES(?,?,?, 'edge',?,?)`, namespace, version, generation, graph.Edges[i].ID, text); err != nil {
			return err
		}
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM graph_search_documents WHERE namespace=? AND version=?`, namespace, version).Scan(&count); err != nil {
		return err
	}
	if count != len(graph.Nodes)+len(graph.Edges) {
		return fmt.Errorf("graph search document coverage mismatch")
	}
	if err = upsertSelectedGraphRetrievalGenerationForSnapshot(tx, namespace, version, graphRetrievalGeneration{
		Component: "fts", Generation: generation, Algorithm: graphsnapshot.SearchDocumentFormatV1 + "/fts5", Tokenizer: "unicode61", Digest: graphDerivedDigest(digestParts...),
	}); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE graph_snapshot_components SET state='ready',generation=? WHERE namespace=? AND version=? AND component='fts'`, generation, namespace, version); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return s.ReconcileGraphSnapshot(ctx, namespace, version)
}
