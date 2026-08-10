package graphsnapshot

import "context"

// ExistingSnapshotReader is intentionally small: replay must happen before a
// full/delta request is materialized or any new graph state is created.
type ExistingSnapshotReader interface {
	LookupGraphSnapshot(context.Context, string, string) (Snapshot, bool, error)
}

// SubmissionCheck describes whether the caller must create a first-time
// snapshot or return an immutable existing resource. HTTP status selection is
// derived later from Snapshot.Status: building replays as 202; ready and
// failed replays as 200.
type SubmissionCheck struct {
	Snapshot Snapshot
	Existing bool
}

// CheckExistingSubmission is the first step of PUT orchestration. It does not
// accept an Idempotency-Key because snapshot identity is strictly namespace,
// version, and server-verified final content hash.
func CheckExistingSubmission(ctx context.Context, repository ExistingSnapshotReader, namespace, version, contentHash string) (SubmissionCheck, *Error) {
	snapshot, found, err := repository.LookupGraphSnapshot(ctx, namespace, version)
	if err != nil {
		return SubmissionCheck{}, NewError(CodeGraphStoreUnavailable, nil, err)
	}
	if !found {
		return SubmissionCheck{}, nil
	}
	if snapshot.ContentHash != contentHash {
		return SubmissionCheck{}, NewError(CodeContentHashConflict, map[string]any{"namespace": namespace, "version": version}, nil)
	}
	return SubmissionCheck{Snapshot: snapshot, Existing: true}, nil
}
