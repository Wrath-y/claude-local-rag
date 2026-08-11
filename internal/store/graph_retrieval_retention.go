package store

import (
	"context"
	"database/sql"
	"fmt"
)

const graphRetrievalRecentVersions = 20

// ApplyGraphRetrievalRetention evicts only selected derived generations that
// fall outside active ∪ newest-20. It is idempotent and never deletes graph
// source rows, heads, hashes, tasks, or lifecycle component state.
func (s *Store) ApplyGraphRetrievalRetention(ctx context.Context, namespace string) error {
	if err := s.GraphUnavailable(); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	keep := map[string]struct{}{}
	var active sql.NullString
	if err = tx.QueryRowContext(ctx, `SELECT active_version FROM graph_namespace_heads WHERE namespace=?`, namespace).Scan(&active); err != nil && err != sql.ErrNoRows {
		return err
	}
	if active.Valid {
		keep[active.String] = struct{}{}
	}
	rows, err := tx.QueryContext(ctx, `SELECT version FROM graph_snapshots WHERE namespace=? ORDER BY created_at DESC,version ASC LIMIT ?`, namespace, graphRetrievalRecentVersions)
	if err != nil {
		return err
	}
	for rows.Next() {
		var version string
		if err = rows.Scan(&version); err != nil {
			rows.Close()
			return err
		}
		keep[version] = struct{}{}
	}
	if err = rows.Close(); err != nil {
		return err
	}
	if err = rows.Err(); err != nil {
		return err
	}
	generations, err := tx.QueryContext(ctx, `SELECT version,component,generation FROM graph_retrieval_generations WHERE namespace=? AND selected=1 AND state='selected' ORDER BY version,component,generation`, namespace)
	if err != nil {
		return err
	}
	type victim struct{ version, component, generation string }
	victims := []victim{}
	for generations.Next() {
		var item victim
		if err = generations.Scan(&item.version, &item.component, &item.generation); err != nil {
			generations.Close()
			return err
		}
		if _, retained := keep[item.version]; !retained {
			victims = append(victims, item)
		}
	}
	if err = generations.Close(); err != nil {
		return err
	}
	if err = generations.Err(); err != nil {
		return err
	}
	for _, item := range victims {
		switch item.component {
		case "fts":
			if _, err = tx.ExecContext(ctx, `DELETE FROM graph_search_documents WHERE namespace=? AND version=? AND generation=?`, namespace, item.version, item.generation); err != nil {
				return err
			}
		case "vector":
			if _, err = tx.ExecContext(ctx, `DELETE FROM graph_vector_items WHERE namespace=? AND version=? AND generation=?`, namespace, item.version, item.generation); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unknown retrieval generation component %q", item.component)
		}
		if _, err = tx.ExecContext(ctx, `UPDATE graph_retrieval_generations SET state='evicted',selected=0 WHERE namespace=? AND version=? AND component=? AND generation=? AND selected=1`, namespace, item.version, item.component, item.generation); err != nil {
			return err
		}
	}
	return tx.Commit()
}
