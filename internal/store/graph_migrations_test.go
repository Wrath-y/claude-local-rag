package store

import "testing"

func TestGraphMigrationLedgerIsTransactionalAndChecksummed(t *testing.T) {
	s, err := New(t.TempDir()+"/rag.db", 4)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	m := graphMigration{component: "graph", version: 1, sql: `CREATE TABLE graph_migration_fixture (id INTEGER PRIMARY KEY)`}
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
