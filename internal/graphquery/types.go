// Package graphquery contains deterministic, provider-independent graph query
// requests, normalization, and result types. It deliberately depends only on
// the immutable graph lifecycle model.
package graphquery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/Wrath-y/local-rag/internal/graphsnapshot"
)

const (
	DefaultMaxDepth = 3
	MaxDepth        = 6
	DefaultMaxNodes = 500
	MaxNodes        = 500
	DefaultMaxPaths = 20
	MaxPaths        = 100
)

type Direction string

const (
	DirectionOutgoing Direction = "outgoing"
	DirectionIncoming Direction = "incoming"
	DirectionBoth     Direction = "both"
)

type TruncationReason string

const (
	MaxDepthReached TruncationReason = "MAX_DEPTH"
	MaxNodesReached TruncationReason = "MAX_NODES"
	MaxPathsReached TruncationReason = "MAX_PATHS"
)

var (
	ErrNoActiveSnapshot = errors.New("no active graph snapshot")
	ErrSnapshotNotReady = errors.New("graph snapshot is not query ready")
	ErrSnapshotNotFound = errors.New("graph snapshot not found")
	ErrStoreUnavailable = errors.New("graph store unavailable")
)

// ReadView is snapshot-bound by its implementation. Methods accept IDs only;
// callers cannot add namespace or version predicates accidentally.
type ReadView interface {
	Version() string
	ContentHash() string
	Nodes(context.Context, []string) ([]graphsnapshot.Node, error)
	Adjacency(context.Context, []string, Direction) ([]graphsnapshot.Edge, error)
}

type Repository interface {
	WithRead(context.Context, string, string, func(ReadView) error) error
}

type TraverseRequest struct {
	SnapshotVersion   string    `json:"snapshot_version,omitempty"`
	StartNodeIDs      []string  `json:"start_node_ids"`
	Direction         Direction `json:"direction,omitempty"`
	NodeTypes         []string  `json:"node_types,omitempty"`
	EdgeTypes         []string  `json:"edge_types,omitempty"`
	RelationshipKinds []string  `json:"relationship_kinds,omitempty"`
	MaxDepth          *int      `json:"max_depth,omitempty"`
	MaxNodes          *int      `json:"max_nodes,omitempty"`
}

type PathsRequest struct {
	SnapshotVersion   string    `json:"snapshot_version,omitempty"`
	SourceNodeIDs     []string  `json:"source_node_ids"`
	TargetNodeIDs     []string  `json:"target_node_ids"`
	Direction         Direction `json:"direction,omitempty"`
	NodeTypes         []string  `json:"node_types,omitempty"`
	EdgeTypes         []string  `json:"edge_types,omitempty"`
	RelationshipKinds []string  `json:"relationship_kinds,omitempty"`
	MaxDepth          *int      `json:"max_depth,omitempty"`
	MaxPaths          *int      `json:"max_paths,omitempty"`
}

type Filter struct {
	SnapshotVersion   string
	Direction         Direction
	NodeTypes         []string
	EdgeTypes         []string
	RelationshipKinds []string
	MaxDepth          int
}

type NormalizedTraverse struct {
	Filter
	StartNodeIDs []string
	MaxNodes     int
}

type NormalizedPaths struct {
	Filter
	SourceNodeIDs []string
	TargetNodeIDs []string
	MaxPaths      int
}

type TraversedNode struct {
	graphsnapshot.Node
	Depth int `json:"depth"`
}

type Path struct {
	SourceNodeID      string   `json:"source_node_id"`
	TargetNodeID      string   `json:"target_node_id"`
	NodeIDs           []string `json:"node_ids"`
	EdgeIDs           []string `json:"edge_ids"`
	HopCount          int      `json:"hop_count"`
	InferredEdgeCount int      `json:"inferred_edge_count"`
	Confidence        float64  `json:"confidence"`
}

type TraverseResult struct {
	ResolvedSnapshotVersion string               `json:"resolved_snapshot_version"`
	ContentHash             string               `json:"content_hash"`
	Nodes                   []TraversedNode      `json:"nodes"`
	Edges                   []graphsnapshot.Edge `json:"edges"`
	Truncated               bool                 `json:"truncated"`
	TruncationReasons       []TruncationReason   `json:"truncation_reasons"`
}

type PathsResult struct {
	ResolvedSnapshotVersion string               `json:"resolved_snapshot_version"`
	ContentHash             string               `json:"content_hash"`
	Paths                   []Path               `json:"paths"`
	Nodes                   []graphsnapshot.Node `json:"nodes"`
	Edges                   []graphsnapshot.Edge `json:"edges"`
	Truncated               bool                 `json:"truncated"`
	TruncationReasons       []TruncationReason   `json:"truncation_reasons"`
}

func DecodeTraverse(body []byte) (TraverseRequest, *graphsnapshot.Error) {
	var request TraverseRequest
	if err := decodeStrict(body, &request); err != nil {
		return TraverseRequest{}, graphsnapshot.NewError(graphsnapshot.CodeInvalidGraphQuery, map[string]any{"field": "body"}, err)
	}
	return request, nil
}

func DecodePaths(body []byte) (PathsRequest, *graphsnapshot.Error) {
	var request PathsRequest
	if err := decodeStrict(body, &request); err != nil {
		return PathsRequest{}, graphsnapshot.NewError(graphsnapshot.CodeInvalidGraphQuery, map[string]any{"field": "body"}, err)
	}
	return request, nil
}

