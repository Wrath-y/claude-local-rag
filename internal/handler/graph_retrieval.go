package handler

import (
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Wrath-y/local-rag/internal/graphretrieval"
	"github.com/Wrath-y/local-rag/internal/graphsnapshot"
	"github.com/Wrath-y/local-rag/internal/provider"
	"github.com/Wrath-y/local-rag/internal/store"
)

const defaultGraphRetrievalMaxPayloadBytes = 1 << 20

type GraphRetrievalService interface {
	Retrieve(context.Context, string, graphretrieval.Request) (graphretrieval.RetrievalResult, *graphsnapshot.Error)
}

// lifecycleGraphRetrievalRepository holds the StoreLifecycle lease for the
// entire transaction-scoped retrieval callback.
type lifecycleGraphRetrievalRepository struct{ stores *StoreLifecycle }

func (r lifecycleGraphRetrievalRepository) WithRetrievalRead(ctx context.Context, namespace, version string, fn func(graphretrieval.ReadView) error) error {
	if r.stores == nil {
		return errors.New("graph retrieval store unavailable")
	}
	return r.stores.WithStore(func(st *store.Store) error { return st.WithRetrievalRead(ctx, namespace, version, fn) })
}

type handlerReranker struct{ provider provider.RerankProvider }

func (r handlerReranker) Rerank(ctx context.Context, query string, documents []string, topN int) ([]graphretrieval.RerankResult, error) {
	if r.provider == nil {
		return nil, errors.New("reranking provider is unavailable")
	}
	result, err := r.provider.Rerank(ctx, query, documents, topN)
	if err != nil {
		return nil, err
	}
	converted := make([]graphretrieval.RerankResult, len(result))
	for i, item := range result {
		converted[i] = graphretrieval.RerankResult{Index: item.Index, Score: item.RelevanceScore}
	}
	return converted, nil
}

// RetrieveGraph runs strict immutable snapshot retrieval without changing the
// unversioned legacy /retrieve route.
func (h *Handler) RetrieveGraph(c *gin.Context) {
	if h.graphRetrieval == nil {
		writeGraphError(c, graphsnapshot.NewError(graphsnapshot.CodeGraphStoreUnavailable, nil, nil))
		return
	}
	namespace := c.Param("namespace")
	if namespace == "" {
		writeGraphError(c, graphsnapshot.NewError(graphsnapshot.CodeInvalidRetrievalRequest, map[string]any{"field": "namespace"}, nil))
		return
	}
	if c.Request.ContentLength > defaultGraphRetrievalMaxPayloadBytes {
		writeGraphError(c, graphsnapshot.NewError(graphsnapshot.CodeLimitExceeded, map[string]any{"field": "payload_bytes", "max": defaultGraphRetrievalMaxPayloadBytes}, nil))
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, defaultGraphRetrievalMaxPayloadBytes)
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		writeGraphError(c, graphsnapshot.NewError(graphsnapshot.CodeInvalidRetrievalRequest, map[string]any{"field": "body"}, err))
		return
	}
	request, graphErr := graphretrieval.DecodeRequest(body)
	if graphErr != nil {
		writeGraphError(c, graphErr)
		return
	}
	if _, graphErr = graphretrieval.Normalize(request); graphErr != nil {
		writeGraphError(c, graphErr)
		return
	}
	result, graphErr := h.graphRetrieval.Retrieve(c.Request.Context(), namespace, request)
	if graphErr != nil {
		writeGraphError(c, graphErr)
		return
	}
	c.JSON(http.StatusOK, result)
}
