package graphretrieval

import (
	"context"
	"errors"
	"math"
)

// ProviderIdentity is safe configuration metadata returned with a retrieval
// result. It intentionally excludes API keys, base URLs, and raw payloads.
type ProviderIdentity struct {
	Provider  string `json:"provider,omitempty"`
	Model     string `json:"model,omitempty"`
	Algorithm string `json:"algorithm,omitempty"`
}

type EmbeddingAdapter struct {
	Provider    EmbeddingProvider
	Identity    ProviderIdentity
	QueryPrefix string
}

// EmbedQuery applies exactly the generation-compatible prefix and requests a
// single vector. It never invokes an LLM and exposes only the stage taxonomy.
func (a EmbeddingAdapter) EmbedQuery(ctx context.Context, query string) ([]float32, StageOutcome) {
	if a.Provider == nil {
		return nil, StagePermanentFailure
	}
	vectors, err := a.Provider.Embed(ctx, []string{a.QueryPrefix + query})
	if err != nil {
		return nil, classifyProviderError(ctx, err)
	}
	if len(vectors) != 1 || len(vectors[0]) == 0 {
		return nil, StagePermanentFailure
	}
	for _, value := range vectors[0] {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return nil, StagePermanentFailure
		}
	}
	return vectors[0], StageUsed
}

type RerankAdapter struct {
	Provider RerankProvider
	Identity ProviderIdentity
}

func (a RerankAdapter) Rerank(ctx context.Context, query string, documents []string) ([]RerankResult, StageOutcome) {
	if a.Provider == nil {
		return nil, StageUnavailable
	}
	result, err := a.Provider.Rerank(ctx, query, documents, len(documents))
	if err != nil {
		return nil, classifyProviderError(ctx, err)
	}
	return result, StageUsed
}

func classifyProviderError(ctx context.Context, err error) StageOutcome {
	if err == nil {
		return StageUsed
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
		return StageTransientFailure
	}
	// Provider transports rarely expose a trustworthy permanent class. Treat an
	// opaque failure as transient; callers can safely retry the same read-only
	// request and no private provider detail crosses the boundary.
	return StageTransientFailure
}
