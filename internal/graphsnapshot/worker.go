package graphsnapshot

import (
	"context"
	"fmt"
)

// GraphTaskClaimer is deliberately limited to durable claim operations. The
// dispatcher receives a claimed task only after its SQLite transaction has
// committed, keeping all provider work outside that transaction.
type GraphTaskClaimer interface {
	ClaimOldestQueuedGraphTask(context.Context) (Task, bool, error)
}

type GraphTaskRecovery interface {
	RecoverGraphTasks(context.Context) error
}

type TaskDispatcher func(context.Context, Task) error

// GraphTaskProcessor is the repository boundary used by Dispatch. Provider
// calls are made by BuildGraphVectors and therefore remain outside SQLite
// transactions; every failure is converted to a durable component outcome.
type GraphTaskProcessor interface {
	LookupGraphSnapshot(context.Context, string, string) (Snapshot, bool, error)
	AdvanceGraphTaskProgress(context.Context, string, string, int) (bool, error)
	PromoteGraphComponent(context.Context, string) error
	PopulateGraphSearchDocuments(context.Context, string) error
	BuildGraphVectors(context.Context, string, Embedder) error
	MarkGraphVectorUnavailable(context.Context, string, string) error
	MarkGraphVectorFailed(context.Context, string, *Error, string) error
	FailRequiredGraphComponent(context.Context, string, ComponentName, *Error) error
	ReconcileGraphSnapshot(context.Context, string, string) error
}

// Start launches one graph-task worker. Calling it repeatedly is idempotent;
// Wake coalesces submissions and Close cancels the dispatcher then waits for
// the worker to leave its current dispatch boundary.
func (s *Service) Start(parent context.Context, dispatch TaskDispatcher) error {
	claimer, ok := s.repository.(GraphTaskClaimer)
	if !ok {
		return fmt.Errorf("graph snapshot repository cannot claim tasks")
	}
	if recovery, ok := s.repository.(GraphTaskRecovery); ok {
		if err := recovery.RecoverGraphTasks(parent); err != nil {
			return fmt.Errorf("recover graph tasks: %w", err)
		}
	}
	if dispatch == nil {
		return fmt.Errorf("graph task dispatcher is required")
	}
	s.workerMu.Lock()
	defer s.workerMu.Unlock()
	if s.workerCancel != nil {
		return nil
	}
	ctx, cancel := context.WithCancel(parent)
	s.workerCancel = cancel
	s.workerWG.Add(1)
	go func() {
		defer s.workerWG.Done()
		for {
			task, found, err := claimer.ClaimOldestQueuedGraphTask(ctx)
			if err == nil && found {
				_ = dispatch(ctx, task)
				continue
			}
			select {
			case <-ctx.Done():
				return
			case <-s.wake:
			}
		}
	}()
	s.Wake()
	return nil
}

func (s *Service) Wake() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Dispatch runs one durable task from the next incomplete component boundary.
// A required failure is terminal; a vector failure is a successful degraded
// completion once graph and FTS are ready.
func (s *Service) Dispatch(ctx context.Context, task Task, embedder Embedder) error {
	processor, ok := s.repository.(GraphTaskProcessor)
	if !ok {
		return fmt.Errorf("graph snapshot repository cannot process tasks")
	}
	snapshot, found, err := processor.LookupGraphSnapshot(ctx, task.Namespace, task.Version)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("graph task snapshot is missing")
	}
	states := make(map[ComponentName]ComponentState, len(snapshot.Components))
	for _, component := range snapshot.Components {
		states[component.Name] = component.State
	}
	if states[ComponentGraph] != ComponentReady {
		if _, err = processor.AdvanceGraphTaskProgress(ctx, task.ID, "graph", 10); err != nil {
			return err
		}
		if err = processor.PromoteGraphComponent(ctx, task.ID); err != nil {
			return processor.FailRequiredGraphComponent(ctx, task.ID, ComponentGraph, NewError(CodeInternalError, nil, err))
		}
	}
	if states[ComponentFTS] != ComponentReady {
		if _, err = processor.AdvanceGraphTaskProgress(ctx, task.ID, "fts", 50); err != nil {
			return err
		}
		if err = processor.PopulateGraphSearchDocuments(ctx, task.ID); err != nil {
			return processor.FailRequiredGraphComponent(ctx, task.ID, ComponentFTS, NewError(CodeInternalError, nil, err))
		}
	}
	if states[ComponentVector] != ComponentReady && states[ComponentVector] != ComponentFailed && states[ComponentVector] != ComponentUnavailable {
		if _, err = processor.AdvanceGraphTaskProgress(ctx, task.ID, "vector", 75); err != nil {
			return err
		}
		if embedder == nil {
			if err = processor.MarkGraphVectorUnavailable(ctx, task.ID, "graph vector provider is unavailable"); err != nil {
				return err
			}
		} else if err = processor.BuildGraphVectors(ctx, task.ID, embedder); err != nil {
			if err = processor.MarkGraphVectorFailed(ctx, task.ID, NewError(CodeInternalError, nil, err), "graph vector generation failed"); err != nil {
				return err
			}
		}
	}
	return processor.ReconcileGraphSnapshot(ctx, task.Namespace, task.Version)
}

func (s *Service) Close() {
	s.workerMu.Lock()
	cancel := s.workerCancel
	s.workerCancel = nil
	s.workerMu.Unlock()
	if cancel != nil {
		cancel()
		s.workerWG.Wait()
	}
}
