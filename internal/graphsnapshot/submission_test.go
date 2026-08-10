package graphsnapshot

import (
	"context"
	"errors"
	"testing"
)

type existingSnapshotReaderFake struct {
	snapshot Snapshot
	found    bool
	err      error
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
