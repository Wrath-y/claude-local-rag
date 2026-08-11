package graphretrieval

import (
	"context"
	"errors"
	"math"
	"sort"

	"github.com/Wrath-y/local-rag/internal/graphquery"
	"github.com/Wrath-y/local-rag/internal/graphsnapshot"
)

type Service struct {
	Repository    Repository
	Embedder      EmbeddingAdapter
	Reranker      RerankAdapter
	RerankEnabled bool
}

func (s Service) Retrieve(ctx context.Context, namespace string, request Request) (RetrievalResult, *graphsnapshot.Error) {
	normalized, graphErr := Normalize(request)
	if graphErr != nil {
		return RetrievalResult{}, graphErr
	}
	if s.Repository == nil {
		return RetrievalResult{}, graphsnapshot.NewError(graphsnapshot.CodeGraphStoreUnavailable, nil, nil)
	}
	// Embedding is deliberately before the SQLite read transaction. A missing
	// provider remains a typed base-stage outcome rather than a hard failure.
	queryVector, embeddingOutcome := s.Embedder.EmbedQuery(ctx, normalized.Query)
	var result RetrievalResult
	var bm25Outcome, vectorOutcome StageOutcome
	err := s.Repository.WithRetrievalRead(ctx, namespace, normalized.SnapshotVersion, func(view ReadView) error {
		ftsIdentity, ftsGenerationState := view.Generation(StageBM25)
		vectorIdentity, vectorGenerationState := view.Generation(StageVector)
		if ftsGenerationState == StageIndexEvicted && vectorGenerationState == StageIndexEvicted {
			return graphsnapshot.NewError(graphsnapshot.CodeSnapshotIndexNotReady, map[string]any{"rebuild_required": true}, nil)
		}
		result.ResolvedSnapshotVersion, result.ContentHash = view.Version(), view.ContentHash()
		result.FTSGeneration, result.VectorGeneration = ftsIdentity, vectorIdentity
		var bm25, vectors []Candidate
		var readErr error
		bm25, bm25Outcome, readErr = view.BM25(ctx, normalized.Query, normalized.NodeTypes, normalized.SeedLimit)
		if readErr != nil {
			return readErr
		}
		vectorOutcome = vectorGenerationState
		if embeddingOutcome != StageUsed {
			vectorOutcome = embeddingOutcome
		} else if vectorGenerationState == StageUsed {
			if !compatibleEmbeddingIdentity(s.Embedder.Identity, vectorIdentity) {
				vectorOutcome = StagePermanentFailure
			} else {
				vectors, vectorOutcome, readErr = view.Vector(ctx, queryVector, normalized.NodeTypes, normalized.SeedLimit)
				if readErr != nil {
					return readErr
				}
			}
		}
		if bm25Outcome != StageUsed && vectorOutcome != StageUsed {
			return retrievalUnavailable(bm25Outcome, vectorOutcome)
		}
		result.ModeUsed = modeFor(bm25Outcome, vectorOutcome)
		result.Warnings = stageWarnings(bm25Outcome, vectorOutcome, StageSkipped)
		result.Degraded = len(result.Warnings) > 0
		seeds := Fuse(bm25, vectors, normalized.SeedLimit)
		result.Results, readErr = Expand(ctx, view, seeds, normalized)
		return readErr
	})
	if err != nil {
		return RetrievalResult{}, mapError(err)
	}
	if graphErr := s.applyRerank(ctx, normalized.Query, &result); graphErr != nil {
		return RetrievalResult{}, graphErr
	}
	return result, nil
}

func compatibleEmbeddingIdentity(configured ProviderIdentity, generation *GenerationIdentity) bool {
	if generation == nil {
		return false
	}
	if generation.Provider != "" && configured.Provider != generation.Provider {
		return false
	}
	return generation.Model == "" || configured.Model == generation.Model
}

