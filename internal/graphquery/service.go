package graphquery

import (
	"context"
	"errors"
	"sort"

	"github.com/Wrath-y/local-rag/internal/graphsnapshot"
)

type Service struct{ Repository Repository }

func (s Service) Traverse(ctx context.Context, namespace string, request TraverseRequest) (TraverseResult, *graphsnapshot.Error) {
	normalized, graphErr := NormalizeTraverse(request)
	if graphErr != nil {
		return TraverseResult{}, graphErr
	}
	if s.Repository == nil {
		return TraverseResult{}, graphsnapshot.NewError(graphsnapshot.CodeGraphStoreUnavailable, nil, nil)
	}
	var result TraverseResult
	err := s.Repository.WithRead(ctx, namespace, normalized.SnapshotVersion, func(view ReadView) error {
		if graphErr := validateNodes(ctx, view, normalized.StartNodeIDs); graphErr != nil {
			return graphErr
		}
		result = traverse(ctx, view, normalized)
		return nil
	})
	if err != nil {
		return TraverseResult{}, mapError(err)
	}
	return result, nil
}

func (s Service) Paths(ctx context.Context, namespace string, request PathsRequest) (PathsResult, *graphsnapshot.Error) {
	normalized, graphErr := NormalizePaths(request)
	if graphErr != nil {
		return PathsResult{}, graphErr
	}
	if s.Repository == nil {
		return PathsResult{}, graphsnapshot.NewError(graphsnapshot.CodeGraphStoreUnavailable, nil, nil)
	}
	var result PathsResult
	err := s.Repository.WithRead(ctx, namespace, normalized.SnapshotVersion, func(view ReadView) error {
		ids := append(append([]string{}, normalized.SourceNodeIDs...), normalized.TargetNodeIDs...)
		if graphErr := validateNodes(ctx, view, ids); graphErr != nil {
			return graphErr
		}
		result = paths(ctx, view, normalized)
		return nil
	})
	if err != nil {
		return PathsResult{}, mapError(err)
	}
	return result, nil
}

