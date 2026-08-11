package graphretrieval

import (
	"context"
	"errors"
	"testing"
)

type embeddingFake struct {
	inputs [][]string
	err    error
}

func (f *embeddingFake) Embed(_ context.Context, texts []string) ([][]float32, error) {
	f.inputs = append(f.inputs, append([]string(nil), texts...))
	if f.err != nil {
		return nil, f.err
	}
	return [][]float32{{1, 0}}, nil
}

func TestEmbeddingAdapterPrefixesOneQueryAndSanitizesFailure(t *testing.T) {
	fake := &embeddingFake{}
	adapter := EmbeddingAdapter{Provider: fake, QueryPrefix: "query: "}
	vector, outcome := adapter.EmbedQuery(context.Background(), "find graph")
	if outcome != StageUsed || len(vector) != 2 || len(fake.inputs) != 1 || fake.inputs[0][0] != "query: find graph" {
		t.Fatalf("vector=%v outcome=%s inputs=%v", vector, outcome, fake.inputs)
	}
	if _, outcome = (EmbeddingAdapter{}).EmbedQuery(context.Background(), "q"); outcome != StagePermanentFailure {
		t.Fatalf("nil provider outcome=%s", outcome)
	}
	fake.err = errors.New("provider body contains secret")
	if _, outcome = adapter.EmbedQuery(context.Background(), "q"); outcome != StageTransientFailure {
		t.Fatalf("opaque provider failure outcome=%s", outcome)
	}
}
