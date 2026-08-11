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

func TestGraphMigrationThreeRegistersLifecycleGenerationsWithoutRebuilding(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rag.db")
	legacy, err := newWithGraphMigrations(path, 4, graphMigrations[:2])
	if err != nil {
		t.Fatal(err)
	}
	const namespace = "project-migration"
	const version = "legacy-version"
	const taskID = "legacy-task"
	const hash = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	const timestamp = "2026-08-10T00:00:00Z"
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO graph_namespaces(namespace,created_at) VALUES(?,?)`, []any{namespace, timestamp}},
		{`INSERT INTO graph_snapshots(namespace,version,schema_version,content_hash,task_id,status,query_ready,created_at,updated_at) VALUES(?,?,?,?,?,'ready',1,?,?)`, []any{namespace, version, "1.0", hash, taskID, timestamp, timestamp}},
		{`INSERT INTO graph_snapshot_components(namespace,version,component,state) VALUES(?,?, 'graph','ready')`, []any{namespace, version}},
		{`INSERT INTO graph_snapshot_components(namespace,version,component,state) VALUES(?,?, 'fts','ready')`, []any{namespace, version}},
		{`INSERT INTO graph_snapshot_components(namespace,version,component,state,generation) VALUES(?,?, 'vector','ready','vector-legacy')`, []any{namespace, version}},
		{`INSERT INTO graph_search_documents(namespace,version,entity_kind,entity_id,search_text) VALUES(?,?, 'node','node-legacy','searchable legacy graph text')`, []any{namespace, version}},
		{`INSERT INTO graph_vector_items(namespace,version,generation,entity_kind,entity_id,dimensions) VALUES(?,?, 'vector-legacy','node','node-legacy',4)`, []any{namespace, version}},
	} {
		if _, err = legacy.DB().Exec(statement.query, statement.args...); err != nil {
			legacy.Close()
			t.Fatal(err)
		}
	}
	var itemID int64
	if err = legacy.DB().QueryRow(`SELECT id FROM graph_vector_items WHERE namespace=? AND version=?`, namespace, version).Scan(&itemID); err != nil {
		legacy.Close()
		t.Fatal(err)
	}
	if _, err = legacy.DB().Exec(`INSERT INTO graph_vectors(item_id,embedding) VALUES(?,?)`, itemID, Float32ToBytes([]float32{1, 0, 0, 0})); err != nil {
		legacy.Close()
		t.Fatal(err)
	}
	if err = legacy.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := New(path, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	var generation string
	if err = upgraded.DB().QueryRow(`SELECT generation FROM graph_search_documents WHERE namespace=? AND version=? AND entity_id='node-legacy'`, namespace, version).Scan(&generation); err != nil || generation != "fts-"+taskID {
		t.Fatalf("fts generation=%q err=%v", generation, err)
	}
	for _, want := range []struct{ component, generation string }{{"fts", "fts-" + taskID}, {"vector", "vector-legacy"}} {
		var state string
		if err = upgraded.DB().QueryRow(`SELECT state FROM graph_retrieval_generations WHERE namespace=? AND version=? AND component=? AND generation=?`, namespace, version, want.component, want.generation).Scan(&state); err != nil || state != "selected" {
			t.Fatalf("%s generation state=%q err=%v", want.component, state, err)
		}
	}
	var ftsRows, vectorRows, vectorBacking, migrations int
	if err = upgraded.DB().QueryRow(`SELECT count(*) FROM graph_search_fts WHERE graph_search_fts MATCH 'searchable'`).Scan(&ftsRows); err != nil || ftsRows != 1 {
		t.Fatalf("fts rows=%d err=%v", ftsRows, err)
	}
	if err = upgraded.DB().QueryRow(`SELECT count(*) FROM graph_vector_items WHERE namespace=? AND version=? AND generation='vector-legacy'`, namespace, version).Scan(&vectorRows); err != nil || vectorRows != 1 {
		t.Fatalf("vector rows=%d err=%v", vectorRows, err)
	}
	if err = upgraded.DB().QueryRow(`SELECT count(*) FROM graph_vectors WHERE item_id=?`, itemID).Scan(&vectorBacking); err != nil || vectorBacking != 1 {
		t.Fatalf("vector backing rows=%d err=%v", vectorBacking, err)
	}
	if err = upgraded.DB().QueryRow(`SELECT count(*) FROM schema_migrations WHERE component='graph' AND version=3`).Scan(&migrations); err != nil || migrations != 1 {
		t.Fatalf("graph/3 ledger entries=%d err=%v", migrations, err)
	}
}

func TestGraphMigrationFourPreservesGraphThreeRecordsAndRejectsChecksumChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rag.db")
	legacy, err := newWithGraphMigrations(path, 4, graphMigrations[:3])
	if err != nil {
		t.Fatal(err)
	}
	const namespace = "operability-upgrade"
	const version = "v3"
	const taskID = "build-v3"
	const hash = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	const timestamp = "2026-08-11T00:00:00Z"
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO graph_namespaces(namespace,created_at) VALUES(?,?)`, []any{namespace, timestamp}},
		{`INSERT INTO graph_snapshots(namespace,version,schema_version,content_hash,task_id,status,query_ready,created_at,updated_at) VALUES(?,?,?,?,?,'ready',1,?,?)`, []any{namespace, version, "1.0", hash, taskID, timestamp, timestamp}},
		{`INSERT INTO graph_tasks(id,namespace,version,state,phase,progress,created_at) VALUES(?,?,?,'succeeded','completed',100,?)`, []any{taskID, namespace, version, timestamp}},
		{`INSERT INTO graph_nodes(namespace,version,node_id,node_type,label,text,properties_json,provenance_json) VALUES(?,?, 'node','kind','label','text','{}','{}')`, []any{namespace, version}},
		{`INSERT INTO graph_snapshot_components(namespace,version,component,state,generation) VALUES(?,?, 'fts','ready','fts-v3')`, []any{namespace, version}},
		{`INSERT INTO graph_snapshot_components(namespace,version,component,state,generation) VALUES(?,?, 'vector','ready','vector-v3')`, []any{namespace, version}},
		{`INSERT INTO graph_retrieval_generations(namespace,version,component,generation,state,selected,algorithm,content_digest,created_at) VALUES(?,?, 'fts','fts-v3','selected',1,'fts5',?,?)`, []any{namespace, version, hash, timestamp}},
		{`INSERT INTO graph_retrieval_generations(namespace,version,component,generation,state,selected,algorithm,dimensions,content_digest,created_at) VALUES(?,?, 'vector','vector-v3','selected',1,'vec',4,?,?)`, []any{namespace, version, hash, timestamp}},
	} {
		if _, err = legacy.DB().Exec(statement.query, statement.args...); err != nil {
			_ = legacy.Close()
			t.Fatal(err)
		}
	}
	if err = legacy.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := New(path, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	var gotHash, operation, sourceHash string
	var progress int
	if err = upgraded.DB().QueryRow(`SELECT s.content_hash,t.operation,t.source_hash,t.progress FROM graph_snapshots s JOIN graph_tasks t ON t.namespace=s.namespace AND t.version=s.version WHERE s.namespace=? AND s.version=?`, namespace, version).Scan(&gotHash, &operation, &sourceHash, &progress); err != nil {
		t.Fatal(err)
	}
	if gotHash != hash || sourceHash != hash || operation != "snapshot_build" || progress != 10000 {
		t.Fatalf("graph/4 task migration hash=%q source=%q operation=%q progress=%d", gotHash, sourceHash, operation, progress)
	}
	for _, component := range []string{"fts", "vector"} {
		var selected int
		if err = upgraded.DB().QueryRow(`SELECT count(*) FROM graph_retrieval_generations WHERE namespace=? AND version=? AND component=? AND state='selected' AND selected=1`, namespace, version, component).Scan(&selected); err != nil || selected != 1 {
			t.Fatalf("%s selected rows=%d err=%v", component, selected, err)
		}
	}
	changed := append([]graphMigration(nil), graphMigrations...)
	changed[3].sql += "\n-- edited historical migration"
	if err = runGraphMigrations(upgraded.DB(), changed); err == nil {
		t.Fatal("accepted changed graph/4 checksum")
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
		"graph_task_steps_state_idx":                              "graph_task_steps(task_id,state,component)",
		"graph_rebuild_idempotency_task_idx":                      "graph_rebuild_idempotency(task_id)",
		"graph_index_adjacency_lookup_idx":                        "graph_index_adjacency(namespace,version,generation,direction,node_id,relation_kind,edge_type,edge_id)",
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
	assertQueryPlanUses(t, s, `SELECT edge_id FROM graph_index_adjacency WHERE namespace=? AND version=? AND generation=? AND direction=? AND node_id=? AND relation_kind=? AND edge_type=?`, "graph_index_adjacency_lookup_idx", "namespace", "version", "generation", "outgoing", "node", "explicit", "kind")
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
