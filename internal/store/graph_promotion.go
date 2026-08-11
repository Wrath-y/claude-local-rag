package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

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
	if err = createInitialGraphIndexGeneration(ctx, tx, namespace, version, taskID, manifest.Edges); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE graph_snapshot_components SET state='ready' WHERE namespace=? AND version=? AND component='graph'`, namespace, version); err != nil {
		return err
	}
	return tx.Commit()
}

// createInitialGraphIndexGeneration gives new graph/4 snapshots a selected,
// generation-addressable adjacency view. Query readers still use immutable
// graph_edges until they opt into the generation seam, preserving direct reads
// for every graph/1..3 snapshot.
func createInitialGraphIndexGeneration(ctx context.Context, tx *sql.Tx, namespace, version, taskID string, edges []graphsnapshot.Edge) error {
	generation := "graph-indexes-" + taskID
	digest := graphIndexDigest(edges)
	if _, err := tx.ExecContext(ctx, `INSERT INTO graph_retrieval_generations(namespace,version,component,generation,state,selected,algorithm,content_digest,created_at)
VALUES(?,?, 'graph_indexes',?,'selected',1,'edge-adjacency-v1',?,strftime('%Y-%m-%dT%H:%M:%fZ','now'))`, namespace, version, generation, digest); err != nil {
		return fmt.Errorf("create graph-index generation: %w", err)
	}
	for _, edge := range edges {
		for _, entry := range []struct {
			direction string
			nodeID    string
		}{{direction: "outgoing", nodeID: edge.From}, {direction: "incoming", nodeID: edge.To}} {
			if _, err := tx.ExecContext(ctx, `INSERT INTO graph_index_adjacency(namespace,version,generation,direction,node_id,edge_id,relation_kind,edge_type)
VALUES(?,?,?,?,?,?,?,?)`, namespace, version, generation, entry.direction, entry.nodeID, edge.ID, edge.RelationKind, edge.Type); err != nil {
				return fmt.Errorf("create graph-index adjacency: %w", err)
			}
		}
	}
	return nil
}

func graphIndexDigest(edges []graphsnapshot.Edge) string {
	ordered := append([]graphsnapshot.Edge(nil), edges...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })
	hash := sha256.New()
	for _, edge := range ordered {
		_, _ = hash.Write([]byte(edge.ID + "\x00" + edge.From + "\x00" + edge.To + "\x00" + edge.Type + "\x00" + edge.RelationKind + "\n"))
	}
	return hex.EncodeToString(hash.Sum(nil))
}
