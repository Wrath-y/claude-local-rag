package graphsnapshot

import "context"

// Embedder is intentionally transport-neutral. Implementations must not hold
// a Store transaction while invoking it; vector generation remains private
// until coverage validation and selection complete.
type Embedder interface {
	Embed(context.Context, []string) ([][]float32, error)
}

// EmbeddingIdentity describes the fixed capability used to build a derived
// generation. It deliberately contains configuration identity only; no
// credentials, endpoint, or provider response belongs in persisted metadata.
type EmbeddingIdentity struct {
	Algorithm  string
	Provider   string
	Model      string
	Dimensions int
}

// IdentifiedEmbedder is optional so deterministic test embedders and legacy
// adapters remain valid. Production wiring supplies it whenever an embedding
// provider is configured.
type IdentifiedEmbedder interface {
	Embedder
	EmbeddingIdentity() EmbeddingIdentity
}

type identifiedEmbedder struct {
	Embedder
	identity EmbeddingIdentity
}

func (e identifiedEmbedder) EmbeddingIdentity() EmbeddingIdentity { return e.identity }

// WithEmbeddingIdentity attaches immutable safe identity metadata without
// changing the provider's batch/cancellation behaviour.
func WithEmbeddingIdentity(embedder Embedder, identity EmbeddingIdentity) Embedder {
	return identifiedEmbedder{Embedder: embedder, identity: identity}
}
