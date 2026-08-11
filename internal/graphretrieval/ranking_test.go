package graphretrieval

import (
	"context"
	"math"
	"sort"
	"testing"

	"github.com/Wrath-y/local-rag/internal/graphquery"
	"github.com/Wrath-y/local-rag/internal/graphsnapshot"
)

type rankingReadView struct {
	nodes map[string]graphsnapshot.Node
	edges []graphsnapshot.Edge
}

func (v rankingReadView) Version() string     { return "v1" }
func (v rankingReadView) ContentHash() string { return "hash" }
func (v rankingReadView) Generation(Stage) (*GenerationIdentity, StageOutcome) {
	return nil, StageUnavailable
}
func (v rankingReadView) BM25(context.Context, string, []string, int) ([]Candidate, StageOutcome, error) {
	return nil, StageUnavailable, nil
}
func (v rankingReadView) Vector(context.Context, []float32, []string, int) ([]Candidate, StageOutcome, error) {
	return nil, StageUnavailable, nil
}
func (v rankingReadView) Nodes(_ context.Context, ids []string) ([]graphsnapshot.Node, error) {
	result := []graphsnapshot.Node{}
	for _, id := range ids {
		if node, ok := v.nodes[id]; ok {
			result = append(result, node)
		}
	}
	return result, nil
}
func (v rankingReadView) Adjacency(_ context.Context, ids []string, direction graphquery.Direction) ([]graphsnapshot.Edge, error) {
	allowed := map[string]struct{}{}
	for _, id := range ids {
		allowed[id] = struct{}{}
	}
	result := []graphsnapshot.Edge{}
	for _, edge := range v.edges {
		if direction == graphquery.DirectionOutgoing {
			if _, ok := allowed[edge.From]; ok {
				result = append(result, edge)
			}
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func TestFuseUsesOneBasedUnweightedRRFAndNodeTieBreak(t *testing.T) {
	seeds := Fuse(
		[]Candidate{{NodeID: "b", SearchText: "b", Rank: 1, RawScore: -3}, {NodeID: "a", SearchText: "a", Rank: 2, RawScore: -2}},
		[]Candidate{{NodeID: "a", SearchText: "a", Rank: 1, RawScore: 0.1}}, 10,
	)
	if len(seeds) != 2 || seeds[0].NodeID != "a" || seeds[0].Scores.BM25Rank == nil || seeds[0].Scores.VectorRank == nil {
		t.Fatalf("seeds=%+v", seeds)
	}
	want := 1.0/62 + 1.0/61
	if math.Abs(seeds[0].Scores.RRFScore-want) > 1e-15 {
		t.Fatalf("rrf=%0.17f want=%0.17f", seeds[0].Scores.RRFScore, want)
	}
	ties := Fuse([]Candidate{{NodeID: "z", Rank: 1}, {NodeID: "a", Rank: 1}}, nil, 10)
	if ties[0].NodeID != "a" {
		t.Fatalf("tie order=%+v", ties)
	}
}

func TestExpandUsesSimplePathsConfidenceAndInferredOptIn(t *testing.T) {
	nodes := map[string]graphsnapshot.Node{}
	for _, id := range []string{"seed", "one", "two", "three"} {
		nodes[id] = graphsnapshot.Node{ID: id, Type: "kind", Text: id, Properties: []byte(`{}`), Provenance: []byte(`{}`)}
	}
	view := rankingReadView{nodes: nodes, edges: []graphsnapshot.Edge{
		{ID: "e1", From: "seed", To: "one", RelationKind: "explicit", Confidence: "1", Properties: []byte(`{}`), Provenance: []byte(`{}`)},
		{ID: "e2", From: "one", To: "two", RelationKind: "inferred", Confidence: "0.6", Properties: []byte(`{}`), Provenance: []byte(`{}`)},
		{ID: "e3", From: "two", To: "three", RelationKind: "explicit", Confidence: "0.9", Properties: []byte(`{}`), Provenance: []byte(`{}`)},
		{ID: "cycle", From: "three", To: "seed", RelationKind: "explicit", Confidence: "1", Properties: []byte(`{}`), Provenance: []byte(`{}`)},
	}}
	seed := FusedSeed{NodeID: "seed", SearchText: "seed", Scores: Scores{RRFScore: .02}}
	explicit, err := Expand(context.Background(), view, []FusedSeed{seed}, NormalizedRequest{Filter: Filter{RelationshipKinds: []string{"explicit"}}, ResultLimit: 10, GraphDepth: 3})
	if err != nil || len(explicit) != 2 || explicit[0].Node.ID != "seed" || explicit[1].Node.ID != "one" {
		t.Fatalf("explicit=%+v err=%v", explicit, err)
	}
	all, err := Expand(context.Background(), view, []FusedSeed{seed}, NormalizedRequest{Filter: Filter{RelationshipKinds: []string{"explicit", "inferred"}}, ResultLimit: 10, GraphDepth: 3})
	if err != nil || len(all) != 4 {
		t.Fatalf("all=%+v err=%v", all, err)
	}
	var two Result
	for _, result := range all {
		if result.Node.ID == "two" {
			two = result
		}
	}
	if two.HopCount != 2 || math.Abs(two.Scores.GraphScore-(.02/3*.6)) > 1e-15 || len(two.Evidence.Path.InferredEdges) != 1 {
		t.Fatalf("two=%+v", two)
	}
}

func TestExpandDepthZeroAndParallelEdgeTieBreakAreDeterministic(t *testing.T) {
	view := rankingReadView{nodes: map[string]graphsnapshot.Node{
		"seed": {ID: "seed", Type: "kind", Text: "seed", Properties: []byte(`{}`), Provenance: []byte(`{}`)},
		"next": {ID: "next", Type: "kind", Text: "next", Properties: []byte(`{}`), Provenance: []byte(`{}`)},
	}, edges: []graphsnapshot.Edge{
		{ID: "edge-z", From: "seed", To: "next", RelationKind: "explicit", Confidence: "1", Properties: []byte(`{}`), Provenance: []byte(`{}`)},
		{ID: "edge-a", From: "seed", To: "next", RelationKind: "explicit", Confidence: "1", Properties: []byte(`{}`), Provenance: []byte(`{}`)},
	}}
	seed := FusedSeed{NodeID: "seed", SearchText: "seed", Scores: Scores{RRFScore: .02}}
	depthZero, err := Expand(context.Background(), view, []FusedSeed{seed}, NormalizedRequest{Filter: Filter{RelationshipKinds: []string{"explicit"}}, ResultLimit: 10, GraphDepth: 0})
	if err != nil || len(depthZero) != 1 || depthZero[0].Node.ID != "seed" {
		t.Fatalf("depth zero=%+v err=%v", depthZero, err)
	}
	results, err := Expand(context.Background(), view, []FusedSeed{seed}, NormalizedRequest{Filter: Filter{RelationshipKinds: []string{"explicit"}}, ResultLimit: 10, GraphDepth: 1})
	if err != nil || len(results) != 2 || results[1].Node.ID != "next" || results[1].Evidence.Path.EdgeIDs[0] != "edge-a" {
		t.Fatalf("parallel edge result=%+v err=%v", results, err)
	}
}
