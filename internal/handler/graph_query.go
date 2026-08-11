package handler

import (
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Wrath-y/local-rag/internal/graphquery"
	"github.com/Wrath-y/local-rag/internal/graphsnapshot"
	"github.com/Wrath-y/local-rag/internal/store"
)

const defaultGraphQueryMaxPayloadBytes = 1 << 20

// lifecycleGraphReadRepository holds the StoreLifecycle read lease for the
// complete SQLite callback, including deterministic graph algorithm work.
type lifecycleGraphReadRepository struct{ stores *StoreLifecycle }

func (r lifecycleGraphReadRepository) WithRead(ctx context.Context, namespace, version string, fn func(graphquery.ReadView) error) error {
	if r.stores == nil {
		return graphquery.ErrStoreUnavailable
	}
	return r.stores.WithStore(func(st *store.Store) error { return st.WithRead(ctx, namespace, version, fn) })
}

// TraverseGraph performs a strict, provider-free immutable graph traversal.
func (h *Handler) TraverseGraph(c *gin.Context) {
	if h.graphQuery == nil {
		writeGraphError(c, graphsnapshot.NewError(graphsnapshot.CodeGraphStoreUnavailable, nil, nil))
		return
	}
	namespace := c.Param("namespace")
	if namespace == "" {
		writeGraphError(c, graphsnapshot.NewError(graphsnapshot.CodeInvalidGraphQuery, map[string]any{"field": "namespace"}, nil))
		return
	}
	body, err := readGraphQueryBody(c)
	if err != nil {
		writeGraphError(c, graphsnapshot.NewError(graphsnapshot.CodeInvalidGraphQuery, map[string]any{"field": "body"}, err))
		return
	}
	request, graphErr := graphquery.DecodeTraverse(body)
	if graphErr != nil {
		writeGraphError(c, graphErr)
		return
	}
	result, graphErr := h.graphQuery.Traverse(c.Request.Context(), namespace, request)
	if graphErr != nil {
		writeGraphError(c, graphErr)
		return
	}
	c.JSON(http.StatusOK, result)
}

// PathsGraph performs a strict, provider-free bounded evidence path query.
func (h *Handler) PathsGraph(c *gin.Context) {
	if h.graphQuery == nil {
		writeGraphError(c, graphsnapshot.NewError(graphsnapshot.CodeGraphStoreUnavailable, nil, nil))
		return
	}
	namespace := c.Param("namespace")
	if namespace == "" {
		writeGraphError(c, graphsnapshot.NewError(graphsnapshot.CodeInvalidGraphQuery, map[string]any{"field": "namespace"}, nil))
		return
	}
	body, err := readGraphQueryBody(c)
	if err != nil {
		writeGraphError(c, graphsnapshot.NewError(graphsnapshot.CodeInvalidGraphQuery, map[string]any{"field": "body"}, err))
		return
	}
	request, graphErr := graphquery.DecodePaths(body)
	if graphErr != nil {
		writeGraphError(c, graphErr)
		return
	}
	result, graphErr := h.graphQuery.Paths(c.Request.Context(), namespace, request)
	if graphErr != nil {
		writeGraphError(c, graphErr)
		return
	}
	c.JSON(http.StatusOK, result)
}

func readGraphQueryBody(c *gin.Context) ([]byte, error) {
	if c.Request.ContentLength > defaultGraphQueryMaxPayloadBytes {
		return nil, errors.New("graph query payload too large")
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, defaultGraphQueryMaxPayloadBytes)
	return io.ReadAll(c.Request.Body)
}
