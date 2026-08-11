package graphretrieval

import (
	"context"
	"fmt"
	"testing"

	"github.com/Wrath-y/local-rag/internal/graphsnapshot"
)

// BenchmarkExpandGeneratedMaximumCase records the accepted 10k-node/100k-edge
// boundary with 100 seeds/results and depth three. It is intentionally a
// benchmark, not a latency promise.
func BenchmarkExpandGeneratedMaximumCase(b *testing.B) {
	const nodesCount, edgesPerNode = 10_000, 10
	nodes := make(map[string]graphsnapshot.Node, nodesCount)
	edges := make([]graphsnapshot.Edge, 0, nodesCount*edgesPerNode)
	for index := 0; index < nodesCount; index++ {
		id := fmt.Sprintf("n%05d", index)
		nodes[id] = graphsnapshot.Node{ID: id, Type: "kind", Text: id, Properties: []byte(`{}`), Provenance: []byte(`{}`)}
		for offset := 1; offset <= edgesPerNode; offset++ {
			edges = append(edges, graphsnapshot.Edge{ID: fmt.Sprintf("e%05d-%02d", index, offset), From: id, To: fmt.Sprintf("n%05d", (index+offset)%nodesCount), RelationKind: "explicit", Confidence: "1", Properties: []byte(`{}`), Provenance: []byte(`{}`)})
		}
	}
	seeds := make([]FusedSeed, 100)
	for index := range seeds {
		seeds[index] = FusedSeed{NodeID: fmt.Sprintf("n%05d", index), SearchText: fmt.Sprintf("n%05d", index), Scores: Scores{RRFScore: 1.0 / float64(RRFK+index+1)}}
	}
	view := rankingReadView{nodes: nodes, edges: edges}
	request := NormalizedRequest{Filter: Filter{RelationshipKinds: []string{"explicit"}}, SeedLimit: 100, ResultLimit: 100, GraphDepth: 3}
	b.ReportAllocs()
	for iteration := 0; iteration < b.N; iteration++ {
		if _, err := Expand(context.Background(), view, seeds, request); err != nil {
			b.Fatal(err)
		}
	}
}
