package handler

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Wrath-y/local-rag/internal/graphsnapshot"
)

func TestGraphRequestIDUsesValidCallerValueAndContext(t *testing.T) {
	router := gin.New()
	router.Use(GraphRequestID())
	router.GET("/v1/test", func(c *gin.Context) {
		requestID := GraphRequestIDFromContext(c.Request.Context())
		c.JSON(http.StatusOK, gin.H{"request_id": requestID})
	})

	request := httptest.NewRequest(http.MethodGet, "/v1/test", nil)
	request.Header.Set(graphRequestIDHeader, "client.request_123")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if got := response.Header().Get(graphRequestIDHeader); got != "client.request_123" {
		t.Fatalf("X-Request-ID = %q, want caller value", got)
	}
	if !strings.Contains(response.Body.String(), `"request_id":"client.request_123"`) {
		t.Fatalf("response does not include context request ID: %s", response.Body.String())
	}
}

func TestGraphRequestIDReplacesInvalidOrMissingValue(t *testing.T) {
	for _, supplied := range []string{"", "bad value", strings.Repeat("a", 129), "_bad"} {
		t.Run("value="+supplied, func(t *testing.T) {
			router := gin.New()
			router.Use(GraphRequestID())
			router.GET("/v1/test", func(c *gin.Context) { c.Status(http.StatusNoContent) })

			request := httptest.NewRequest(http.MethodGet, "/v1/test", nil)
			if supplied != "" {
				request.Header.Set(graphRequestIDHeader, supplied)
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			got := response.Header().Get(graphRequestIDHeader)
			if !validGraphRequestID(got) || got == supplied {
				t.Fatalf("generated request ID = %q for supplied %q", got, supplied)
			}
		})
	}
}

func TestWriteGraphErrorMapsEveryStableCode(t *testing.T) {
	testCases := map[graphsnapshot.Code]int{
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

	if len(testCases) != len(graphErrorStatuses) {
		t.Fatalf("test cases cover %d codes, writer maps %d", len(testCases), len(graphErrorStatuses))
	}
	for code, wantStatus := range testCases {
		t.Run(string(code), func(t *testing.T) {
			router := gin.New()
			router.Use(GraphRequestID())
			router.GET("/v1/test", func(c *gin.Context) {
				writeGraphError(c, graphsnapshot.NewError(code, map[string]any{"field": "content_hash"}, nil))
			})

			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/test", nil))
			if response.Code != wantStatus {
				t.Fatalf("status = %d, want %d: %s", response.Code, wantStatus, response.Body.String())
			}
			for _, field := range []string{`"code":"` + string(code) + `"`, `"message":`, `"retryable":`, `"details":{"field":"content_hash"}`, `"request_id":"`} {
				if !strings.Contains(response.Body.String(), field) {
					t.Fatalf("response lacks %q: %s", field, response.Body.String())
				}
			}
			if got := response.Header().Get(graphRequestIDHeader); !validGraphRequestID(got) || !strings.Contains(response.Body.String(), `"request_id":"`+got+`"`) {
				t.Fatalf("request ID is inconsistent: header=%q body=%s", got, response.Body.String())
			}
		})
	}
}

func TestWriteGraphErrorDoesNotExposeCauseOrUncataloguedMessage(t *testing.T) {
	router := gin.New()
	router.Use(GraphRequestID())
	router.GET("/v1/test", func(c *gin.Context) {
		writeGraphError(c, &graphsnapshot.Error{
			Code:    graphsnapshot.CodeInternalError,
			Message: "database password=top-secret at /private/data/local-rag.db",
			Cause:   errors.New("database password=top-secret at /private/data/local-rag.db"),
		})
	})

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/test", nil))
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusInternalServerError)
	}
	for _, forbidden := range []string{"top-secret", "/private/data", "database password"} {
		if strings.Contains(response.Body.String(), forbidden) {
			t.Fatalf("response exposes %q: %s", forbidden, response.Body.String())
		}
	}
	if !strings.Contains(response.Body.String(), `"message":"Graph lifecycle operation failed"`) {
		t.Fatalf("response does not use catalogued public message: %s", response.Body.String())
	}
}
