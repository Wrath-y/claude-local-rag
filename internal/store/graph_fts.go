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
		if _, err = tx.ExecContext(ctx, `INSERT INTO graph_search_documents(namespace,version,entity_kind,entity_id,search_text) VALUES(?,?, 'node',?,?)`, namespace, version, graph.Nodes[i].ID, text); err != nil {
			return err
		}
	}
	for i := range graph.Edges {
		text, e := graphsnapshot.SearchDocument(nil, &graph.Edges[i])
		if e != nil {
			return e
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO graph_search_documents(namespace,version,entity_kind,entity_id,search_text) VALUES(?,?, 'edge',?,?)`, namespace, version, graph.Edges[i].ID, text); err != nil {
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
	if _, err = tx.ExecContext(ctx, `UPDATE graph_snapshot_components SET state='ready' WHERE namespace=? AND version=? AND component='fts'`, namespace, version); err != nil {
		return err
	}
	return tx.Commit()
}
