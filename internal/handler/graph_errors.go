package handler

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/Wrath-y/local-rag/internal/graphsnapshot"
)

const graphRequestIDHeader = "X-Request-ID"

type graphRequestIDContextKey struct{}

// GraphRequestID attaches a safe request ID to /v1 requests. Callers may
// supply an ID for correlation, but malformed values are replaced rather than
// echoed into logs or response headers.
func GraphRequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		graphRequestID(c)
		c.Next()
	}
}

// GraphRequestIDFromContext returns the /v1 request ID propagated by
// GraphRequestID. Graph handlers pass this context to the service so durable
// task failures can retain the request correlation in a later change.
func GraphRequestIDFromContext(ctx context.Context) string {
	requestID, _ := ctx.Value(graphRequestIDContextKey{}).(string)
	return requestID
}

func graphRequestID(c *gin.Context) string {
	if requestID, ok := c.Get(string(graphRequestIDHeader)); ok {
		if value, ok := requestID.(string); ok && validGraphRequestID(value) {
			c.Header(graphRequestIDHeader, value)
			return value
		}
	}

	requestID := c.GetHeader(graphRequestIDHeader)
	if !validGraphRequestID(requestID) {
		requestID = "req_" + uuid.NewString()
	}
	c.Set(string(graphRequestIDHeader), requestID)
	c.Header(graphRequestIDHeader, requestID)
	if c.Request != nil {
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), graphRequestIDContextKey{}, requestID))
	}
	return requestID
}

func validGraphRequestID(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for index, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '.' || char == '_' || char == '-' {
			if index == 0 && (char == '.' || char == '_' || char == '-') {
				return false
			}
			continue
		}
		return false
	}
	return true
}

var graphErrorStatuses = map[graphsnapshot.Code]int{
	graphsnapshot.CodeInvalidSnapshotRequest:        http.StatusBadRequest,
	graphsnapshot.CodeDuplicateNodeID:               http.StatusUnprocessableEntity,
	graphsnapshot.CodeDuplicateEdgeID:               http.StatusUnprocessableEntity,
	graphsnapshot.CodeDanglingEdge:                  http.StatusUnprocessableEntity,
	graphsnapshot.CodeInvalidRelationProvenance:     http.StatusUnprocessableEntity,
	graphsnapshot.CodeInvalidDeltaOperation:         http.StatusUnprocessableEntity,
	graphsnapshot.CodeBaseSnapshotNotFound:          http.StatusNotFound,
	graphsnapshot.CodeBaseSnapshotNotReady:          http.StatusConflict,
	graphsnapshot.CodeContentHashMismatch:           http.StatusUnprocessableEntity,
	graphsnapshot.CodeContentHashConflict:           http.StatusConflict,
	graphsnapshot.CodeSnapshotNotFound:              http.StatusNotFound,
	graphsnapshot.CodeTaskNotFound:                  http.StatusNotFound,
	graphsnapshot.CodeSnapshotNotReady:              http.StatusConflict,
	graphsnapshot.CodeActiveSnapshotDeleteForbidden: http.StatusConflict,
	graphsnapshot.CodeSnapshotWriteInProgress:       http.StatusConflict,
	graphsnapshot.CodeInvalidGraphQuery:             http.StatusBadRequest,
	graphsnapshot.CodeLimitExceeded:                 http.StatusBadRequest,
	graphsnapshot.CodeNoActiveSnapshot:              http.StatusNotFound,
	graphsnapshot.CodeNodeNotFound:                  http.StatusNotFound,
	graphsnapshot.CodeGraphStoreUnavailable:         http.StatusServiceUnavailable,
	graphsnapshot.CodeInternalError:                 http.StatusInternalServerError,
}

type graphErrorResponse struct {
	Code      graphsnapshot.Code `json:"code"`
	Message   string             `json:"message"`
	Retryable bool               `json:"retryable"`
	Details   map[string]any     `json:"details"`
	RequestID string             `json:"request_id"`
}

// writeGraphError is the only graph lifecycle error writer. It recreates the
// catalogued public fields, so an internal cause or an accidentally supplied
// message can never be serialized to a /v1 client.
func writeGraphError(c *gin.Context, graphErr *graphsnapshot.Error) {
	if graphErr == nil {
		graphErr = graphsnapshot.NewError(graphsnapshot.CodeInternalError, nil, nil)
	}
	if _, known := graphErrorStatuses[graphErr.Code]; !known {
		graphErr = graphsnapshot.NewError(graphsnapshot.CodeInternalError, nil, graphErr)
	} else {
		graphErr = graphsnapshot.NewError(graphErr.Code, graphErr.Details, graphErr.Cause)
	}

	if graphErr.Cause != nil {
		slog.Error("graph lifecycle request failed", "code", graphErr.Code, "request_id", graphRequestID(c), "err", graphErr.Cause)
	}

	c.AbortWithStatusJSON(graphErrorStatuses[graphErr.Code], graphErrorResponse{
		Code:      graphErr.Code,
		Message:   graphErr.Message,
		Retryable: graphErr.Retryable,
		Details:   graphErr.Details,
		RequestID: graphRequestID(c),
	})
}
