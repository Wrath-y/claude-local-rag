package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/Wrath-y/local-rag/internal/graphquery"
	"github.com/Wrath-y/local-rag/internal/graphsnapshot"
)

// WithRead supplies one immutable, namespace/version-bound read view. The
// transaction captures an active pointer exactly once and stays open until the
// caller's callback completes, so no query can mix snapshots.
func (s *Store) WithRead(ctx context.Context, namespace, requestedVersion string, fn func(graphquery.ReadView) error) error {
	if err := s.GraphUnavailable(); err != nil {
		return fmt.Errorf("%w: %v", graphquery.ErrStoreUnavailable, err)
	}
	if namespace == "" {
		return ErrInvalidGraphIdentity
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return fmt.Errorf("%w: begin graph query read: %v", graphquery.ErrStoreUnavailable, err)
	}
	defer tx.Rollback()
	version := requestedVersion
	if version == "" {
		err = tx.QueryRowContext(ctx, `SELECT active_version FROM graph_namespace_heads WHERE namespace=?`, namespace).Scan(&version)
		if errors.Is(err, sql.ErrNoRows) {
			return graphquery.ErrNoActiveSnapshot
		}
		if err != nil {
			return fmt.Errorf("resolve active graph snapshot: %w", err)
		}
	}
	var status graphsnapshot.SnapshotStatus
	var ready int
	var contentHash string
	err = tx.QueryRowContext(ctx, `SELECT status,query_ready,content_hash FROM graph_snapshots WHERE namespace=? AND version=?`, namespace, version).Scan(&status, &ready, &contentHash)
	if errors.Is(err, sql.ErrNoRows) {
		return graphquery.ErrSnapshotNotFound
	}
	if err != nil {
		return fmt.Errorf("resolve graph snapshot: %w", err)
	}
	if status != graphsnapshot.SnapshotReady || ready != 1 {
		return graphquery.ErrSnapshotNotReady
	}
	var indexGeneration sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT generation FROM graph_retrieval_generations WHERE namespace=? AND version=? AND component='graph_indexes' AND selected=1 AND state='selected'`, namespace, version).Scan(&indexGeneration); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("resolve graph-index generation: %w", err)
	}
	if err := fn(&graphReadView{tx: tx, namespace: namespace, version: version, contentHash: contentHash, graphIndexGeneration: indexGeneration.String}); err != nil {
		return err
	}
	return tx.Commit()
}

type graphReadView struct {
	tx                   *sql.Tx
	namespace            string
	version              string
	contentHash          string
	graphIndexGeneration string
	nodes                map[string]graphsnapshot.Node
	adjacency            map[graphquery.Direction]map[string][]graphsnapshot.Edge
}

func (v *graphReadView) Version() string     { return v.version }
func (v *graphReadView) ContentHash() string { return v.contentHash }

func (v *graphReadView) Nodes(ctx context.Context, ids []string) ([]graphsnapshot.Node, error) {
	ids = sortedUnique(ids)
	if len(ids) == 0 {
		return []graphsnapshot.Node{}, nil
	}
	if v.nodes == nil {
		v.nodes = map[string]graphsnapshot.Node{}
	}
	missing := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, ok := v.nodes[id]; !ok {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		query, args := graphInQuery(`SELECT node_id,node_type,label,text,properties_json,provenance_json FROM graph_nodes WHERE namespace=? AND version=? AND node_id IN`, v.namespace, v.version, missing)
		rows, err := v.tx.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, fmt.Errorf("load graph nodes: %w", err)
		}
		for rows.Next() {
			var node graphsnapshot.Node
			var properties, provenance string
			if err := rows.Scan(&node.ID, &node.Type, &node.Label, &node.Text, &properties, &provenance); err != nil {
				rows.Close()
				return nil, err
			}
			node.Properties, node.Provenance = json.RawMessage(properties), json.RawMessage(provenance)
			v.nodes[node.ID] = node
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	result := make([]graphsnapshot.Node, 0, len(ids))
	for _, id := range ids {
		if node, ok := v.nodes[id]; ok {
			result = append(result, node)
		}
	}
	return result, nil
}

func (v *graphReadView) Adjacency(ctx context.Context, ids []string, direction graphquery.Direction) ([]graphsnapshot.Edge, error) {
	ids = sortedUnique(ids)
	if len(ids) == 0 {
		return []graphsnapshot.Edge{}, nil
	}
	if v.adjacency == nil {
		v.adjacency = map[graphquery.Direction]map[string][]graphsnapshot.Edge{}
	}
	cache := v.adjacency[direction]
	if cache == nil {
		cache = map[string][]graphsnapshot.Edge{}
		v.adjacency[direction] = cache
	}
	missing := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, ok := cache[id]; !ok {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		if err := v.loadAdjacency(ctx, missing, direction, cache); err != nil {
			return nil, err
		}
	}
	byID := map[string]graphsnapshot.Edge{}
	for _, id := range ids {
		for _, edge := range cache[id] {
			byID[edge.ID] = edge
		}
	}
	result := make([]graphsnapshot.Edge, 0, len(byID))
	for _, edge := range byID {
		result = append(result, edge)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func (v *graphReadView) loadAdjacency(ctx context.Context, ids []string, direction graphquery.Direction, cache map[string][]graphsnapshot.Edge) error {
	for _, id := range ids {
		cache[id] = []graphsnapshot.Edge{}
	}
	column := "from_node_id"
	if direction == graphquery.DirectionIncoming {
		column = "to_node_id"
	}
	if direction == graphquery.DirectionBoth {
		if err := v.loadAdjacency(ctx, ids, graphquery.DirectionOutgoing, cache); err != nil {
			return err
		}
		incoming := map[string][]graphsnapshot.Edge{}
		if err := v.loadAdjacency(ctx, ids, graphquery.DirectionIncoming, incoming); err != nil {
			return err
		}
		for id, edges := range incoming {
			cache[id] = append(cache[id], edges...)
		}
		for id := range cache {
			cache[id] = uniqueEdges(cache[id])
		}
		return nil
	}
	if v.graphIndexGeneration != "" {
		marks := make([]string, len(ids))
		args := make([]any, 0, 4+len(ids))
		args = append(args, v.namespace, v.version, v.graphIndexGeneration, string(direction))
		for index, id := range ids {
			marks[index] = "?"
			args = append(args, id)
		}
		query := `SELECT edge.edge_id,edge.from_node_id,edge.to_node_id,edge.edge_type,edge.relation_kind,edge.confidence,edge.properties_json,edge.provenance_json FROM graph_index_adjacency AS adjacency JOIN graph_edges AS edge ON edge.namespace=adjacency.namespace AND edge.version=adjacency.version AND edge.edge_id=adjacency.edge_id WHERE adjacency.namespace=? AND adjacency.version=? AND adjacency.generation=? AND adjacency.direction=? AND adjacency.node_id IN (` + joinMarks(marks) + `)`
		rows, err := v.tx.QueryContext(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("load graph-index adjacency: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var edge graphsnapshot.Edge
			var confidence, properties, provenance string
			if err := rows.Scan(&edge.ID, &edge.From, &edge.To, &edge.Type, &edge.RelationKind, &confidence, &properties, &provenance); err != nil {
				return err
			}
			edge.Confidence, edge.Properties, edge.Provenance = json.Number(confidence), json.RawMessage(properties), json.RawMessage(provenance)
			key := edge.From
			if direction == graphquery.DirectionIncoming {
				key = edge.To
			}
			cache[key] = append(cache[key], edge)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		for id := range cache {
			sort.Slice(cache[id], func(i, j int) bool { return cache[id][i].ID < cache[id][j].ID })
		}
		return nil
	}
	query, args := graphInQuery(`SELECT edge_id,from_node_id,to_node_id,edge_type,relation_kind,confidence,properties_json,provenance_json FROM graph_edges WHERE namespace=? AND version=? AND `+column+` IN`, v.namespace, v.version, ids)
	rows, err := v.tx.QueryContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("load graph adjacency: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var edge graphsnapshot.Edge
		var confidence, properties, provenance string
		if err := rows.Scan(&edge.ID, &edge.From, &edge.To, &edge.Type, &edge.RelationKind, &confidence, &properties, &provenance); err != nil {
			return err
		}
		edge.Confidence, edge.Properties, edge.Provenance = json.Number(confidence), json.RawMessage(properties), json.RawMessage(provenance)
		key := edge.From
		if direction == graphquery.DirectionIncoming {
			key = edge.To
		}
		cache[key] = append(cache[key], edge)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for id := range cache {
		sort.Slice(cache[id], func(i, j int) bool { return cache[id][i].ID < cache[id][j].ID })
	}
	return nil
}

func graphInQuery(prefix, namespace, version string, ids []string) (string, []any) {
	marks := make([]string, len(ids))
	args := make([]any, 0, 2+len(ids))
	args = append(args, namespace, version)
	for i, id := range ids {
		marks[i] = "?"
		args = append(args, id)
	}
	return prefix + ` (` + joinMarks(marks) + `)`, args
}

func joinMarks(marks []string) string {
	if len(marks) == 0 {
		return ""
	}
	result := marks[0]
	for _, mark := range marks[1:] {
		result += "," + mark
	}
	return result
}
func sortedUnique(values []string) []string {
	seen := map[string]struct{}{}
	for _, value := range values {
		seen[value] = struct{}{}
	}
	result := make([]string, 0, len(seen))
	for value := range seen {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
func uniqueEdges(edges []graphsnapshot.Edge) []graphsnapshot.Edge {
	byID := map[string]graphsnapshot.Edge{}
	for _, edge := range edges {
		byID[edge.ID] = edge
	}
	result := make([]graphsnapshot.Edge, 0, len(byID))
	for _, edge := range byID {
		result = append(result, edge)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}
