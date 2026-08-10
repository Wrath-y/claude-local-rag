package graphsnapshot

import (
	"encoding/json"
	"math"
	"strings"
	"time"
)

// NormalizeFull validates a complete request and returns the immutable final
// graph. It intentionally does not compare ContentHash: that happens only
// after full/delta materialization, immediately before durable acceptance.
func NormalizeFull(request Request) (Manifest, *Error) {
	if err := validateRequestIdentity(request, ModeFull); err != nil {
		return Manifest{}, err
	}
	if request.BaseVersion != "" || len(request.NodeUpserts) != 0 || len(request.NodeDeletes) != 0 || len(request.EdgeUpserts) != 0 || len(request.EdgeDeletes) != 0 {
		return Manifest{}, NewError(CodeInvalidSnapshotRequest, map[string]any{"field": "mode"}, nil)
	}
	if err := validateGraph(request.Nodes, request.Edges); err != nil {
		return Manifest{}, err
	}
	return Manifest{SchemaVersion: SchemaVersionV1, Nodes: cloneNodes(request.Nodes), Edges: cloneEdges(request.Edges)}, nil
}

// ApplyDelta applies complete-record upserts and strict deletes to an already
// immutable base. It never mutates the base manifest or permits an operation
// to silently overwrite a missing record.
func ApplyDelta(base Manifest, request Request) (Manifest, *Error) {
	if err := validateRequestIdentity(request, ModeDelta); err != nil {
		return Manifest{}, err
	}
	if request.BaseVersion == "" || len(request.Nodes) != 0 || len(request.Edges) != 0 {
		return Manifest{}, NewError(CodeInvalidSnapshotRequest, map[string]any{"field": "base_version"}, nil)
	}
	nodes := make(map[string]Node, len(base.Nodes)+len(request.NodeUpserts))
	for _, node := range base.Nodes {
		nodes[node.ID] = node
	}
	edges := make(map[string]Edge, len(base.Edges)+len(request.EdgeUpserts))
	for _, edge := range base.Edges {
		edges[edge.ID] = edge
	}
	if err := applyNodeDelta(nodes, request.NodeUpserts, request.NodeDeletes); err != nil {
		return Manifest{}, err
	}
	if err := applyEdgeDelta(edges, request.EdgeUpserts, request.EdgeDeletes); err != nil {
		return Manifest{}, err
	}
	finalNodes := make([]Node, 0, len(nodes))
	for _, node := range nodes {
		finalNodes = append(finalNodes, node)
	}
	finalEdges := make([]Edge, 0, len(edges))
	for _, edge := range edges {
		finalEdges = append(finalEdges, edge)
	}
	if err := validateGraph(finalNodes, finalEdges); err != nil {
		return Manifest{}, err
	}
	return Manifest{SchemaVersion: SchemaVersionV1, Nodes: finalNodes, Edges: finalEdges}, nil
}

func validateRequestIdentity(request Request, mode Mode) *Error {
	if request.SchemaVersion != SchemaVersionV1 || request.Mode != mode || !isLowerSHA256(request.ContentHash) {
		return NewError(CodeInvalidSnapshotRequest, nil, nil)
	}
	return nil
}

func applyNodeDelta(nodes map[string]Node, upserts []Node, deletes []string) *Error {
	upsertIDs := map[string]struct{}{}
	for _, node := range upserts {
		if _, duplicate := upsertIDs[node.ID]; duplicate {
			return NewError(CodeDuplicateNodeID, map[string]any{"id": node.ID}, nil)
		}
		upsertIDs[node.ID] = struct{}{}
	}
	deleteIDs := map[string]struct{}{}
	for _, id := range deletes {
		if _, duplicate := deleteIDs[id]; duplicate {
			return NewError(CodeInvalidDeltaOperation, map[string]any{"id": id}, nil)
		}
		if _, conflict := upsertIDs[id]; conflict {
			return NewError(CodeInvalidDeltaOperation, map[string]any{"id": id}, nil)
		}
		if _, exists := nodes[id]; !exists {
			return NewError(CodeInvalidDeltaOperation, map[string]any{"id": id}, nil)
		}
		deleteIDs[id] = struct{}{}
	}
	for id := range deleteIDs {
		delete(nodes, id)
	}
	for _, node := range upserts {
		nodes[node.ID] = node
	}
	return nil
}

func applyEdgeDelta(edges map[string]Edge, upserts []Edge, deletes []string) *Error {
	upsertIDs := map[string]struct{}{}
	for _, edge := range upserts {
		if _, duplicate := upsertIDs[edge.ID]; duplicate {
			return NewError(CodeDuplicateEdgeID, map[string]any{"id": edge.ID}, nil)
		}
		upsertIDs[edge.ID] = struct{}{}
	}
	deleteIDs := map[string]struct{}{}
	for _, id := range deletes {
		if _, duplicate := deleteIDs[id]; duplicate {
			return NewError(CodeInvalidDeltaOperation, map[string]any{"id": id}, nil)
		}
		if _, conflict := upsertIDs[id]; conflict {
			return NewError(CodeInvalidDeltaOperation, map[string]any{"id": id}, nil)
		}
		if _, exists := edges[id]; !exists {
			return NewError(CodeInvalidDeltaOperation, map[string]any{"id": id}, nil)
		}
		deleteIDs[id] = struct{}{}
	}
	for id := range deleteIDs {
		delete(edges, id)
	}
	for _, edge := range upserts {
		edges[edge.ID] = edge
	}
	return nil
}

