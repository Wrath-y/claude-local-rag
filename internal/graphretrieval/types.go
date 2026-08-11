// Package graphretrieval defines transport-neutral snapshot-bound retrieval.
// It composes lifecycle and graph-query records but never calls legacy chunk
// retrieval, an LLM, or HTTP handlers.
package graphretrieval

import (
	"context"
	"sort"

	"github.com/Wrath-y/local-rag/internal/graphquery"
	"github.com/Wrath-y/local-rag/internal/graphsnapshot"
)

const (
	DefaultSeedLimit   = 20
	DefaultResultLimit = 20
	MaxSeedLimit       = 100
	MaxResultLimit     = 100
	DefaultGraphDepth  = 1
	MaxGraphDepth      = 3
	RRFK               = 60
)

type Request struct {
	Query             string   `json:"query"`
	SnapshotVersion   string   `json:"snapshot_version,omitempty"`
	NodeTypes         []string `json:"node_types,omitempty"`
	EdgeTypes         []string `json:"edge_types,omitempty"`
	RelationshipKinds []string `json:"relationship_kinds,omitempty"`
	SeedLimit         *int     `json:"seed_limit,omitempty"`
	ResultLimit       *int     `json:"result_limit,omitempty"`
	GraphDepth        *int     `json:"graph_depth,omitempty"`
}

type Filter struct {
	SnapshotVersion   string
	NodeTypes         []string
	EdgeTypes         []string
	RelationshipKinds []string
}

type NormalizedRequest struct {
	Query string
	Filter
	SeedLimit   int
	ResultLimit int
	GraphDepth  int
}

type Stage string

const (
	StageBM25   Stage = "bm25"
	StageVector Stage = "vector"
	StageRerank Stage = "rerank"
)

type StageOutcome string

const (
	StageUsed             StageOutcome = "used"
	StageUnavailable      StageOutcome = "unavailable"
	StageTransientFailure StageOutcome = "transient_failure"
	StagePermanentFailure StageOutcome = "permanent_failure"
	StageIndexEvicted     StageOutcome = "index_evicted"
	StageSkipped          StageOutcome = "skipped"
)

