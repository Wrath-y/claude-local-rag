package graphretrieval

import (
	"context"
	"math"
	"sort"

	"github.com/Wrath-y/local-rag/internal/graphquery"
	"github.com/Wrath-y/local-rag/internal/graphsnapshot"
)

type FusedSeed struct {
	NodeID     string
	SearchText string
	Scores     Scores
}

// Fuse applies the fixed, one-based RRF rule. It intentionally accepts raw
// native scores without normalizing or weighting them.
func Fuse(bm25, vector []Candidate, limit int) []FusedSeed {
	byID := map[string]FusedSeed{}
	add := func(candidates []Candidate, bm25Stage bool) {
		for _, candidate := range candidates {
			seed := byID[candidate.NodeID]
			seed.NodeID = candidate.NodeID
			if seed.SearchText == "" {
				seed.SearchText = candidate.SearchText
			}
			rank, score := candidate.Rank, candidate.RawScore
			if bm25Stage {
				seed.Scores.BM25Rank, seed.Scores.BM25Score = &rank, &score
			} else {
				seed.Scores.VectorRank, seed.Scores.VectorScore = &rank, &score
			}
			seed.Scores.RRFScore += 1 / float64(RRFK+candidate.Rank)
			byID[candidate.NodeID] = seed
		}
	}
	add(bm25, true)
	add(vector, false)
	result := make([]FusedSeed, 0, len(byID))
	for _, seed := range byID {
		result = append(result, seed)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Scores.RRFScore != result[j].Scores.RRFScore {
			return result[i].Scores.RRFScore > result[j].Scores.RRFScore
		}
		return result[i].NodeID < result[j].NodeID
	})
	if limit >= 0 && len(result) > limit {
		return result[:limit]
	}
	return result
}

type expansionRoute struct {
	seed       FusedSeed
	current    string
	nodeIDs    []string
	edges      []graphsnapshot.Edge
	confidence float64
}

func (r expansionRoute) hopCount() int { return len(r.edges) }
func (r expansionRoute) score() float64 {
	// Keep this two-step binary64 order aligned with the published contract.
	score := r.seed.Scores.RRFScore / float64(1+r.hopCount())
	return score * r.confidence
}

// Expand applies deterministic outgoing, simple-path graph expansion. It
// delegates exact filter and orientation semantics to graphquery helpers.
func Expand(ctx context.Context, view ReadView, seeds []FusedSeed, request NormalizedRequest) ([]Result, error) {
	if len(seeds) == 0 {
		return []Result{}, nil
	}
	seedIDs := make([]string, 0, len(seeds))
	for _, seed := range seeds {
		seedIDs = append(seedIDs, seed.NodeID)
	}
	seedNodes, err := view.Nodes(ctx, seedIDs)
	if err != nil {
		return nil, err
	}
	nodes := map[string]graphsnapshot.Node{}
	for _, node := range seedNodes {
		nodes[node.ID] = node
	}
	frontier := make([]expansionRoute, 0, len(seeds))
	best := map[string]expansionRoute{}
	for _, seed := range seeds {
		if node, found := nodes[seed.NodeID]; found && graphquery.MatchesNode(node, request.NodeTypes) {
			route := expansionRoute{seed: seed, current: seed.NodeID, nodeIDs: []string{seed.NodeID}, confidence: 1}
			frontier = append(frontier, route)
			best[seed.NodeID] = route
		}
	}
	for depth := 0; depth < request.GraphDepth && len(frontier) > 0 && ctx.Err() == nil; depth++ {
		currentIDs := map[string]struct{}{}
		for _, route := range frontier {
			currentIDs[route.current] = struct{}{}
		}
		edges, err := view.Adjacency(ctx, sortedKeys(currentIDs), graphquery.DirectionOutgoing)
		if err != nil {
			return nil, err
		}
		byFrom := map[string][]graphsnapshot.Edge{}
		for _, edge := range edges {
			if graphquery.MatchesEdge(edge, request.EdgeTypes, request.RelationshipKinds) {
				byFrom[edge.From] = append(byFrom[edge.From], edge)
			}
		}
		provisional := []expansionRoute{}
		candidateIDs := map[string]struct{}{}
		for _, route := range frontier {
			for _, edge := range byFrom[route.current] {
				neighbor, ok := graphquery.NeighborFor(edge, route.current, graphquery.DirectionOutgoing)
				if !ok || containsID(route.nodeIDs, neighbor) {
					continue
				}
				confidence := route.confidence
				if parsed, parseErr := edge.Confidence.Float64(); parseErr == nil && parsed < confidence {
					confidence = parsed
				}
				provisional = append(provisional, expansionRoute{seed: route.seed, current: neighbor, nodeIDs: appendCopy(route.nodeIDs, neighbor), edges: appendEdge(route.edges, edge), confidence: confidence})
				candidateIDs[neighbor] = struct{}{}
			}
		}
		candidateNodes, err := view.Nodes(ctx, sortedKeys(candidateIDs))
		if err != nil {
			return nil, err
		}
		for _, node := range candidateNodes {
			nodes[node.ID] = node
		}
		next := make([]expansionRoute, 0, len(provisional))
		for _, route := range provisional {
			node, found := nodes[route.current]
			if !found || !graphquery.MatchesNode(node, request.NodeTypes) {
				continue
			}
			if prior, exists := best[route.current]; !exists || routeBetter(route, prior) {
				best[route.current] = route
			}
			next = append(next, route)
		}
		frontier = next
	}
	routes := make([]expansionRoute, 0, len(best))
	for _, route := range best {
		routes = append(routes, route)
	}
	sort.Slice(routes, func(i, j int) bool { return routeBetter(routes[i], routes[j]) })
	if len(routes) > request.ResultLimit {
		routes = routes[:request.ResultLimit]
	}
	result := make([]Result, 0, len(routes))
	for index, route := range routes {
		pathNodes, err := view.Nodes(ctx, route.nodeIDs)
		if err != nil {
			return nil, err
		}
		node := nodes[route.current]
		scores := route.seed.Scores
		scores.GraphScore = route.score()
		result = append(result, Result{
			Rank: index + 1, Node: node, CitationText: node.Text, HopCount: route.hopCount(), PathConfidence: route.confidence, Scores: scores,
			Evidence: Evidence{Seed: &SeedEvidence{NodeID: route.seed.NodeID, SearchText: route.seed.SearchText}, Path: buildPathEvidence(route.nodeIDs, route.edges, pathNodes)},
		})
	}
	return result, nil
}

