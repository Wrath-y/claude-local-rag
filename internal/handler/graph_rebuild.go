package handler

import (
	"context"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Wrath-y/local-rag/internal/graphsnapshot"
	"github.com/Wrath-y/local-rag/internal/operability"
)

// GraphRebuildService is deliberately transport-neutral so admission behavior
// is shared by HTTP and future local callers without duplicating validation.
type GraphRebuildService interface {
	Submit(context.Context, string, string, string, string, operability.RebuildRequest) (operability.RebuildSubmission, *graphsnapshot.Error)
}

// RebuildGraphSnapshot accepts explicit, caller-idempotent derived-index work.
// It never starts work implicitly from reads, health checks, or task polling.
func (h *Handler) RebuildGraphSnapshot(c *gin.Context) {
	namespace, version, ok := graphSnapshotIdentity(c)
	if !ok {
		return
	}
	if h.graphRebuild == nil {
		writeGraphError(c, graphsnapshot.NewError(graphsnapshot.CodeGraphStoreUnavailable, nil, nil))
		return
	}
	limits := graphLimits(h.deps.Config)
	if c.Request.ContentLength > int64(limits.maxPayloadBytes) {
		writeGraphError(c, graphLimitError("payload_bytes", limits.maxPayloadBytes))
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, int64(limits.maxPayloadBytes))
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		writeGraphError(c, graphsnapshot.NewError(graphsnapshot.CodeInvalidRebuildRequest, map[string]any{"field": "body"}, err))
		return
	}
	var request operability.RebuildRequest
	if err = graphsnapshot.DecodeStrictJSON(body, &request); err != nil {
		writeGraphError(c, graphsnapshot.NewError(graphsnapshot.CodeInvalidRebuildRequest, map[string]any{"field": "body"}, err))
		return
	}
	submission, graphErr := h.graphRebuild.Submit(c.Request.Context(), namespace, version, c.GetHeader("Idempotency-Key"), GraphRequestIDFromContext(c.Request.Context()), request)
	if graphErr != nil {
		writeGraphError(c, graphErr)
		return
	}
	c.Header("Location", submission.TaskURL)
	c.JSON(http.StatusAccepted, submission)
}