type Warning struct {
	Stage     Stage  `json:"stage"`
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

const (
	WarningBM25Unavailable      = "BM25_UNAVAILABLE"
	WarningBM25TransientFailure = "BM25_TRANSIENT_FAILURE"
	WarningVectorUnavailable    = "VECTOR_UNAVAILABLE"
	WarningVectorTransient      = "VECTOR_TRANSIENT_FAILURE"
	WarningRerankUnavailable    = "RERANK_UNAVAILABLE"
	WarningRerankFailure        = "RERANK_FAILURE"
	WarningRerankInvalid        = "RERANK_INVALID_RESPONSE"
)

type GenerationIdentity struct {
	Component     string `json:"component"`
	Generation    string `json:"generation"`
	Algorithm     string `json:"algorithm"`
	Provider      string `json:"provider,omitempty"`
	Model         string `json:"model,omitempty"`
	Dimensions    *int   `json:"dimensions,omitempty"`
	Tokenizer     string `json:"tokenizer,omitempty"`
	ContentDigest string `json:"content_digest"`
}

type Candidate struct {
	NodeID     string
	Node       graphsnapshot.Node
	SearchText string
	RawScore   float64
	Rank       int
}

// ReadView exposes only the namespace/version-bound primitives required by
// retrieval. No method accepts another namespace, version, or generation.
type ReadView interface {
	Version() string
	ContentHash() string
	Generation(Stage) (*GenerationIdentity, StageOutcome)
	BM25(context.Context, string, []string, int) ([]Candidate, StageOutcome, error)
	Vector(context.Context, []float32, []string, int) ([]Candidate, StageOutcome, error)
	Nodes(context.Context, []string) ([]graphsnapshot.Node, error)
	Adjacency(context.Context, []string, graphquery.Direction) ([]graphsnapshot.Edge, error)
}

type Repository interface {
	WithRetrievalRead(context.Context, string, string, func(ReadView) error) error
}

type Scores struct {
	BM25Rank    *int     `json:"bm25_rank,omitempty"`
	BM25Score   *float64 `json:"bm25_score,omitempty"`
	VectorRank  *int     `json:"vector_rank,omitempty"`
	VectorScore *float64 `json:"vector_score,omitempty"`
	RRFScore    float64  `json:"rrf_score"`
	GraphScore  float64  `json:"graph_score"`
	RerankScore *float64 `json:"rerank_score,omitempty"`
}

type SeedEvidence struct {
	NodeID     string `json:"node_id"`
	SearchText string `json:"search_text"`
}

type PathEvidence struct {
	NodeIDs       []string             `json:"node_ids"`
	EdgeIDs       []string             `json:"edge_ids"`
	Nodes         []graphsnapshot.Node `json:"nodes"`
	Edges         []graphsnapshot.Edge `json:"edges"`
	ExplicitEdges []graphsnapshot.Edge `json:"explicit_edges"`
	InferredEdges []graphsnapshot.Edge `json:"inferred_edges"`
}

type Evidence struct {
	Seed *SeedEvidence `json:"seed,omitempty"`
	Path *PathEvidence `json:"path,omitempty"`
}

type Result struct {
	Rank           int                `json:"rank"`
	Node           graphsnapshot.Node `json:"node"`
	CitationText   string             `json:"citation_text"`
	HopCount       int                `json:"hop_count"`
	PathConfidence float64            `json:"path_confidence"`
	Scores         Scores             `json:"scores"`
	Evidence       Evidence           `json:"evidence"`
}

type RetrievalResult struct {
	ResolvedSnapshotVersion string              `json:"resolved_snapshot_version"`
	ContentHash             string              `json:"content_hash"`
	ModeUsed                string              `json:"mode_used"`
	Degraded                bool                `json:"degraded"`
	Warnings                []Warning           `json:"warnings"`
	FTSGeneration           *GenerationIdentity `json:"fts_generation,omitempty"`
	VectorGeneration        *GenerationIdentity `json:"vector_generation,omitempty"`
	Rerank                  StageOutcome        `json:"rerank"`
	Results                 []Result            `json:"results"`
}

// EmbeddingProvider is intentionally minimal. The caller provides the
// generation-compatible query prefix rather than relying on global defaults.
type EmbeddingProvider interface {
	Embed(context.Context, []string) ([][]float32, error)
}

type RerankResult struct {
	Index int
	Score float64
}

type RerankProvider interface {
	Rerank(context.Context, string, []string, int) ([]RerankResult, error)
}

func SortWarnings(warnings []Warning) {
	order := map[Stage]int{StageBM25: 0, StageVector: 1, StageRerank: 2}
	sort.SliceStable(warnings, func(i, j int) bool {
		if order[warnings[i].Stage] != order[warnings[j].Stage] {
			return order[warnings[i].Stage] < order[warnings[j].Stage]
		}
		return warnings[i].Code < warnings[j].Code
	})
}

func WarningFor(stage Stage, outcome StageOutcome) (Warning, bool) {
	switch stage {
	case StageBM25:
		switch outcome {
		case StageUnavailable, StagePermanentFailure:
			return Warning{Stage: stage, Code: WarningBM25Unavailable, Message: "BM25 retrieval is unavailable", Retryable: false}, true
		case StageTransientFailure:
			return Warning{Stage: stage, Code: WarningBM25TransientFailure, Message: "BM25 retrieval temporarily failed", Retryable: true}, true
		}
	case StageVector:
		switch outcome {
		case StageUnavailable, StagePermanentFailure:
			return Warning{Stage: stage, Code: WarningVectorUnavailable, Message: "Vector retrieval is unavailable", Retryable: false}, true
		case StageTransientFailure:
			return Warning{Stage: stage, Code: WarningVectorTransient, Message: "Vector retrieval temporarily failed", Retryable: true}, true
		}
	case StageRerank:
		switch outcome {
		case StageUnavailable, StagePermanentFailure:
			return Warning{Stage: stage, Code: WarningRerankUnavailable, Message: "Reranking is unavailable", Retryable: false}, true
		case StageTransientFailure:
			return Warning{Stage: stage, Code: WarningRerankFailure, Message: "Reranking temporarily failed", Retryable: true}, true
		}
	}
	return Warning{}, false
}

// BaseFailureRetryable is true only when every unavailable base stage is
// transient. A permanent or missing capability must not ask callers to retry.
func BaseFailureRetryable(outcomes ...StageOutcome) bool {
	if len(outcomes) == 0 {
		return false
	}
	for _, outcome := range outcomes {
		if outcome != StageTransientFailure {
			return false
		}
	}
	return true
}
