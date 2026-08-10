package graphsnapshot

import (
	"context"
	"crypto/rand"
	"encoding/hex"
)

// ExistingSnapshotReader is intentionally small: replay must happen before a
// full/delta request is materialized or any new graph state is created.
type ExistingSnapshotReader interface {
	LookupGraphSnapshot(context.Context, string, string) (Snapshot, bool, error)
}

// SnapshotBase is the immutable source needed to materialize a delta without
// importing persistence details into the lifecycle service.
type SnapshotBase struct {
	Status   SnapshotStatus
	Manifest Manifest
}

// SnapshotAccepter owns the one short transaction that persists the accepted
// canonical manifest, component rows, private staging, and durable task.
type SnapshotAccepter interface {
	ExistingSnapshotReader
	ReadGraphSnapshotBase(context.Context, string, string) (SnapshotBase, bool, error)
	AcceptGraphSnapshot(context.Context, AcceptedSnapshot) (Snapshot, error)
}

// AcceptedSnapshot is fully normalized and hash-verified before it reaches
// the storage transaction.
type AcceptedSnapshot struct {
	Namespace   string
	Version     string
	BaseVersion string
	ContentHash string
	Manifest    Manifest
	TaskID      string
}

// Service coordinates request validation and durable acceptance. Worker
// startup is added separately; this service never performs graph materialization.
type Service struct {
	repository SnapshotAccepter
	nextTaskID func() (string, error)
}

func NewService(repository SnapshotAccepter, nextTaskID func() (string, error)) *Service {
	if nextTaskID == nil {
		nextTaskID = randomTaskID
	}
	return &Service{repository: repository, nextTaskID: nextTaskID}
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

// Put validates and fully materializes a full or delta request before one
// durable acceptance transaction. A missing/invalid base and a hash mismatch
// exit before the repository is asked to create a namespace or task.
func (s *Service) Put(ctx context.Context, namespace, version string, request Request) (SubmissionCheck, *Error) {
	if existing, graphErr := CheckExistingSubmission(ctx, s.repository, namespace, version, request.ContentHash); graphErr != nil || existing.Existing {
		return existing, graphErr
	}
	manifest, baseVersion, graphErr := s.materialize(ctx, namespace, request)
	if graphErr != nil {
		return SubmissionCheck{}, graphErr
	}
	_, actualHash, err := CanonicalHash(manifest.Nodes, manifest.Edges)
	if err != nil {
		return SubmissionCheck{}, NewError(CodeInternalError, nil, err)
	}
	if actualHash != request.ContentHash {
		return SubmissionCheck{}, NewError(CodeContentHashMismatch, map[string]any{"expected": request.ContentHash, "actual": actualHash}, nil)
	}
	taskID, err := s.nextTaskID()
	if err != nil {
		return SubmissionCheck{}, NewError(CodeInternalError, nil, err)
	}
	snapshot, err := s.repository.AcceptGraphSnapshot(ctx, AcceptedSnapshot{Namespace: namespace, Version: version, BaseVersion: baseVersion, ContentHash: actualHash, Manifest: manifest, TaskID: taskID})
	if err != nil {
		return SubmissionCheck{}, NewError(CodeGraphStoreUnavailable, nil, err)
	}
	return SubmissionCheck{Snapshot: snapshot}, nil
}

func (s *Service) materialize(ctx context.Context, namespace string, request Request) (Manifest, string, *Error) {
	switch request.Mode {
	case ModeFull:
		manifest, err := NormalizeFull(request)
		return manifest, "", err
	case ModeDelta:
		base, found, err := s.repository.ReadGraphSnapshotBase(ctx, namespace, request.BaseVersion)
		if err != nil {
			return Manifest{}, "", NewError(CodeGraphStoreUnavailable, nil, err)
		}
		if !found {
			return Manifest{}, "", NewError(CodeBaseSnapshotNotFound, map[string]any{"base_version": request.BaseVersion}, nil)
		}
		if base.Status != SnapshotReady {
			return Manifest{}, "", NewError(CodeBaseSnapshotNotReady, map[string]any{"base_version": request.BaseVersion}, nil)
		}
		manifest, graphErr := ApplyDelta(base.Manifest, request)
		return manifest, request.BaseVersion, graphErr
	default:
		return Manifest{}, "", NewError(CodeInvalidSnapshotRequest, map[string]any{"field": "mode"}, nil)
	}
}

func randomTaskID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return "graph-" + hex.EncodeToString(bytes), nil
}