func buildPathEvidence(nodeIDs []string, edges []graphsnapshot.Edge, nodes []graphsnapshot.Node) *PathEvidence {
	evidence := &PathEvidence{NodeIDs: append([]string(nil), nodeIDs...), Nodes: nodes, Edges: append([]graphsnapshot.Edge(nil), edges...), ExplicitEdges: []graphsnapshot.Edge{}, InferredEdges: []graphsnapshot.Edge{}}
	for _, edge := range edges {
		evidence.EdgeIDs = append(evidence.EdgeIDs, edge.ID)
		if edge.RelationKind == "inferred" {
			evidence.InferredEdges = append(evidence.InferredEdges, edge)
		} else {
			evidence.ExplicitEdges = append(evidence.ExplicitEdges, edge)
		}
	}
	return evidence
}

func routeBetter(left, right expansionRoute) bool {
	if math.Abs(left.score()-right.score()) > 0 {
		return left.score() > right.score()
	}
	if left.hopCount() != right.hopCount() {
		return left.hopCount() < right.hopCount()
	}
	if left.seed.NodeID != right.seed.NodeID {
		return left.seed.NodeID < right.seed.NodeID
	}
	if compareIDs(edgeIDs(left.edges), edgeIDs(right.edges)) != 0 {
		return compareIDs(edgeIDs(left.edges), edgeIDs(right.edges)) < 0
	}
	return compareIDs(left.nodeIDs, right.nodeIDs) < 0
}

func edgeIDs(edges []graphsnapshot.Edge) []string {
	ids := make([]string, len(edges))
	for i := range edges {
		ids[i] = edges[i].ID
	}
	return ids
}
func compareIDs(left, right []string) int {
	for i := 0; i < len(left) && i < len(right); i++ {
		if left[i] < right[i] {
			return -1
		}
		if left[i] > right[i] {
			return 1
		}
	}
	if len(left) < len(right) {
		return -1
	}
	if len(left) > len(right) {
		return 1
	}
	return 0
}
func containsID(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}
func appendCopy(ids []string, value string) []string {
	result := append([]string(nil), ids...)
	return append(result, value)
}
func appendEdge(edges []graphsnapshot.Edge, edge graphsnapshot.Edge) []graphsnapshot.Edge {
	result := append([]graphsnapshot.Edge(nil), edges...)
	return append(result, edge)
}
func sortedKeys(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
