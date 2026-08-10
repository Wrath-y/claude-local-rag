package graphsnapshot

import (
	"context"
	"errors"
	"testing"
	"time"
)

type existingSnapshotReaderFake struct {
	snapshot Snapshot
	found    bool
	err      error
}

type snapshotAccepterFake struct {
	existingSnapshotReaderFake
	base      SnapshotBase
	baseFound bool
	accepted  *AcceptedSnapshot
}

func (f *snapshotAccepterFake) ReadGraphSnapshotBase(context.Context, string, string) (SnapshotBase, bool, error) {
	return f.base, f.baseFound, nil
}

func (f *snapshotAccepterFake) AcceptGraphSnapshot(_ context.Context, accepted AcceptedSnapshot) (Snapshot, error) {
	f.accepted = &accepted
	return Snapshot{Namespace: accepted.Namespace, Version: accepted.Version, ContentHash: accepted.ContentHash, TaskID: accepted.TaskID, Status: SnapshotBuilding}, nil
}

type raceSnapshotAccepterFake struct {
	lookups int
	hash    string
}

type workerRepositoryFake struct {
	snapshotAccepterFake
	tasks chan Task
}

func (f *workerRepositoryFake) ClaimOldestQueuedGraphTask(context.Context) (Task, bool, error) {
	select {
	case task := <-f.tasks:
		return task, true, nil
	default:
		return Task{}, false, nil
	}
}

func (f *raceSnapshotAccepterFake) LookupGraphSnapshot(context.Context, string, string) (Snapshot, bool, error) {
	f.lookups++
	if f.lookups == 1 {
		return Snapshot{}, false, nil
	}
	return Snapshot{ContentHash: f.hash, TaskID: "original-task", Status: SnapshotBuilding}, true, nil
}
func (f *raceSnapshotAccepterFake) ReadGraphSnapshotBase(context.Context, string, string) (SnapshotBase, bool, error) {
	return SnapshotBase{}, false, nil
}
func (f *raceSnapshotAccepterFake) AcceptGraphSnapshot(context.Context, AcceptedSnapshot) (Snapshot, error) {
	return Snapshot{}, ErrSnapshotAlreadyAccepted
}

func (f existingSnapshotReaderFake) LookupGraphSnapshot(context.Context, string, string) (Snapshot, bool, error) {
	return f.snapshot, f.found, f.err
}

func TestCheckExistingSubmissionReplaysEveryLifecycleState(t *testing.T) {
	for _, status := range []SnapshotStatus{SnapshotBuilding, SnapshotReady, SnapshotFailed} {
		t.Run(string(status), func(t *testing.T) {
			snapshot := Snapshot{Namespace: "project", Version: "revision", ContentHash: testHash, TaskID: "original-task", Status: status}
			result, graphErr := CheckExistingSubmission(context.Background(), existingSnapshotReaderFake{snapshot: snapshot, found: true}, snapshot.Namespace, snapshot.Version, testHash)
			if graphErr != nil || !result.Existing || result.Snapshot.TaskID != "original-task" || result.Snapshot.Status != status {
				t.Fatalf("result=%#v error=%#v", result, graphErr)
			}
		})
	}
}

func TestCheckExistingSubmissionRejectsConflictsAndDoesNotTreatMissingAsReplay(t *testing.T) {
	if result, graphErr := CheckExistingSubmission(context.Background(), existingSnapshotReaderFake{}, "project", "revision", testHash); graphErr != nil || result.Existing {
		t.Fatalf("missing result=%#v error=%#v", result, graphErr)
	}
	if _, graphErr := CheckExistingSubmission(context.Background(), existingSnapshotReaderFake{snapshot: Snapshot{ContentHash: testHash}, found: true}, "project", "revision", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"); graphErr == nil || graphErr.Code != CodeContentHashConflict {
		t.Fatalf("conflict error=%#v", graphErr)
	}
	if _, graphErr := CheckExistingSubmission(context.Background(), existingSnapshotReaderFake{err: errors.New("offline")}, "project", "revision", testHash); graphErr == nil || graphErr.Code != CodeGraphStoreUnavailable {
		t.Fatalf("store error=%#v", graphErr)
	}
}

