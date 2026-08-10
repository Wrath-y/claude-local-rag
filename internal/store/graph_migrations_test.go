package store

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGraphMigrationLedgerIsTransactionalAndChecksummed(t *testing.T) {
	s, err := New(t.TempDir()+"/rag.db", 4)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
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

func TestGraphMigrationFailureRetainsLegacyStoreAndDiagnostic(t *testing.T) {
	path := t.TempDir() + "/rag.db"
	s, err := newWithGraphMigrations(path, 4, []graphMigration{{component: "graph", version: 1, sql: `CREATE TABLE graph_partial_fixture (id INTEGER PRIMARY KEY); THIS IS INVALID;`}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if err := s.GraphUnavailable(); !errors.Is(err, ErrGraphUnavailable) {
		t.Fatalf("graph diagnostic=%v, want ErrGraphUnavailable", err)
	}
	if _, err := s.InsertChunk("legacy remains usable", "fixture", "legacy-md5", "", "", []float32{1, 0, 0, 0}); err != nil {
		t.Fatalf("legacy write after graph migration failure: %v", err)
	}
	if count, err := s.ChunkCount(); err != nil || count != 1 {
		t.Fatalf("legacy count=%d err=%v", count, err)
	}
	var partial int
	if err := s.DB().QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='graph_partial_fixture'`).Scan(&partial); err != nil || partial != 0 {
		t.Fatalf("partial graph table count=%d err=%v", partial, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = New(path, 4)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.GraphUnavailable(); err != nil {
		t.Fatalf("graph migration did not recover on reopen: %v", err)
	}
}

func TestGraphMigrationUpgradesVersionedPreGraphFixtureWithoutLegacyDataLoss(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rag.db")
	fixture, err := os.ReadFile(filepath.Join("testdata", "pre-graph-store-v1.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(fixture), "fixture_version: pre-graph-store/v1") {
		t.Fatal("fixture is missing its version marker")
	}
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = legacy.Exec(string(fixture)); err != nil {
		legacy.Close()
		t.Fatal(err)
	}
	if err = legacy.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := New(path, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if err := s.GraphUnavailable(); err != nil {
		t.Fatalf("graph should be available after fixture upgrade: %v", err)
	}
	var text, source, md5 string
	if err := s.DB().QueryRow(`SELECT text,source,md5 FROM chunks WHERE id=1`).Scan(&text, &source, &md5); err != nil {
		t.Fatal(err)
	}
	if text != "legacy searchable text" || source != "legacy-source" || md5 != "legacy-md5" {
		t.Fatalf("legacy chunk changed: text=%q source=%q md5=%q", text, source, md5)
	}
	var vectorCount, ftsCount, migrationCount int
	if err := s.DB().QueryRow(`SELECT count(*) FROM vec_chunks WHERE chunk_id=1`).Scan(&vectorCount); err != nil || vectorCount != 1 {
		t.Fatalf("legacy vector count=%d err=%v", vectorCount, err)
	}
	if err := s.DB().QueryRow(`SELECT count(*) FROM chunks_fts WHERE chunks_fts MATCH 'searchable'`).Scan(&ftsCount); err != nil || ftsCount != 1 {
		t.Fatalf("legacy fts count=%d err=%v", ftsCount, err)
	}
	if err := s.DB().QueryRow(`SELECT count(*) FROM schema_migrations WHERE component='graph' AND version=1`).Scan(&migrationCount); err != nil || migrationCount != 1 {
		t.Fatalf("graph migration count=%d err=%v", migrationCount, err)
	}
	var integrity string
	if err := s.DB().QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity=%q err=%v", integrity, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = New(path, 4)
	if err != nil {
		t.Fatal(err)
	}
	if count, err := s.ChunkCount(); err != nil || count != 1 {
		t.Fatalf("legacy count after repeated open=%d err=%v", count, err)
	}
}

func TestGraphMigrationCreatesSnapshotCoreTables(t *testing.T) {
	s, err := New(t.TempDir()+"/rag.db", 4)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, name := range []string{"graph_namespaces", "graph_snapshots", "graph_namespace_heads", "graph_snapshot_components", "graph_nodes", "graph_edges", "graph_snapshot_staging", "graph_search_documents", "graph_search_fts", "graph_vector_items", "graph_vectors", "graph_tasks"} {
		var got string
		if err := s.DB().QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&got); err != nil || got != name {
			t.Fatalf("table %s: %v", name, err)
		}
	}
}

func TestGraphSearchAndVectorRowsAreScopedAndCleanedUp(t *testing.T) {
	s, err := New(t.TempDir()+"/rag.db", 4)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	const namespace = "project-search"
	const version = "revision-search"
	const taskID = "graph-task-search"
	const timestamp = "2026-08-10T00:00:00Z"
	const hash = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if _, err = s.DB().Exec(`INSERT INTO graph_namespaces(namespace,created_at) VALUES(?,?)`, namespace, timestamp); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB().Exec(`INSERT INTO graph_snapshots(namespace,version,schema_version,content_hash,task_id,status,query_ready,created_at,updated_at) VALUES(?,?,?,?,?,'building',0,?,?)`, namespace, version, "1.0", hash, taskID, timestamp, timestamp); err != nil {
		t.Fatal(err)
	}
	document, err := s.DB().Exec(`INSERT INTO graph_search_documents(namespace,version,entity_kind,entity_id,search_text) VALUES(?,?,?,?,?)`, namespace, version, "node", "node-search", "isolated searchable document")
	if err != nil {
		t.Fatal(err)
	}
	documentID, err := document.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	var ftsID int64
	if err := s.DB().QueryRow(`SELECT rowid FROM graph_search_fts WHERE graph_search_fts MATCH 'searchable'`).Scan(&ftsID); err != nil || ftsID != documentID {
		t.Fatalf("fts row=%d err=%v, want %d", ftsID, err, documentID)
	}
	item, err := s.DB().Exec(`INSERT INTO graph_vector_items(namespace,version,generation,entity_kind,entity_id,dimensions) VALUES(?,?,?,?,?,?)`, namespace, version, "private-generation", "node", "node-search", 4)
	if err != nil {
		t.Fatal(err)
	}
	itemID, err := item.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB().Exec(`INSERT INTO graph_vectors(item_id,embedding) VALUES(?,?)`, itemID, Float32ToBytes([]float32{1, 0, 0, 0})); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB().Exec(`DELETE FROM graph_vector_items WHERE id=?`, itemID); err != nil {
		t.Fatal(err)
	}
	var vectorCount int
	if err := s.DB().QueryRow(`SELECT count(*) FROM graph_vectors WHERE item_id=?`, itemID).Scan(&vectorCount); err != nil || vectorCount != 0 {
		t.Fatalf("vector count=%d err=%v", vectorCount, err)
	}
	if _, err = s.DB().Exec(`DELETE FROM graph_snapshots WHERE namespace=? AND version=?`, namespace, version); err != nil {
		t.Fatal(err)
	}
	var ftsCount int
	if err := s.DB().QueryRow(`SELECT count(*) FROM graph_search_fts WHERE rowid=?`, documentID).Scan(&ftsCount); err != nil || ftsCount != 0 {
		t.Fatalf("fts cleanup count=%d err=%v", ftsCount, err)
	}
}

func TestGraphMigrationDefinesScopedIndexesAndUsesThemForScopedQueries(t *testing.T) {
	s, err := New(t.TempDir()+"/rag.db", 4)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	for name, want := range map[string]string{
		"graph_snapshots_namespace_status_version_idx":           "graph_snapshots(namespace,status,version)",
		"graph_snapshots_namespace_base_version_idx":             "graph_snapshots(namespace,base_version,version)",
		"graph_tasks_state_created_id_idx":                       "graph_tasks(state,created_at,id)",
		"graph_snapshot_components_namespace_version_state_idx":  "graph_snapshot_components(namespace,version,state,component)",
		"graph_edges_namespace_version_from_idx":                 "graph_edges(namespace,version,from_node_id,edge_id)",
		"graph_edges_namespace_version_to_idx":                   "graph_edges(namespace,version,to_node_id,edge_id)",
		"graph_search_documents_namespace_version_id_idx":        "graph_search_documents(namespace,version,id)",
		"graph_vector_items_namespace_version_generation_id_idx": "graph_vector_items(namespace,version,generation,id)",
	} {
		var definition string
		if err := s.DB().QueryRow(`SELECT sql FROM sqlite_master WHERE type='index' AND name=?`, name).Scan(&definition); err != nil {
			t.Fatalf("index %s: %v", name, err)
		}
		if !strings.Contains(strings.ReplaceAll(definition, " ", ""), want) {
			t.Fatalf("index %s definition=%q, want %q", name, definition, want)
		}
	}

	assertQueryPlanUses(t, s, `SELECT version FROM graph_snapshots WHERE namespace=? AND status=?`, "graph_snapshots_namespace_status_version_idx", "namespace", "building")
	assertQueryPlanUses(t, s, `SELECT version FROM graph_snapshots WHERE namespace=? AND base_version=?`, "graph_snapshots_namespace_base_version_idx", "namespace", "base")
	assertQueryPlanUses(t, s, `SELECT id FROM graph_tasks WHERE state=? ORDER BY created_at,id LIMIT 1`, "graph_tasks_state_created_id_idx", "queued")
	assertQueryPlanUses(t, s, `SELECT component FROM graph_snapshot_components WHERE namespace=? AND version=? AND state=?`, "graph_snapshot_components_namespace_version_state_idx", "namespace", "version", "pending")
	assertQueryPlanUses(t, s, `SELECT edge_id FROM graph_edges WHERE namespace=? AND version=? AND from_node_id=?`, "graph_edges_namespace_version_from_idx", "namespace", "version", "node")
	assertQueryPlanUses(t, s, `SELECT edge_id FROM graph_edges WHERE namespace=? AND version=? AND to_node_id=?`, "graph_edges_namespace_version_to_idx", "namespace", "version", "node")
	assertQueryPlanUses(t, s, `SELECT id FROM graph_search_documents WHERE namespace=? AND version=? ORDER BY id`, "graph_search_documents_namespace_version_id_idx", "namespace", "version")
	assertQueryPlanUses(t, s, `SELECT id FROM graph_vector_items WHERE namespace=? AND version=? AND generation=? ORDER BY id`, "graph_vector_items_namespace_version_generation_id_idx", "namespace", "version", "generation")
}

func TestGraphMigrationEnforcesLifecycleConstraintsAndImmutability(t *testing.T) {
	s, err := New(t.TempDir()+"/rag.db", 4)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	const namespace = "project-a"
	const version = "revision-a"
	const taskID = "graph-task-a"
	const timestamp = "2026-08-10T00:00:00Z"
	const hash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if _, err = s.DB().Exec(`INSERT INTO graph_namespaces(namespace,created_at) VALUES(?,?)`, namespace, timestamp); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB().Exec(`INSERT INTO graph_snapshots(namespace,version,schema_version,content_hash,task_id,status,query_ready,created_at,updated_at) VALUES(?,?,?,?,?,'building',0,?,?)`, namespace, version, "1.0", hash, taskID, timestamp, timestamp); err != nil {
		t.Fatal(err)
	}
	assertGraphConstraint(t, s, `UPDATE graph_snapshots SET content_hash=? WHERE namespace=? AND version=?`, strings.Repeat("b", 64), namespace, version)
	assertGraphConstraint(t, s, `INSERT INTO graph_snapshots(namespace,version,schema_version,content_hash,task_id,status,query_ready,created_at,updated_at) VALUES(?,?,?,?,?,'building',0,?,?)`, namespace, "invalid-hash", strings.Repeat("G", 64), "graph-task-invalid-hash", timestamp, timestamp)
	assertGraphConstraint(t, s, `INSERT INTO graph_snapshots(namespace,version,schema_version,content_hash,task_id,status,query_ready,created_at,updated_at) VALUES(?,?,?,?,?,'ready',0,?,?)`, namespace, "invalid-ready", "1.0", hash, "graph-task-invalid", timestamp, timestamp)
	assertGraphConstraint(t, s, `INSERT INTO graph_snapshot_components(namespace,version,component,state) VALUES(?,?,?,?)`, namespace, version, "unknown", "pending")
	assertGraphConstraint(t, s, `INSERT INTO graph_tasks(id,namespace,version,state,phase,created_at) VALUES(?,?,?,?,?,?)`, "other-task", namespace, version, "queued", "queued", timestamp)
	if _, err = s.DB().Exec(`INSERT INTO graph_tasks(id,namespace,version,state,phase,created_at) VALUES(?,?,?,?,?,?)`, taskID, namespace, version, "queued", "queued", timestamp); err != nil {
		t.Fatal(err)
	}
	assertGraphConstraint(t, s, `INSERT INTO graph_nodes(namespace,version,node_id,node_type,label,text,properties_json,provenance_json) VALUES(?,?,?,?,?,?,?,?)`, namespace, version, "node-a", "kind", "label", "text", `[]`, `{}`)
	if _, err = s.DB().Exec(`INSERT INTO graph_nodes(namespace,version,node_id,node_type,label,text,properties_json,provenance_json) VALUES(?,?,?,?,?,?,?,?)`, namespace, version, "node-a", "kind", "label", "text", `{}`, `{}`); err != nil {
		t.Fatal(err)
	}
	assertGraphConstraint(t, s, `UPDATE graph_nodes SET label='changed' WHERE namespace=? AND version=? AND node_id=?`, namespace, version, "node-a")
	assertGraphConstraint(t, s, `INSERT INTO graph_edges(namespace,version,edge_id,from_node_id,to_node_id,edge_type,relation_kind,confidence,properties_json,provenance_json) VALUES(?,?,?,?,?,?,?,?,?,?)`, namespace, version, "edge-a", "node-a", "missing", "kind", "explicit", "1", `{}`, `{}`)
	if _, err = s.DB().Exec(`INSERT INTO graph_namespace_heads(namespace,active_version) VALUES(?,?)`, namespace, version); err != nil {
		t.Fatal(err)
	}
	assertGraphConstraint(t, s, `DELETE FROM graph_snapshots WHERE namespace=? AND version=?`, namespace, version)

	rows, err := s.DB().Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		var table string
		var rowID, parent, fkID any
		if err := rows.Scan(&table, &rowID, &parent, &fkID); err != nil {
			t.Fatal(err)
		}
		t.Fatalf("foreign key check failed: table=%s row=%v parent=%v fk=%v", table, rowID, parent, fkID)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func assertGraphConstraint(t *testing.T, s *Store, statement string, args ...any) {
	t.Helper()
	if _, err := s.DB().Exec(statement, args...); err == nil {
		t.Fatalf("constraint unexpectedly accepted: %s", statement)
	} else if strings.TrimSpace(err.Error()) == "" {
		t.Fatalf("constraint returned an empty error: %s", statement)
	} else if strings.Contains(err.Error(), "database is locked") {
		t.Fatalf("constraint did not execute: %s: %v", statement, err)
	}
}

func assertQueryPlanUses(t *testing.T, s *Store, statement, index string, args ...any) {
	t.Helper()
	rows, err := s.DB().Query(`EXPLAIN QUERY PLAN `+statement, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var details []string
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatal(err)
		}
		details = append(details, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(details, "\n"), index) {
		t.Fatalf("plan for %q = %q, want %s", statement, details, index)
	}
}
