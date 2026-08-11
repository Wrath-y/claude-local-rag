package store

import (
	"context"
	"testing"
)

func TestDeleteGraphSnapshotGuardsActiveAndWriterThenCascades(t *testing.T) {
	s, err := New(t.TempDir()+"/rag.db", 4)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	seedGraphSnapshot(t, s, "project", "target", "target")
	if _, err := s.DB().Exec(`INSERT INTO graph_tasks(id,namespace,version,state,phase,created_at) VALUES('task-project-target','project','target','queued','queued','2026-08-10T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteGraphSnapshot(context.Background(), "project", "target"); err == nil {
		t.Fatal("deleted while writer queued")
	}
	if _, err := s.DB().Exec(`UPDATE graph_tasks SET state='failed' WHERE id='task-project-target'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`INSERT INTO graph_namespace_heads(namespace,active_version) VALUES('project','target')`); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteGraphSnapshot(context.Background(), "project", "target"); err == nil {
		t.Fatal("deleted active snapshot")
	}
	if _, err := s.DB().Exec(`DELETE FROM graph_namespace_heads WHERE namespace='project'`); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteGraphSnapshot(context.Background(), "project", "target"); err != nil {
		t.Fatal(err)
	}
	var snapshots, tasks, nodes int
	if err := s.DB().QueryRow(`SELECT count(*) FROM graph_snapshots WHERE namespace='project' AND version='target'`).Scan(&snapshots); err != nil || snapshots != 0 {
		t.Fatalf("snapshots=%d err=%v", snapshots, err)
	}
	if err := s.DB().QueryRow(`SELECT count(*) FROM graph_tasks WHERE namespace='project' AND version='target'`).Scan(&tasks); err != nil || tasks != 0 {
		t.Fatalf("tasks=%d err=%v", tasks, err)
	}
	if err := s.DB().QueryRow(`SELECT count(*) FROM graph_nodes WHERE namespace='project' AND version='target'`).Scan(&nodes); err != nil || nodes != 0 {
		t.Fatalf("nodes=%d err=%v", nodes, err)
	}
}
