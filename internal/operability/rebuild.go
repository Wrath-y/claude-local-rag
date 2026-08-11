package operability

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/Wrath-y/local-rag/internal/graphsnapshot"
)

type RebuildRepository interface {
	AdmitGraphRebuild(context.Context, string, string, string, string, string, string, []Component) (graphsnapshot.Task, bool, error)
}

type WorkerWaker interface{ Wake() }

type RebuildService struct {
	Repository RebuildRepository
	Waker      WorkerWaker
	NewTaskID  func() string
}

func (s RebuildService) Submit(ctx context.Context, namespace, version, idempotencyKey, requestID string, request RebuildRequest) (RebuildSubmission, *graphsnapshot.Error) {
	if s.Repository == nil {
		return RebuildSubmission{}, graphsnapshot.NewError(graphsnapshot.CodeGraphStoreUnavailable, nil, nil)
	}
	if err := ValidateIdempotencyKey(idempotencyKey); err != nil {
		return RebuildSubmission{}, graphsnapshot.NewError(graphsnapshot.CodeInvalidRebuildRequest, map[string]any{"field": "Idempotency-Key"}, err)
	}
	components, err := NormalizeComponents(request.Components)
	if err != nil {
		return RebuildSubmission{}, graphsnapshot.NewError(graphsnapshot.CodeInvalidRebuildRequest, map[string]any{"field": "components"}, err)
	}
	taskID := "graph-rebuild-" + uuid.NewString()
	if s.NewTaskID != nil {
		taskID = s.NewTaskID()
	}
	task, replayed, err := s.Repository.AdmitGraphRebuild(ctx, namespace, version, idempotencyKey, RequestFingerprint(components), requestID, taskID, components)
	if err != nil {
		return RebuildSubmission{}, rebuildError(err)
	}
	if !replayed && s.Waker != nil {
		s.Waker.Wake()
	}
	return RebuildSubmission{TaskID: task.ID, State: task.State, TaskURL: fmt.Sprintf("/v1/tasks/%s", task.ID), Components: components, Namespace: namespace, SnapshotVersion: version, Replayed: replayed}, nil
}

func rebuildError(err error) *graphsnapshot.Error {
	switch {
	case errors.Is(err, ErrReimportRequired):
		return graphsnapshot.NewError(graphsnapshot.CodeReimportRequired, nil, nil)
	case errors.Is(err, ErrIdempotencyConflict):
		return graphsnapshot.NewError(graphsnapshot.CodeIdempotencyConflict, nil, nil)
	case errors.Is(err, ErrSnapshotNotFound):
		return graphsnapshot.NewError(graphsnapshot.CodeSnapshotNotFound, nil, nil)
	default:
		return graphsnapshot.NewError(graphsnapshot.CodeInternalError, nil, err)
	}
}
