package store

import "testing"

func TestGraphMigrationLedgerIsTransactionalAndChecksummed(t *testing.T) {
	s, err := New(t.TempDir()+"/rag.db", 4)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	m := graphMigration{component: "graph-test", version: 1, sql: `CREATE TABLE graph_migration_fixture (id INTEGER PRIMARY KEY)`}
	if err := runGraphMigrations(s.DB(), []graphMigration{m}); err != nil {
		t.Fatal(err)
	}
	if err := runGraphMigrations(s.DB(), []graphMigration{m}); err != nil {
		t.Fatal(err)
	}
	m.sql = `CREATE TABLE changed_fixture (id INTEGER PRIMARY KEY)`
	if err := runGraphMigrations(s.DB(), []graphMigration{m}); err == nil {
		t.Fatal("accepted changed migration")
	}
}

func TestGraphMigrationCreatesSnapshotCoreTables(t *testing.T) {
	s, err := New(t.TempDir()+"/rag.db", 4)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, name := range []string{"graph_namespaces", "graph_snapshots", "graph_namespace_heads", "graph_snapshot_components", "graph_nodes", "graph_edges", "graph_snapshot_staging", "graph_tasks"} {
		var got string
		if err := s.DB().QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&got); err != nil || got != name {
			t.Fatalf("table %s: %v", name, err)
		}
	}
}