func modeFor(bm25, vector StageOutcome) string {
	if bm25 == StageUsed && vector == StageUsed {
		return "hybrid"
	}
	if bm25 == StageUsed {
		return "bm25_only"
	}
	return "vector_only"
}

func stageWarnings(bm25, vector, rerank StageOutcome) []Warning {
	warnings := []Warning{}
	if warning, ok := WarningFor(StageBM25, bm25); ok {
		warnings = append(warnings, warning)
	}
	if warning, ok := WarningFor(StageVector, vector); ok {
		warnings = append(warnings, warning)
	}
	if warning, ok := WarningFor(StageRerank, rerank); ok {
		warnings = append(warnings, warning)
	}
	SortWarnings(warnings)
	return warnings
}

func retrievalUnavailable(outcomes ...StageOutcome) *graphsnapshot.Error {
	return graphsnapshot.NewError(graphsnapshot.CodeRetrievalUnavailable, map[string]any{"stages": []string{"bm25", "vector"}}, nil).WithRetryability(BaseFailureRetryable(outcomes...))
}

func (s Service) applyRerank(ctx context.Context, query string, result *RetrievalResult) *graphsnapshot.Error {
	if !s.RerankEnabled {
		result.Rerank = StageSkipped
		return nil
	}
	if len(result.Results) == 0 {
		result.Rerank = StageSkipped
		return nil
	}
	documents := make([]string, len(result.Results))
	for i := range result.Results {
		documents[i] = result.Results[i].CitationText
	}
	output, outcome := s.Reranker.Rerank(ctx, query, documents)
	result.Rerank = outcome
	if outcome == StageUsed && validRerankOutput(output, len(result.Results)) {
		for _, item := range output {
			score := item.Score
			result.Results[item.Index].Scores.RerankScore = &score
		}
		sort.SliceStable(result.Results, func(i, j int) bool {
			return *result.Results[i].Scores.RerankScore > *result.Results[j].Scores.RerankScore
		})
		for i := range result.Results {
			result.Results[i].Rank = i + 1
		}
		return nil
	}
	if outcome == StageUsed {
		result.Rerank = StagePermanentFailure
		result.Warnings = append(result.Warnings, Warning{Stage: StageRerank, Code: WarningRerankInvalid, Message: "Reranking returned an invalid response", Retryable: false})
	} else if warning, ok := WarningFor(StageRerank, outcome); ok {
		result.Warnings = append(result.Warnings, warning)
	}
	SortWarnings(result.Warnings)
	result.Degraded = true
	return nil
}

func validRerankOutput(output []RerankResult, length int) bool {
	if len(output) != length {
		return false
	}
	seen := make(map[int]struct{}, len(output))
	for _, item := range output {
		if item.Index < 0 || item.Index >= length || math.IsNaN(item.Score) || math.IsInf(item.Score, 0) {
			return false
		}
		if _, duplicate := seen[item.Index]; duplicate {
			return false
		}
		seen[item.Index] = struct{}{}
	}
	return true
}

func mapError(err error) *graphsnapshot.Error {
	var graphErr *graphsnapshot.Error
	if errors.As(err, &graphErr) {
		return graphErr
	}
	switch {
	case errors.Is(err, graphquery.ErrNoActiveSnapshot):
		return graphsnapshot.NewError(graphsnapshot.CodeNoActiveSnapshot, nil, nil)
	case errors.Is(err, graphquery.ErrSnapshotNotReady):
		return graphsnapshot.NewError(graphsnapshot.CodeSnapshotNotReady, nil, nil)
	case errors.Is(err, graphquery.ErrSnapshotNotFound):
		return graphsnapshot.NewError(graphsnapshot.CodeSnapshotNotFound, nil, nil)
	case errors.Is(err, graphquery.ErrStoreUnavailable):
		return graphsnapshot.NewError(graphsnapshot.CodeGraphStoreUnavailable, nil, err)
	default:
		return graphsnapshot.NewError(graphsnapshot.CodeInternalError, nil, err)
	}
}