func validateNodes(ctx context.Context, view ReadView, requested []string) *graphsnapshot.Error {
	requested = sortedUnique(requested)
	nodes, err := view.Nodes(ctx, requested)
	if err != nil {
		return graphsnapshot.NewError(graphsnapshot.CodeGraphStoreUnavailable, nil, err)
	}
	found := map[string]struct{}{}
	for _, node := range nodes {
		found[node.ID] = struct{}{}
	}
	missing := make([]string, 0)
	for _, id := range requested {
		if _, ok := found[id]; !ok {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		return graphsnapshot.NewError(graphsnapshot.CodeNodeNotFound, map[string]any{"missing_node_ids": missing}, nil)
	}
	return nil
}

func mapError(err error) *graphsnapshot.Error {
	var graphErr *graphsnapshot.Error
	if errors.As(err, &graphErr) {
		return graphErr
	}
	switch {
	case errors.Is(err, ErrNoActiveSnapshot):
		return graphsnapshot.NewError(graphsnapshot.CodeNoActiveSnapshot, nil, nil)
	case errors.Is(err, ErrSnapshotNotReady):
		return graphsnapshot.NewError(graphsnapshot.CodeSnapshotNotReady, nil, nil)
	case errors.Is(err, ErrSnapshotNotFound):
		return graphsnapshot.NewError(graphsnapshot.CodeSnapshotNotFound, nil, nil)
	case errors.Is(err, ErrStoreUnavailable):
		return graphsnapshot.NewError(graphsnapshot.CodeGraphStoreUnavailable, nil, err)
	default:
		return graphsnapshot.NewError(graphsnapshot.CodeInternalError, nil, err)
	}
}

func traverse(ctx context.Context, view ReadView, request NormalizedTraverse) TraverseResult {
	result := TraverseResult{ResolvedSnapshotVersion: view.Version(), ContentHash: view.ContentHash(), Nodes: []TraversedNode{}, Edges: []graphsnapshot.Edge{}, TruncationReasons: []TruncationReason{}}
	nodes, _ := view.Nodes(ctx, request.StartNodeIDs)
	nodeByID := map[string]graphsnapshot.Node{}
	for _, node := range nodes {
		nodeByID[node.ID] = node
	}
	visited := map[string]int{}
	layer := append([]string{}, request.StartNodeIDs...)
	for _, id := range layer {
		visited[id] = 0
	}
	for depth := 0; ; depth++ {
		if ctx.Err() != nil {
			break
		}
		if depth == request.MaxDepth {
			if hasContinuation(ctx, view, layer, visited, request.Filter) {
				result.TruncationReasons = append(result.TruncationReasons, MaxDepthReached)
			}
			break
		}
		edges, err := view.Adjacency(ctx, layer, request.Direction)
		if err != nil {
			break
		}
		candidates := map[string]struct{}{}
		for _, edge := range edges {
			if !matchesEdge(edge, request.Filter) {
				continue
			}
			for _, current := range layer {
				neighbor, ok := neighborFor(edge, current, request.Direction)
				if !ok {
					continue
				}
				if _, seen := visited[neighbor]; !seen {
					candidates[neighbor] = struct{}{}
				}
			}
		}
		candidateIDs := sortedMapKeys(candidates)
		if len(candidateIDs) == 0 {
			break
		}
		candidateNodes, _ := view.Nodes(ctx, candidateIDs)
		candidateByID := map[string]graphsnapshot.Node{}
		for _, node := range candidateNodes {
			candidateByID[node.ID] = node
		}
		next := make([]string, 0, len(candidateIDs))
		for _, id := range candidateIDs {
			node, ok := candidateByID[id]
			if ok && matchesNode(node, request.NodeTypes) {
				next = append(next, id)
			}
		}
		if len(next) > 0 && len(visited)+len(next) > request.MaxNodes {
			result.TruncationReasons = append(result.TruncationReasons, MaxNodesReached)
			next = next[:max(0, request.MaxNodes-len(visited))]
		}
		if len(next) == 0 {
			break
		}
		layer = layer[:0]
		for _, id := range next {
			node := candidateByID[id]
			visited[id] = depth + 1
			nodeByID[id] = node
			layer = append(layer, id)
		}
		if len(layer) == 0 {
			break
		}
	}
	for id, depth := range visited {
		result.Nodes = append(result.Nodes, TraversedNode{Node: nodeByID[id], Depth: depth})
	}
	sort.Slice(result.Nodes, func(i, j int) bool {
		if result.Nodes[i].Depth != result.Nodes[j].Depth {
			return result.Nodes[i].Depth < result.Nodes[j].Depth
		}
		return result.Nodes[i].ID < result.Nodes[j].ID
	})
	allIDs := make([]string, 0, len(visited))
	for id := range visited {
		allIDs = append(allIDs, id)
	}
	edges, _ := view.Adjacency(ctx, allIDs, request.Direction)
	seenEdges := map[string]graphsnapshot.Edge{}
	for _, edge := range edges {
		if matchesEdge(edge, request.Filter) {
			if _, from := visited[edge.From]; from {
				if _, to := visited[edge.To]; to {
					seenEdges[edge.ID] = edge
				}
			}
		}
	}
	for _, edge := range seenEdges {
		result.Edges = append(result.Edges, edge)
	}
	sort.Slice(result.Edges, func(i, j int) bool { return result.Edges[i].ID < result.Edges[j].ID })
	result.Truncated = len(result.TruncationReasons) > 0
	return result
}

type pathState struct {
	source, current string
	nodes, edges    []string
	visited         map[string]struct{}
	inferred        int
	confidence      float64
}

func paths(ctx context.Context, view ReadView, request NormalizedPaths) PathsResult {
	result := PathsResult{ResolvedSnapshotVersion: view.Version(), ContentHash: view.ContentHash(), Paths: []Path{}, Nodes: []graphsnapshot.Node{}, Edges: []graphsnapshot.Edge{}, TruncationReasons: []TruncationReason{}}
	targets := map[string]struct{}{}
	for _, id := range request.TargetNodeIDs {
		targets[id] = struct{}{}
	}
	reachable := reverseReachability(ctx, view, targets, request.Filter)
	frontier := make([]pathState, 0, len(request.SourceNodeIDs))
	candidates := []Path{}
	for _, source := range request.SourceNodeIDs {
		frontier = append(frontier, pathState{source: source, current: source, nodes: []string{source}, visited: map[string]struct{}{source: {}}, confidence: 1})
		if _, target := targets[source]; target {
			candidates = append(candidates, Path{SourceNodeID: source, TargetNodeID: source, NodeIDs: []string{source}, EdgeIDs: []string{}, Confidence: 1})
		}
	}
	for depth := 0; depth < request.MaxDepth && len(frontier) > 0 && ctx.Err() == nil; depth++ {
		byCurrent := map[string][]pathState{}
		for _, state := range frontier {
			byCurrent[state.current] = append(byCurrent[state.current], state)
		}
		ids := sortedMapKeys(byCurrent)
		edges, err := view.Adjacency(ctx, ids, request.Direction)
		if err != nil {
			break
		}
		edgesByCurrent := map[string][]graphsnapshot.Edge{}
		for _, edge := range edges {
			if !matchesEdge(edge, request.Filter) {
				continue
			}
			for _, id := range ids {
				if _, ok := neighborFor(edge, id, request.Direction); ok {
					edgesByCurrent[id] = append(edgesByCurrent[id], edge)
				}
			}
		}
		next := []pathState{}
		for _, current := range ids {
			for _, state := range byCurrent[current] {
				for _, edge := range edgesByCurrent[current] {
					neighbor, ok := neighborFor(edge, current, request.Direction)
					if !ok {
						continue
					}
					if _, seen := state.visited[neighbor]; seen {
						continue
					}
					nodeRecords, _ := view.Nodes(ctx, []string{neighbor})
					if len(nodeRecords) != 1 {
						continue
					}
					if _, endpoint := targets[neighbor]; !endpoint && !matchesNode(nodeRecords[0], request.NodeTypes) {
						continue
					}
					newVisited := make(map[string]struct{}, len(state.visited)+1)
					for id := range state.visited {
						newVisited[id] = struct{}{}
					}
					newVisited[neighbor] = struct{}{}
					confidence := state.confidence
					if value, err := edge.Confidence.Float64(); err == nil && value < confidence {
						confidence = value
					}
					inferred := state.inferred
					if edge.RelationKind == "inferred" {
						inferred++
					}
					newState := pathState{source: state.source, current: neighbor, nodes: append(append([]string{}, state.nodes...), neighbor), edges: append(append([]string{}, state.edges...), edge.ID), visited: newVisited, inferred: inferred, confidence: confidence}
					if _, target := targets[neighbor]; target {
						candidates = append(candidates, toPath(newState))
					}
					if _, canReach := reachable[request.MaxDepth-len(newState.edges)][neighbor]; !canReach {
						continue
					}
					next = append(next, newState)
				}
			}
		}
		frontier = next
	}
	if len(frontier) > 0 && hasPathContinuation(ctx, view, frontier, targets, request.Filter) {
		result.Truncated = true
		result.TruncationReasons = append(result.TruncationReasons, MaxDepthReached)
	}
	sortPaths(candidates)
	if len(candidates) > request.MaxPaths {
		result.Truncated = true
		result.TruncationReasons = append(result.TruncationReasons, MaxPathsReached)
		candidates = candidates[:request.MaxPaths]
	}
	result.Paths = candidates
	nodeIDs, edgeIDs := []string{}, []string{}
	for _, path := range candidates {
		nodeIDs = append(nodeIDs, path.NodeIDs...)
		edgeIDs = append(edgeIDs, path.EdgeIDs...)
	}
	result.Nodes, _ = view.Nodes(ctx, nodeIDs)
	allEdges, _ := view.Adjacency(ctx, nodeIDs, DirectionBoth)
	wanted := map[string]struct{}{}
	for _, id := range edgeIDs {
		wanted[id] = struct{}{}
	}
	for _, edge := range allEdges {
		if _, ok := wanted[edge.ID]; ok {
			result.Edges = append(result.Edges, edge)
		}
	}
	sort.Slice(result.Nodes, func(i, j int) bool { return result.Nodes[i].ID < result.Nodes[j].ID })
	sort.Slice(result.Edges, func(i, j int) bool { return result.Edges[i].ID < result.Edges[j].ID })
	return result
}

func hasPathContinuation(ctx context.Context, view ReadView, states []pathState, targets map[string]struct{}, filter Filter) bool {
	byCurrent := map[string][]pathState{}
	for _, state := range states {
		byCurrent[state.current] = append(byCurrent[state.current], state)
	}
	ids := sortedMapKeys(byCurrent)
	edges, err := view.Adjacency(ctx, ids, filter.Direction)
	if err != nil {
		return false
	}
	for _, current := range ids {
		for _, state := range byCurrent[current] {
			for _, edge := range edges {
				if !matchesEdge(edge, filter) {
					continue
				}
				neighbor, ok := neighborFor(edge, current, filter.Direction)
				if !ok {
					continue
				}
				if _, visited := state.visited[neighbor]; visited {
					continue
				}
				if _, target := targets[neighbor]; target {
					return true
				}
			}
		}
	}
	return false
}

// reverseReachability is deliberately an over-approximation: it ignores a
// particular path state's visited set, so it can retain extra work but never
// removes a valid simple path before the bounded forward search sees it.
func reverseReachability(ctx context.Context, view ReadView, targets map[string]struct{}, filter Filter) []map[string]struct{} {
	reachable := make([]map[string]struct{}, filter.MaxDepth+1)
	reachable[0] = map[string]struct{}{}
	for target := range targets {
		reachable[0][target] = struct{}{}
	}
	direction := filter.Direction
	if direction == DirectionOutgoing {
		direction = DirectionIncoming
	} else if direction == DirectionIncoming {
		direction = DirectionOutgoing
	}
	for depth := 1; depth <= filter.MaxDepth; depth++ {
		reachable[depth] = make(map[string]struct{}, len(reachable[depth-1]))
		for id := range reachable[depth-1] {
			reachable[depth][id] = struct{}{}
		}
		ids := sortedMapKeys(reachable[depth-1])
		edges, err := view.Adjacency(ctx, ids, direction)
		if err != nil {
			continue
		}
		for _, edge := range edges {
			if !matchesEdge(edge, filter) {
				continue
			}
			for _, current := range ids {
				neighbor, ok := neighborFor(edge, current, direction)
				if ok {
					reachable[depth][neighbor] = struct{}{}
				}
			}
		}
	}
	return reachable
}

func toPath(state pathState) Path {
	return Path{SourceNodeID: state.source, TargetNodeID: state.current, NodeIDs: state.nodes, EdgeIDs: state.edges, HopCount: len(state.edges), InferredEdgeCount: state.inferred, Confidence: state.confidence}
}
func sortPaths(paths []Path) {
	sort.Slice(paths, func(i, j int) bool {
		a, b := paths[i], paths[j]
		if a.HopCount != b.HopCount {
			return a.HopCount < b.HopCount
		}
		if a.InferredEdgeCount != b.InferredEdgeCount {
			return a.InferredEdgeCount < b.InferredEdgeCount
		}
		if a.Confidence != b.Confidence {
			return a.Confidence > b.Confidence
		}
		if c := compareStrings(a.EdgeIDs, b.EdgeIDs); c != 0 {
			return c < 0
		}
		return compareStrings(a.NodeIDs, b.NodeIDs) < 0
	})
}
func compareStrings(a, b []string) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	if len(a) < len(b) {
		return -1
	}
	if len(a) > len(b) {
		return 1
	}
	return 0
}
func matchesEdge(edge graphsnapshot.Edge, filter Filter) bool {
	return contains(filter.RelationshipKinds, edge.RelationKind) && (len(filter.EdgeTypes) == 0 || contains(filter.EdgeTypes, edge.Type))
}

