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

type TaskDispatcher func(context.Context, Task) error

// Start launches one graph-task worker. Calling it repeatedly is idempotent;
// Wake coalesces submissions and Close cancels the dispatcher then waits for
// the worker to leave its current dispatch boundary.
func (s *Service) Start(parent context.Context, dispatch TaskDispatcher) error {
	claimer, ok := s.repository.(GraphTaskClaimer)
	if !ok {
		return fmt.Errorf("graph snapshot repository cannot claim tasks")
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
