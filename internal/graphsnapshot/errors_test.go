package graphsnapshot

import "testing"

func TestErrorCatalogCoversLifecycleCodes(t *testing.T) {
	codes := []Code{
		CodeInvalidSnapshotRequest, CodeDuplicateNodeID, CodeDuplicateEdgeID,
		CodeDanglingEdge, CodeInvalidRelationProvenance, CodeInvalidDeltaOperation,
		CodeBaseSnapshotNotFound, CodeBaseSnapshotNotReady, CodeContentHashMismatch,
		CodeContentHashConflict, CodeSnapshotNotFound, CodeTaskNotFound,
		CodeSnapshotNotReady, CodeActiveSnapshotDeleteForbidden,
		CodeSnapshotWriteInProgress, CodeGraphStoreUnavailable, CodeInternalError,
	}
	for _, code := range codes {
		err := NewError(code, nil, nil)
		if err.Code != code || err.Message == "" || err.Details == nil {
			t.Fatalf("catalog entry %s = %#v", code, err)
		}
	}
}

func TestUnknownErrorCodeCannotEscapeTheCatalog(t *testing.T) {
	err := NewError("UNSTABLE", nil, nil)
	if err.Code != CodeInternalError || err.Message == "" {
		t.Fatalf("unknown code = %#v", err)
	}
}