// MatchesEdge exposes the constrained-query filter semantics to graph
// retrieval without giving it a second interpretation of stored edges.
func MatchesEdge(edge graphsnapshot.Edge, edgeTypes, relationshipKinds []string) bool {
	return matchesEdge(edge, Filter{EdgeTypes: edgeTypes, RelationshipKinds: relationshipKinds})
}
func matchesNode(node graphsnapshot.Node, types []string) bool {
	return len(types) == 0 || contains(types, node.Type)
}

// MatchesNode exposes the constrained-query exact type-filter semantics.
func MatchesNode(node graphsnapshot.Node, types []string) bool { return matchesNode(node, types) }
func contains(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}
func neighborFor(edge graphsnapshot.Edge, current string, direction Direction) (string, bool) {
	switch direction {
	case DirectionOutgoing:
		if edge.From == current {
			return edge.To, true
		}
	case DirectionIncoming:
		if edge.To == current {
			return edge.From, true
		}
	case DirectionBoth:
		if edge.From == current {
			return edge.To, true
		}
		if edge.To == current {
			return edge.From, true
		}
	}
	return "", false
}

// NeighborFor preserves the stored edge orientation used by traverse/paths.
func NeighborFor(edge graphsnapshot.Edge, current string, direction Direction) (string, bool) {
	return neighborFor(edge, current, direction)
}
func sortedMapKeys[V any](values map[string]V) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func sortedUnique(values []string) []string {
	seen := map[string]struct{}{}
	for _, value := range values {
		seen[value] = struct{}{}
	}
	return sortedMapKeys(seen)
}
func hasContinuation(ctx context.Context, view ReadView, layer []string, visited map[string]int, filter Filter) bool {
	edges, err := view.Adjacency(ctx, layer, filter.Direction)
	if err != nil {
		return false
	}
	for _, edge := range edges {
		if !matchesEdge(edge, filter) {
			continue
		}
		for _, id := range layer {
			if neighbor, ok := neighborFor(edge, id, filter.Direction); ok {
				if _, seen := visited[neighbor]; !seen {
					nodes, err := view.Nodes(ctx, []string{neighbor})
					if err == nil && len(nodes) == 1 && matchesNode(nodes[0], filter.NodeTypes) {
						return true
					}
				}
			}
		}
	}
	return false
}
