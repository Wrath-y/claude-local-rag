package graphsnapshot

import "fmt"

// Code is a stable machine-readable /v1 lifecycle error identifier.
type Code string

const (
	CodeInvalidSnapshotRequest        Code = "INVALID_SNAPSHOT_REQUEST"
	CodeDuplicateNodeID               Code = "DUPLICATE_NODE_ID"
	CodeDuplicateEdgeID               Code = "DUPLICATE_EDGE_ID"
	CodeDanglingEdge                  Code = "DANGLING_EDGE"
	CodeInvalidRelationProvenance     Code = "INVALID_RELATION_PROVENANCE"
	CodeInvalidDeltaOperation         Code = "INVALID_DELTA_OPERATION"
	CodeBaseSnapshotNotFound          Code = "BASE_SNAPSHOT_NOT_FOUND"
	CodeBaseSnapshotNotReady          Code = "BASE_SNAPSHOT_NOT_READY"
	CodeContentHashMismatch           Code = "CONTENT_HASH_MISMATCH"
	CodeContentHashConflict           Code = "CONTENT_HASH_CONFLICT"
	CodeSnapshotNotFound              Code = "SNAPSHOT_NOT_FOUND"
	CodeTaskNotFound                  Code = "TASK_NOT_FOUND"
	CodeSnapshotNotReady              Code = "SNAPSHOT_NOT_READY"
	CodeActiveSnapshotDeleteForbidden Code = "ACTIVE_SNAPSHOT_DELETE_FORBIDDEN"
	CodeSnapshotWriteInProgress       Code = "SNAPSHOT_WRITE_IN_PROGRESS"
	CodeInvalidGraphQuery             Code = "INVALID_GRAPH_QUERY"
	CodeInvalidRetrievalRequest       Code = "INVALID_RETRIEVAL_REQUEST"
	CodeInvalidRebuildRequest         Code = "INVALID_REBUILD_REQUEST"
	CodeLimitExceeded                 Code = "LIMIT_EXCEEDED"
	CodeNoActiveSnapshot              Code = "NO_ACTIVE_SNAPSHOT"
	CodeNodeNotFound                  Code = "NODE_NOT_FOUND"
	CodeGraphStoreUnavailable         Code = "GRAPH_STORE_UNAVAILABLE"
	CodeSnapshotIndexNotReady         Code = "SNAPSHOT_INDEX_NOT_READY"
	CodeIdempotencyConflict           Code = "IDEMPOTENCY_CONFLICT"
	CodeReimportRequired              Code = "REIMPORT_REQUIRED"
	CodeRetrievalUnavailable          Code = "RETRIEVAL_UNAVAILABLE"
	CodeInternalError                 Code = "INTERNAL_ERROR"
)

type errorDefinition struct {
	message   string
	retryable bool
}

var errorCatalog = map[Code]errorDefinition{
	CodeInvalidSnapshotRequest:        {"Snapshot request is invalid", false},
	CodeDuplicateNodeID:               {"Snapshot contains duplicate node IDs", false},
	CodeDuplicateEdgeID:               {"Snapshot contains duplicate edge IDs", false},
	CodeDanglingEdge:                  {"Snapshot contains an edge with an unknown endpoint", false},
	CodeInvalidRelationProvenance:     {"Snapshot relationship provenance is invalid", false},
	CodeInvalidDeltaOperation:         {"Snapshot delta operation is invalid", false},
	CodeBaseSnapshotNotFound:          {"Base snapshot was not found", false},
	CodeBaseSnapshotNotReady:          {"Base snapshot is not ready", false},
	CodeContentHashMismatch:           {"Snapshot content hash does not match", false},
	CodeContentHashConflict:           {"Snapshot version already has different content", false},
	CodeSnapshotNotFound:              {"Snapshot was not found", false},
	CodeTaskNotFound:                  {"Graph task was not found", false},
	CodeSnapshotNotReady:              {"Snapshot is not ready", false},
	CodeActiveSnapshotDeleteForbidden: {"Active snapshot cannot be deleted", false},
	CodeSnapshotWriteInProgress:       {"Snapshot has an active writer", false},
	CodeInvalidGraphQuery:             {"Graph query is invalid", false},
	CodeInvalidRetrievalRequest:       {"Retrieval request is invalid", false},
	CodeInvalidRebuildRequest:         {"Graph rebuild request is invalid", false},
	CodeLimitExceeded:                 {"Graph query limit is exceeded", false},
	CodeNoActiveSnapshot:              {"No active snapshot is available", false},
	CodeNodeNotFound:                  {"Graph node was not found", false},
	CodeGraphStoreUnavailable:         {"Graph storage is unavailable", true},
	CodeSnapshotIndexNotReady:         {"Snapshot retrieval indexes are not ready", false},
	CodeIdempotencyConflict:           {"Idempotency key conflicts with existing rebuild work", false},
	CodeReimportRequired:              {"Graph snapshot must be reimported before rebuild", false},
	CodeRetrievalUnavailable:          {"Graph retrieval is unavailable", true},
	CodeInternalError:                 {"Graph lifecycle operation failed", false},
}

// Error is safe for a public /v1 response. Cause stays private to service and
// handler logging; it is deliberately excluded from JSON serialization.
type Error struct {
	Code      Code           `json:"code"`
	Message   string         `json:"message"`
	Retryable bool           `json:"retryable"`
	Details   map[string]any `json:"details"`
	RequestID string         `json:"request_id,omitempty"`
	Cause     error          `json:"-"`
}

func (e *Error) Error() string { return string(e.Code) + ": " + e.Message }
func (e *Error) Unwrap() error { return e.Cause }

// NewError only accepts catalogued codes and safe caller-supplied details.
// Unknown codes become INTERNAL_ERROR so implementations cannot accidentally
// publish an unstable identifier.
func NewError(code Code, details map[string]any, cause error) *Error {
	definition, ok := errorCatalog[code]
	if !ok {
		code = CodeInternalError
		definition = errorCatalog[code]
	}
	if details == nil {
		details = map[string]any{}
	}
	return &Error{Code: code, Message: definition.message, Retryable: definition.retryable, Details: details, Cause: cause}
}

func (e *Error) WithDetail(key string, value any) *Error {
	if e.Details == nil {
		e.Details = map[string]any{}
	}
	e.Details[key] = value
	return e
}

// WithRetryability is reserved for stable dependency errors whose retry rule
// depends on the observed stage outcomes rather than the code alone.
func (e *Error) WithRetryability(retryable bool) *Error {
	e.Retryable = retryable
	return e
}

// WithRequestID records the safe causal request identifier on a durable
// background error. The HTTP adapter has its own request ID for poll errors.
func (e *Error) WithRequestID(requestID string) *Error {
	e.RequestID = requestID
	return e
}

func Errorf(code Code, format string, args ...any) *Error {
	return NewError(code, nil, fmt.Errorf(format, args...))
}
