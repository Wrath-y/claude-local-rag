package graphquery

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/Wrath-y/local-rag/internal/graphsnapshot"
)

type fakeRepository struct {
	view ReadView
	err  error
}

func (r fakeRepository) WithRead(_ context.Context, _, _ string, fn func(ReadView) error) error {
	if r.err != nil {
		return r.err
	}
	return fn(r.view)
}

type fakeView struct {
	version, hash string
	nodes         map[string]graphsnapshot.Node
	edges         []graphsnapshot.Edge
}

func (v fakeView) Version() string     { return v.version }
func (v fakeView) ContentHash() string { return v.hash }
func (v fakeView) Nodes(_ context.Context, ids []string) ([]graphsnapshot.Node, error) {
	result := []graphsnapshot.Node{}
	for _, id := range ids {
		if n, ok := v.nodes[id]; ok {
			result = append(result, n)
		}
	}
	return result, nil
}
func (v fakeView) Adjacency(_ context.Context, ids []string, direction Direction) ([]graphsnapshot.Edge, error) {
	wanted := map[string]struct{}{}
	for _, id := range ids {
		wanted[id] = struct{}{}
	}
	result := []graphsnapshot.Edge{}
	seen := map[string]struct{}{}
	for _, e := range v.edges {
		include := false
		switch direction {
		case DirectionOutgoing:
			_, include = wanted[e.From]
		case DirectionIncoming:
			_, include = wanted[e.To]
		case DirectionBoth:
			_, a := wanted[e.From]
			_, b := wanted[e.To]
			include = a || b
		}
		if include {
			if _, ok := seen[e.ID]; !ok {
				seen[e.ID] = struct{}{}
				result = append(result, e)
			}
		}
	}
	return result, nil
}

func queryNode(id, typ string) graphsnapshot.Node {
	return graphsnapshot.Node{ID: id, Type: typ, Label: id, Text: id, Properties: json.RawMessage(`{}`), Provenance: json.RawMessage(`{}`)}
}
func queryEdge(id, from, to, kind string, confidence string) graphsnapshot.Edge {
	return graphsnapshot.Edge{ID: id, From: from, To: to, Type: "depends_on", RelationKind: kind, Confidence: json.Number(confidence), Properties: json.RawMessage(`{}`), Provenance: json.RawMessage(`{}`)}
}

func TestTraverseUsesSortedLayerPrefixAndExplicitDefault(t *testing.T) {
	view := fakeView{version: "v1", hash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", nodes: map[string]graphsnapshot.Node{"a": queryNode("a", "service"), "b": queryNode("b", "service"), "c": queryNode("c", "service"), "d": queryNode("d", "service")}, edges: []graphsnapshot.Edge{queryEdge("z", "a", "d", "explicit", "0.5"), queryEdge("a", "a", "c", "explicit", "0.5"), queryEdge("b", "a", "b", "inferred", "0.9")}}
	maxNodes := 2
	result, err := Service{Repository: fakeRepository{view: view}}.Traverse(context.Background(), "tenant", TraverseRequest{StartNodeIDs: []string{"a"}, MaxNodes: &maxNodes})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Nodes) != 2 || result.Nodes[1].ID != "c" {
		t.Fatalf("nodes=%#v", result.Nodes)
	}
	if !result.Truncated || len(result.TruncationReasons) != 1 || result.TruncationReasons[0] != MaxNodesReached {
		t.Fatalf("truncation=%#v", result)
	}
	if len(result.Edges) != 1 || result.Edges[0].ID != "a" {
		t.Fatalf("edges=%#v", result.Edges)
	}
}