func validateGraph(nodes []Node, edges []Edge) *Error {
	nodeIDs := make(map[string]struct{}, len(nodes))
	for _, node := range nodes {
		if err := validateNode(node); err != nil {
			return err
		}
		if _, duplicate := nodeIDs[node.ID]; duplicate {
			return NewError(CodeDuplicateNodeID, map[string]any{"id": node.ID}, nil)
		}
		nodeIDs[node.ID] = struct{}{}
	}
	edgeIDs := make(map[string]struct{}, len(edges))
	for _, edge := range edges {
		if err := validateEdge(edge); err != nil {
			return err
		}
		if _, duplicate := edgeIDs[edge.ID]; duplicate {
			return NewError(CodeDuplicateEdgeID, map[string]any{"id": edge.ID}, nil)
		}
		if _, exists := nodeIDs[edge.From]; !exists {
			return NewError(CodeDanglingEdge, map[string]any{"edge_id": edge.ID, "endpoint": "from"}, nil)
		}
		if _, exists := nodeIDs[edge.To]; !exists {
			return NewError(CodeDanglingEdge, map[string]any{"edge_id": edge.ID, "endpoint": "to"}, nil)
		}
		edgeIDs[edge.ID] = struct{}{}
	}
	return nil
}

func validateNode(node Node) *Error {
	if normalizedID(node.ID) == "" || strings.TrimSpace(node.Type) == "" || strings.TrimSpace(node.Label) == "" || strings.TrimSpace(node.Text) == "" || !isJSONObject(node.Properties) || !isJSONObject(node.Provenance) {
		return NewError(CodeInvalidSnapshotRequest, map[string]any{"entity": "node"}, nil)
	}
	return nil
}

func validateEdge(edge Edge) *Error {
	if normalizedID(edge.ID) == "" || normalizedID(edge.From) == "" || normalizedID(edge.To) == "" || strings.TrimSpace(edge.Type) == "" || !isJSONObject(edge.Properties) || !isJSONObject(edge.Provenance) {
		return NewError(CodeInvalidSnapshotRequest, map[string]any{"entity": "edge"}, nil)
	}
	confidence, err := edge.Confidence.Float64()
	if err != nil || math.IsNaN(confidence) || math.IsInf(confidence, 0) {
		return NewError(CodeInvalidRelationProvenance, map[string]any{"edge_id": edge.ID}, err)
	}
	switch edge.RelationKind {
	case "explicit":
		if confidence != 1 {
			return NewError(CodeInvalidRelationProvenance, map[string]any{"edge_id": edge.ID}, nil)
		}
	case "inferred":
		if confidence < 0 || confidence > 1 || !validInferredProvenance(edge.Provenance) {
			return NewError(CodeInvalidRelationProvenance, map[string]any{"edge_id": edge.ID}, nil)
		}
	default:
		return NewError(CodeInvalidRelationProvenance, map[string]any{"edge_id": edge.ID}, nil)
	}
	return nil
}

func validInferredProvenance(raw json.RawMessage) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return false
	}
	producer := firstString(fields, "producer", "model", "algorithm")
	version := firstString(fields, "producer_version", "model_version", "algorithm_version", "version")
	generated := firstString(fields, "generated_at")
	if producer == "" || version == "" || generated == "" {
		return false
	}
	if _, err := time.Parse(time.RFC3339, generated); err != nil {
		return false
	}
	for _, key := range []string{"evidence", "evidence_refs", "evidence_references"} {
		var values []string
		if value, ok := fields[key]; ok && json.Unmarshal(value, &values) == nil && len(values) > 0 {
			for _, item := range values {
				if strings.TrimSpace(item) == "" {
					return false
				}
			}
			return true
		}
	}
	return false
}

func firstString(fields map[string]json.RawMessage, keys ...string) string {
	for _, key := range keys {
		var value string
		if raw, ok := fields[key]; ok && json.Unmarshal(raw, &value) == nil && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func isJSONObject(raw json.RawMessage) bool {
	var object map[string]json.RawMessage
	return len(raw) > 0 && json.Unmarshal(raw, &object) == nil && object != nil
}

func isLowerSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if !(character >= '0' && character <= '9') && !(character >= 'a' && character <= 'f') {
			return false
		}
	}
	return true
}

func cloneNodes(nodes []Node) []Node { return append([]Node(nil), nodes...) }
func cloneEdges(edges []Edge) []Edge { return append([]Edge(nil), edges...) }
