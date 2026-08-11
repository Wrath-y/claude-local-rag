package store

import (
	"context"
	"sync"
	"testing"
)

func TestActivateGraphSnapshotRequiresReadyAndIsIdempotent(t *testing.T) {
	s, err := New(t.TempDir()+"/rag.db", 4)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	seedGraphSnapshot(t, s, "project", "building", "building")
	if _, err := s.ActivateGraphSnapshot(context.Background(), "project", "building"); err == nil {
		t.Fatal("activated building snapshot")
	}
	seedGraphSnapshot(t, s, "project", "ready", "ready")
	if _, err := s.DB().Exec(`UPDATE graph_snapshots SET status='ready',query_ready=1 WHERE namespace='project' AND version='ready'`); err != nil {
		t.Fatal(err)
	}
	changed, err := s.ActivateGraphSnapshot(context.Background(), "project", "ready")
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	changed, err = s.ActivateGraphSnapshot(context.Background(), "project", "ready")
	if err != nil || changed {
		t.Fatalf("replay changed=%v err=%v", changed, err)
	}
	var active string
	if err = s.DB().QueryRow(`SELECT active_version FROM graph_namespace_heads WHERE namespace='project'`).Scan(&active); err != nil || active != "ready" {
		t.Fatalf("active=%q err=%v", active, err)
	}
}

func TestConcurrentActivationKeepsOneValidNamespaceHead(t *testing.T) {
	s, err := New(t.TempDir()+"/rag.db", 4)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, version := range []string{"one", "two"} {
		seedGraphSnapshot(t, s, "project", version, version)
		if _, err := s.DB().Exec(`UPDATE graph_snapshots SET status='ready',query_ready=1 WHERE namespace='project' AND version=?`, version); err != nil {
			t.Fatal(err)
		}
	}
	var group sync.WaitGroup
	for _, version := range []string{"one", "two"} {
		group.Add(1)
		go func(version string) {
			defer group.Done()
			_, _ = s.ActivateGraphSnapshot(context.Background(), "project", version)
		}(version)
	}
	group.Wait()
	var active string
	if err := s.DB().QueryRow(`SELECT active_version FROM graph_namespace_heads WHERE namespace='project'`).Scan(&active); err != nil || (active != "one" && active != "two") {
		t.Fatalf("active=%q err=%v", active, err)
	}
	var count int
	if err := s.DB().QueryRow(`SELECT count(*) FROM graph_snapshots WHERE namespace='project'`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("snapshots=%d err=%v", count, err)
	}
}