func TestPathsRanksZeroHopThenExplicitAndPreservesEvidence(t *testing.T) {
	view := fakeView{version: "v1", hash: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", nodes: map[string]graphsnapshot.Node{"a": queryNode("a", "service"), "b": queryNode("b", "service"), "c": queryNode("c", "service")}, edges: []graphsnapshot.Edge{queryEdge("z", "a", "c", "inferred", "0.9"), queryEdge("a", "a", "b", "explicit", "0.3"), queryEdge("b", "b", "c", "explicit", "0.8")}}
	depth := 2
	result, err := Service{Repository: fakeRepository{view: view}}.Paths(context.Background(), "tenant", PathsRequest{SourceNodeIDs: []string{"a", "b"}, TargetNodeIDs: []string{"b", "c"}, RelationshipKinds: []string{"explicit", "inferred"}, MaxDepth: &depth})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Paths) != 5 {
		t.Fatalf("paths=%#v", result.Paths)
	}
	if result.Paths[0].HopCount != 0 || result.Paths[0].SourceNodeID != "b" {
		t.Fatalf("zero hop=%#v", result.Paths[0])
	}
	if result.Paths[1].EdgeIDs[0] != "b" || result.Paths[1].Confidence != 0.8 || result.Paths[3].EdgeIDs[0] != "z" || result.Paths[3].InferredEdgeCount != 1 {
		t.Fatalf("one hop ranking=%#v", result.Paths)
	}
	if len(result.Edges) != 3 || result.Edges[0].ID != "a" {
		t.Fatalf("evidence=%#v", result.Edges)
	}
}

func TestNormalizationRejectsEmptyAndLimitButCanonicalizesSets(t *testing.T) {
	depth, nodes := 7, 2
	if _, err := NormalizeTraverse(TraverseRequest{StartNodeIDs: []string{"a"}, MaxDepth: &depth}); err == nil || err.Code != graphsnapshot.CodeLimitExceeded {
		t.Fatalf("limit err=%#v", err)
	}
	if _, err := NormalizeTraverse(TraverseRequest{StartNodeIDs: []string{"a"}, RelationshipKinds: []string{}}); err == nil || err.Code != graphsnapshot.CodeInvalidGraphQuery {
		t.Fatalf("empty kinds=%#v", err)
	}
	got, err := NormalizeTraverse(TraverseRequest{StartNodeIDs: []string{"b", "a", "b"}, RelationshipKinds: []string{"inferred", "explicit", "explicit"}, MaxNodes: &nodes})
	if err != nil || got.StartNodeIDs[0] != "a" || len(got.RelationshipKinds) != 2 {
		t.Fatalf("normalized=%#v err=%v", got, err)
	}
}

func TestDecodeRejectsDuplicateAndUnknownJSONMembers(t *testing.T) {
	if _, err := DecodeTraverse([]byte(`{"start_node_ids":["a"],"start_node_ids":["b"]}`)); err == nil {
		t.Fatal("accepted duplicate member")
	}
	if _, err := DecodePaths([]byte(`{"source_node_ids":["a"],"target_node_ids":["b"],"unknown":true}`)); err == nil {
		t.Fatal("accepted unknown member")
	}
}

// BenchmarkTraverse10k100k is an opt-in generated capacity fixture. It keeps
// the production limits and deterministic traversal path intact while making
// the 10k-node/100k-edge target measurable on a reference machine.
func BenchmarkTraverse10k100k(b *testing.B) {
	nodes := make(map[string]graphsnapshot.Node, 10000)
	edges := make([]graphsnapshot.Edge, 0, 100000)
	for i := 0; i < 10000; i++ {
		nodes[fmt.Sprintf("n-%05d", i)] = queryNode(fmt.Sprintf("n-%05d", i), "node")
	}
	for i := 0; i < 100000; i++ {
		from := fmt.Sprintf("n-%05d", i%10000)
		to := fmt.Sprintf("n-%05d", (i+1)%10000)
		edges = append(edges, queryEdge(fmt.Sprintf("e-%06d", i), from, to, "explicit", "1"))
	}
	service := Service{Repository: fakeRepository{view: fakeView{version: "capacity", hash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", nodes: nodes, edges: edges}}}
	depth, limit := 3, 500
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := service.Traverse(context.Background(), "capacity", TraverseRequest{StartNodeIDs: []string{"n-00000"}, MaxDepth: &depth, MaxNodes: &limit}); err != nil {
			b.Fatal(err)
		}
	}
}
