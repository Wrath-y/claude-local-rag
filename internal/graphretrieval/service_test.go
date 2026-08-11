package graphretrieval

import (
	"context"
	"testing"

	"github.com/Wrath-y/local-rag/internal/graphquery"
	"github.com/Wrath-y/local-rag/internal/graphsnapshot"
)

type serviceRepository struct{ view ReadView }

func (r serviceRepository) WithRetrievalRead(_ context.Context, _, _ string, fn func(ReadView) error) error {
	return fn(r.view)
}

type serviceView struct {
	fts, vector           *GenerationIdentity
	ftsState, vectorState StageOutcome
	bm25, vectors         []Candidate
}

func (v serviceView) Version() string     { return "v1" }
func (v serviceView) ContentHash() string { return "hash" }
func (v serviceView) Generation(stage Stage) (*GenerationIdentity, StageOutcome) {
	if stage == StageBM25 {
		return v.fts, v.ftsState
	}
	return v.vector, v.vectorState
}
func (v serviceView) BM25(context.Context, string, []string, int) ([]Candidate, StageOutcome, error) {
	return v.bm25, v.ftsState, nil
}
func (v serviceView) Vector(context.Context, []float32, []string, int) ([]Candidate, StageOutcome, error) {
	return v.vectors, v.vectorState, nil
}
func (v serviceView) Nodes(_ context.Context, ids []string) ([]graphsnapshot.Node, error) {
	result := make([]graphsnapshot.Node, 0, len(ids))
	for _, id := range ids {
		result = append(result, graphsnapshot.Node{ID: id, Type: "kind", Text: id, Properties: []byte(`{}`), Provenance: []byte(`{}`)})
	}
	return result, nil
}
func (serviceView) Adjacency(context.Context, []string, graphquery.Direction) ([]graphsnapshot.Edge, error) {
	return []graphsnapshot.Edge{}, nil
}

type rerankFake struct{ output []RerankResult }

func (r rerankFake) Rerank(context.Context, string, []string, int) ([]RerankResult, error) {
	return r.output, nil
}

func usableView() serviceView {
	dimension := 2
	return serviceView{
		fts:         &GenerationIdentity{Component: "fts", Generation: "fts", Algorithm: "fts5", ContentDigest: "digest"},
		vector:      &GenerationIdentity{Component: "vector", Generation: "vector", Provider: "provider", Model: "model", Dimensions: &dimension, ContentDigest: "digest"},
		ftsState:    StageUsed,
		vectorState: StageUsed,
		bm25:        []Candidate{{NodeID: "a", SearchText: "a", Rank: 1, RawScore: -1}},
		vectors:     []Candidate{{NodeID: "b", SearchText: "b", Rank: 1, RawScore: 0}},
	}
}

func retrievalService(view serviceView) Service {
	return Service{Repository: serviceRepository{view: view}, Embedder: EmbeddingAdapter{Provider: &embeddingFake{}, Identity: ProviderIdentity{Provider: "provider", Model: "model"}}}
}

func TestServiceUsesHybridAndBM25FallbackWithoutLegacyCalls(t *testing.T) {
	service := retrievalService(usableView())
	result, graphErr := service.Retrieve(context.Background(), "namespace", Request{Query: "q", GraphDepth: pointer(0)})
	if graphErr != nil || result.ModeUsed != "hybrid" || result.Degraded || len(result.Results) != 2 {
		t.Fatalf("result=%+v error=%v", result, graphErr)
	}
	view := usableView()
	view.vectorState = StageUnavailable
	result, graphErr = retrievalService(view).Retrieve(context.Background(), "namespace", Request{Query: "q", GraphDepth: pointer(0)})
	if graphErr != nil || result.ModeUsed != "bm25_only" || !result.Degraded || len(result.Warnings) != 1 || result.Warnings[0].Stage != StageVector {
		t.Fatalf("fallback result=%+v error=%v", result, graphErr)
	}
}

func TestServiceStableUnavailableEvictionAndRerankFallback(t *testing.T) {
	view := usableView()
	view.ftsState, view.vectorState = StagePermanentFailure, StagePermanentFailure
	if _, graphErr := retrievalService(view).Retrieve(context.Background(), "namespace", Request{Query: "q"}); graphErr == nil || graphErr.Code != graphsnapshot.CodeRetrievalUnavailable || graphErr.Retryable {
		t.Fatalf("unavailable error=%v", graphErr)
	}
	view = usableView()
	view.ftsState, view.vectorState = StageIndexEvicted, StageIndexEvicted
	if _, graphErr := retrievalService(view).Retrieve(context.Background(), "namespace", Request{Query: "q"}); graphErr == nil || graphErr.Code != graphsnapshot.CodeSnapshotIndexNotReady || graphErr.Details["rebuild_required"] != true {
		t.Fatalf("eviction error=%v", graphErr)
	}
	service := retrievalService(usableView())
	service.RerankEnabled = true
	service.Reranker = RerankAdapter{Provider: rerankFake{output: []RerankResult{{Index: 0, Score: 1}}}}
	result, graphErr := service.Retrieve(context.Background(), "namespace", Request{Query: "q", GraphDepth: pointer(0)})
	if graphErr != nil || !result.Degraded || result.Rerank != StagePermanentFailure || len(result.Warnings) != 1 || result.Warnings[0].Code != WarningRerankInvalid {
		t.Fatalf("rerank fallback result=%+v error=%v", result, graphErr)
	}
}

func TestServiceBaseStageMatrixAndDisabledRerank(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		bm25      StageOutcome
		vector    StageOutcome
		mode      string
		wantError bool
	}{
		{"hybrid", StageUsed, StageUsed, "hybrid", false},
		{"bm25 only", StageUsed, StageUnavailable, "bm25_only", false},
		{"vector only", StageUnavailable, StageUsed, "vector_only", false},
		{"no stages", StagePermanentFailure, StagePermanentFailure, "", true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			view := usableView()
			view.ftsState, view.vectorState = testCase.bm25, testCase.vector
			result, graphErr := retrievalService(view).Retrieve(context.Background(), "namespace", Request{Query: "q", GraphDepth: pointer(0)})
			if testCase.wantError {
				if graphErr == nil || graphErr.Code != graphsnapshot.CodeRetrievalUnavailable {
					t.Fatalf("error=%v", graphErr)
				}
				return
			}
			if graphErr != nil || result.ModeUsed != testCase.mode || result.Rerank != StageSkipped {
				t.Fatalf("result=%+v error=%v", result, graphErr)
			}
		})
	}
	view := usableView()
	view.bm25, view.vectors = []Candidate{}, []Candidate{}
	result, graphErr := retrievalService(view).Retrieve(context.Background(), "namespace", Request{Query: "q", GraphDepth: pointer(0)})
	if graphErr != nil || result.ModeUsed != "hybrid" || len(result.Results) != 0 {
		t.Fatalf("empty stage result=%+v error=%v", result, graphErr)
	}
}
