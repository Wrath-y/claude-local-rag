package graphsnapshot

import "context"

// Embedder is intentionally transport-neutral. Implementations must not hold
// a Store transaction while invoking it; vector generation remains private
// until coverage validation and selection complete.
type Embedder interface {
	Embed(context.Context, []string) ([][]float32, error)
}