func NormalizeTraverse(request TraverseRequest) (NormalizedTraverse, *graphsnapshot.Error) {
	filter, graphErr := normalizeFilter(request.SnapshotVersion, request.Direction, request.NodeTypes, request.EdgeTypes, request.RelationshipKinds, request.MaxDepth)
	if graphErr != nil {
		return NormalizedTraverse{}, graphErr
	}
	ids, graphErr := normalizedRequiredSet("start_node_ids", request.StartNodeIDs)
	if graphErr != nil {
		return NormalizedTraverse{}, graphErr
	}
	limit, graphErr := normalizedLimit("max_nodes", request.MaxNodes, DefaultMaxNodes, MaxNodes)
	if graphErr != nil {
		return NormalizedTraverse{}, graphErr
	}
	return NormalizedTraverse{Filter: filter, StartNodeIDs: ids, MaxNodes: limit}, nil
}

func NormalizePaths(request PathsRequest) (NormalizedPaths, *graphsnapshot.Error) {
	filter, graphErr := normalizeFilter(request.SnapshotVersion, request.Direction, request.NodeTypes, request.EdgeTypes, request.RelationshipKinds, request.MaxDepth)
	if graphErr != nil {
		return NormalizedPaths{}, graphErr
	}
	sources, graphErr := normalizedRequiredSet("source_node_ids", request.SourceNodeIDs)
	if graphErr != nil {
		return NormalizedPaths{}, graphErr
	}
	targets, graphErr := normalizedRequiredSet("target_node_ids", request.TargetNodeIDs)
	if graphErr != nil {
		return NormalizedPaths{}, graphErr
	}
	limit, graphErr := normalizedLimit("max_paths", request.MaxPaths, DefaultMaxPaths, MaxPaths)
	if graphErr != nil {
		return NormalizedPaths{}, graphErr
	}
	return NormalizedPaths{Filter: filter, SourceNodeIDs: sources, TargetNodeIDs: targets, MaxPaths: limit}, nil
}

func normalizeFilter(version string, direction Direction, nodeTypes, edgeTypes, relationshipKinds []string, maxDepth *int) (Filter, *graphsnapshot.Error) {
	if direction == "" {
		direction = DirectionOutgoing
	}
	if direction != DirectionOutgoing && direction != DirectionIncoming && direction != DirectionBoth {
		return Filter{}, invalid("direction")
	}
	nodeTypes, graphErr := normalizedOptionalSet("node_types", nodeTypes)
	if graphErr != nil {
		return Filter{}, graphErr
	}
	edgeTypes, graphErr = normalizedOptionalSet("edge_types", edgeTypes)
	if graphErr != nil {
		return Filter{}, graphErr
	}
	if relationshipKinds == nil {
		relationshipKinds = []string{"explicit"}
	}
	relationshipKinds, graphErr = normalizedRequiredSet("relationship_kinds", relationshipKinds)
	if graphErr != nil {
		return Filter{}, graphErr
	}
	for _, kind := range relationshipKinds {
		if kind != "explicit" && kind != "inferred" {
			return Filter{}, invalid("relationship_kinds")
		}
	}
	depth, graphErr := normalizedLimit("max_depth", maxDepth, DefaultMaxDepth, MaxDepth)
	if graphErr != nil {
		return Filter{}, graphErr
	}
	return Filter{SnapshotVersion: strings.TrimSpace(version), Direction: direction, NodeTypes: nodeTypes, EdgeTypes: edgeTypes, RelationshipKinds: relationshipKinds, MaxDepth: depth}, nil
}

func normalizedLimit(field string, value *int, fallback, maximum int) (int, *graphsnapshot.Error) {
	if value == nil {
		return fallback, nil
	}
	if *value > maximum {
		return 0, graphsnapshot.NewError(graphsnapshot.CodeLimitExceeded, map[string]any{"field": field, "requested": *value, "maximum": maximum}, nil)
	}
	if *value < 0 || (field != "max_depth" && *value == 0) {
		return 0, invalid(field)
	}
	return *value, nil
}

func normalizedRequiredSet(field string, values []string) ([]string, *graphsnapshot.Error) {
	if len(values) == 0 {
		return nil, invalid(field)
	}
	return normalizedOptionalSet(field, values)
}

func normalizedOptionalSet(field string, values []string) ([]string, *graphsnapshot.Error) {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, invalid(field)
		}
		seen[value] = struct{}{}
	}
	result := make([]string, 0, len(seen))
	for value := range seen {
		result = append(result, value)
	}
	sort.Strings(result)
	return result, nil
}

func invalid(field string) *graphsnapshot.Error {
	return graphsnapshot.NewError(graphsnapshot.CodeInvalidGraphQuery, map[string]any{"field": field}, nil)
}

func decodeStrict(body []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := inspectValue(decoder); err != nil {
		return err
	}
	if token, err := decoder.Token(); err != io.EOF {
		if err != nil {
			return err
		}
		return fmt.Errorf("trailing value %v", token)
	}
	decoder = json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if token, err := decoder.Token(); err != io.EOF {
		if err != nil {
			return err
		}
		return fmt.Errorf("trailing value %v", token)
	}
	return nil
}

func inspectValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]struct{}{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("object key is not a string")
			}
			if _, exists := seen[key]; exists {
				return fmt.Errorf("duplicate object member %q", key)
			}
			seen[key] = struct{}{}
			if err := inspectValue(decoder); err != nil {
				return err
			}
		}
		_, err := decoder.Token()
		return err
	case '[':
		for decoder.More() {
			if err := inspectValue(decoder); err != nil {
				return err
			}
		}
		_, err := decoder.Token()
		return err
	default:
		return fmt.Errorf("invalid JSON delimiter")
	}
}
