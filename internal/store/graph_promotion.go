package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Wrath-y/local-rag/internal/graphsnapshot"
)

// PromoteGraphComponent materializes one accepted canonical staging manifest
// atomically. All graph rows remain absent if validation or any insert fails.
func (s *Store) PromoteGraphComponent(ctx context.Context, taskID string) error {
	var namespace, version, hash, manifestJSON string
	err := s.db.QueryRowContext(ctx, `SELECT t.namespace,t.version,s.content_hash,g.manifest_json FROM graph_tasks t JOIN graph_snapshots s ON s.namespace=t.namespace AND s.version=t.version JOIN graph_snapshot_staging g ON g.namespace=s.namespace AND g.version=s.version WHERE t.id=?`, taskID).Scan(&namespace, &version, &hash, &manifestJSON)
	if err != nil {
		return err
	}
	var manifest graphsnapshot.Manifest
	if err := json.Unmarshal([]byte(manifestJSON), &manifest); err != nil {
		return fmt.Errorf("decode graph staging: %w", err)
	}
	_, actual, err := graphsnapshot.CanonicalHash(manifest.Nodes, manifest.Edges)
	if err != nil || actual != hash {
		return fmt.Errorf("graph staging hash mismatch")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM graph_edges WHERE namespace=? AND version=?`, namespace, version); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM graph_nodes WHERE namespace=? AND version=?`, namespace, version); err != nil {
		return err
	}
	for _, n := range manifest.Nodes {
		if _, err = tx.ExecContext(ctx, `INSERT INTO graph_nodes(namespace,version,node_id,node_type,label,text,properties_json,provenance_json) VALUES(?,?,?,?,?,?,?,?)`, namespace, version, n.ID, n.Type, n.Label, n.Text, string(n.Properties), string(n.Provenance)); err != nil {
			return err
		}
	}
	for _, e := range manifest.Edges {
		if _, err = tx.ExecContext(ctx, `INSERT INTO graph_edges(namespace,version,edge_id,from_node_id,to_node_id,edge_type,relation_kind,confidence,properties_json,provenance_json) VALUES(?,?,?,?,?,?,?,?,?,?)`, namespace, version, e.ID, e.From, e.To, e.Type, e.RelationKind, e.Confidence.String(), string(e.Properties), string(e.Provenance)); err != nil {
			return err
		}
	}
	var nodes, edges int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM graph_nodes WHERE namespace=? AND version=?`, namespace, version).Scan(&nodes); err != nil {
		return err
	}
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM graph_edges WHERE namespace=? AND version=?`, namespace, version).Scan(&edges); err != nil {
		return err
	}
	if nodes != len(manifest.Nodes) || edges != len(manifest.Edges) {
		return fmt.Errorf("graph promotion count mismatch")
	}
	if _, err = tx.ExecContext(ctx, `UPDATE graph_snapshot_components SET state='ready' WHERE namespace=? AND version=? AND component='graph'`, namespace, version); err != nil {
		return err
	}
	return tx.Commit()
}