func TestServicePutAcceptsOnlyVerifiedCanonicalFullAndDeltaManifests(t *testing.T) {
	full := Manifest{SchemaVersion: SchemaVersionV1, Nodes: []Node{testNode("a")}}
	_, fullHash, err := CanonicalHash(full.Nodes, full.Edges)
	if err != nil {
		t.Fatal(err)
	}
	repository := &snapshotAccepterFake{}
	service := NewService(repository, func() (string, error) { return "task-full", nil })
	result, graphErr := service.Put(context.Background(), "project", "full", Request{SchemaVersion: SchemaVersionV1, Mode: ModeFull, ContentHash: fullHash, Nodes: full.Nodes})
	if graphErr != nil || result.Existing || repository.accepted == nil || repository.accepted.TaskID != "task-full" || len(repository.accepted.Manifest.Nodes) != 1 {
		t.Fatalf("result=%#v accepted=%#v error=%#v", result, repository.accepted, graphErr)
	}

	base := Manifest{SchemaVersion: SchemaVersionV1, Nodes: []Node{testNode("a")}}
	deltaResult := Manifest{SchemaVersion: SchemaVersionV1, Nodes: []Node{testNode("a"), testNode("b")}}
	_, deltaHash, err := CanonicalHash(deltaResult.Nodes, deltaResult.Edges)
	if err != nil {
		t.Fatal(err)
	}
	repository = &snapshotAccepterFake{base: SnapshotBase{Status: SnapshotReady, Manifest: base}, baseFound: true}
	service = NewService(repository, func() (string, error) { return "task-delta", nil })
	_, graphErr = service.Put(context.Background(), "project", "delta", Request{SchemaVersion: SchemaVersionV1, Mode: ModeDelta, BaseVersion: "base", ContentHash: deltaHash, NodeUpserts: []Node{testNode("b")}})
	if graphErr != nil || repository.accepted == nil || repository.accepted.BaseVersion != "base" || len(repository.accepted.Manifest.Nodes) != 2 {
		t.Fatalf("delta accepted=%#v error=%#v", repository.accepted, graphErr)
	}

	repository = &snapshotAccepterFake{}
	service = NewService(repository, func() (string, error) { return "must-not-be-used", nil })
	if _, graphErr = service.Put(context.Background(), "project", "bad", Request{SchemaVersion: SchemaVersionV1, Mode: ModeFull, ContentHash: testHash, Nodes: full.Nodes}); graphErr == nil || graphErr.Code != CodeContentHashMismatch || repository.accepted != nil {
		t.Fatalf("mismatch accepted=%#v error=%#v", repository.accepted, graphErr)
	}
}

func TestServicePutReloadsOriginalSnapshotAfterSameVersionRace(t *testing.T) {
	manifest := Manifest{SchemaVersion: SchemaVersionV1, Nodes: []Node{testNode("a")}}
	_, hash, err := CanonicalHash(manifest.Nodes, manifest.Edges)
	if err != nil {
		t.Fatal(err)
	}
	if hash == testHash {
		t.Fatal("test fixture unexpectedly used the fixed hash")
	}
	repository := &raceSnapshotAccepterFake{hash: testHash}
	service := NewService(repository, func() (string, error) { return "losing-task", nil })
	_, graphErr := service.Put(context.Background(), "project", "version", Request{SchemaVersion: SchemaVersionV1, Mode: ModeFull, ContentHash: hash, Nodes: manifest.Nodes})
	if graphErr == nil || graphErr.Code != CodeContentHashConflict {
		t.Fatalf("different winner hash error=%#v", graphErr)
	}

	// The same-hash path is the only race replay that may return the original
	// task. Use a winner carrying the exact normalized hash.
	repository = &raceSnapshotAccepterFake{hash: hash}
	service = NewService(repository, func() (string, error) { return "losing-task", nil })
	result, graphErr := service.Put(context.Background(), "project", "version", Request{SchemaVersion: SchemaVersionV1, Mode: ModeFull, ContentHash: hash, Nodes: manifest.Nodes})
	if graphErr != nil || !result.Existing || result.Snapshot.TaskID != "original-task" {
		t.Fatalf("same-hash race result=%#v error=%#v", result, graphErr)
	}
}

func TestServiceWorkerWakeAndClose(t *testing.T) {
	repository := &workerRepositoryFake{tasks: make(chan Task, 1)}
	service := NewService(repository, nil)
	dispatched := make(chan Task, 1)
	if err := service.Start(context.Background(), func(_ context.Context, task Task) error { dispatched <- task; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := service.Start(context.Background(), func(context.Context, Task) error { t.Fatal("second worker dispatch"); return nil }); err != nil {
		t.Fatal(err)
	}
	repository.tasks <- Task{ID: "task", State: TaskRunning}
	service.Wake()
	select {
	case task := <-dispatched:
		if task.ID != "task" {
			t.Fatalf("task=%#v", task)
		}
	case <-time.After(time.Second):
		t.Fatal("worker was not woken")
	}
	service.Close()

	blocked := make(chan struct{})
	repository = &workerRepositoryFake{tasks: make(chan Task, 1)}
	service = NewService(repository, nil)
	if err := service.Start(context.Background(), func(ctx context.Context, _ Task) error { close(blocked); <-ctx.Done(); return ctx.Err() }); err != nil {
		t.Fatal(err)
	}
	repository.tasks <- Task{ID: "blocking", State: TaskRunning}
	service.Wake()
	select {
	case <-blocked:
	case <-time.After(time.Second):
		t.Fatal("blocking dispatch did not start")
	}
	done := make(chan struct{})
	go func() { service.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("close did not cancel dispatcher")
	}
}
