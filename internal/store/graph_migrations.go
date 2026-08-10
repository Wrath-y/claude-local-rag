package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"
)

type graphMigration struct {
	component string
	version   int
	sql       string
}

var graphMigrations = []graphMigration{{component: "graph", version: 1, sql: `
CREATE TABLE graph_namespaces (
 namespace TEXT PRIMARY KEY CHECK(length(namespace) > 0),
 created_at TEXT NOT NULL CHECK(length(created_at) > 0)
);
CREATE TABLE graph_snapshots (
 namespace TEXT NOT NULL, version TEXT NOT NULL, base_version TEXT, schema_version TEXT NOT NULL CHECK(schema_version = '1.0'), content_hash TEXT NOT NULL CHECK(length(content_hash) = 64 AND content_hash NOT GLOB '*[^0-9a-f]*'),
 node_count INTEGER NOT NULL DEFAULT 0 CHECK(node_count >= 0), edge_count INTEGER NOT NULL DEFAULT 0 CHECK(edge_count >= 0), task_id TEXT NOT NULL UNIQUE CHECK(length(task_id) > 0), status TEXT NOT NULL CHECK(status IN ('building','ready','failed')), query_ready INTEGER NOT NULL DEFAULT 0 CHECK(query_ready IN (0,1)),
 created_at TEXT NOT NULL CHECK(length(created_at) > 0), updated_at TEXT NOT NULL CHECK(length(updated_at) > 0),
 PRIMARY KEY(namespace,version),
 CHECK(length(namespace) > 0 AND length(version) > 0 AND (base_version IS NULL OR (length(base_version) > 0 AND base_version <> version))),
 CHECK((status = 'ready' AND query_ready = 1) OR (status <> 'ready' AND query_ready = 0)),
 FOREIGN KEY(namespace) REFERENCES graph_namespaces(namespace) ON DELETE CASCADE
);
CREATE TABLE graph_namespace_heads (
 namespace TEXT PRIMARY KEY, active_version TEXT NOT NULL,
 FOREIGN KEY(namespace,active_version) REFERENCES graph_snapshots(namespace,version) ON DELETE RESTRICT
);
CREATE TABLE graph_snapshot_components (
 namespace TEXT NOT NULL, version TEXT NOT NULL, component TEXT NOT NULL CHECK(component IN ('graph','fts','vector')), state TEXT NOT NULL CHECK(state IN ('pending','building','ready','failed','unavailable')), generation TEXT, error_json TEXT, warning TEXT,
 PRIMARY KEY(namespace,version,component),
 CHECK(error_json IS NULL OR (json_valid(error_json) AND json_type(error_json) = 'object')),
 FOREIGN KEY(namespace,version) REFERENCES graph_snapshots(namespace,version) ON DELETE CASCADE
);
CREATE TABLE graph_nodes (
 namespace TEXT NOT NULL, version TEXT NOT NULL, node_id TEXT NOT NULL CHECK(length(node_id) > 0), node_type TEXT NOT NULL CHECK(length(node_type) > 0), label TEXT NOT NULL, text TEXT NOT NULL, properties_json TEXT NOT NULL, provenance_json TEXT NOT NULL,
 PRIMARY KEY(namespace,version,node_id),
 CHECK(json_valid(properties_json) AND json_type(properties_json) = 'object' AND json_valid(provenance_json) AND json_type(provenance_json) = 'object'),
 FOREIGN KEY(namespace,version) REFERENCES graph_snapshots(namespace,version) ON DELETE CASCADE
);
CREATE TABLE graph_edges (
 namespace TEXT NOT NULL, version TEXT NOT NULL, edge_id TEXT NOT NULL CHECK(length(edge_id) > 0), from_node_id TEXT NOT NULL CHECK(length(from_node_id) > 0), to_node_id TEXT NOT NULL CHECK(length(to_node_id) > 0), edge_type TEXT NOT NULL CHECK(length(edge_type) > 0), relation_kind TEXT NOT NULL CHECK(relation_kind IN ('explicit','inferred')), confidence TEXT NOT NULL CHECK(length(confidence) > 0), properties_json TEXT NOT NULL, provenance_json TEXT NOT NULL,
 PRIMARY KEY(namespace,version,edge_id),
 CHECK(json_valid(properties_json) AND json_type(properties_json) = 'object' AND json_valid(provenance_json) AND json_type(provenance_json) = 'object'),
 FOREIGN KEY(namespace,version,from_node_id) REFERENCES graph_nodes(namespace,version,node_id) ON DELETE CASCADE,
 FOREIGN KEY(namespace,version,to_node_id) REFERENCES graph_nodes(namespace,version,node_id) ON DELETE CASCADE
);
CREATE TABLE graph_snapshot_staging (
 namespace TEXT NOT NULL, version TEXT NOT NULL, manifest_json TEXT NOT NULL,
 PRIMARY KEY(namespace,version),
 CHECK(json_valid(manifest_json) AND json_type(manifest_json) = 'object'),
 FOREIGN KEY(namespace,version) REFERENCES graph_snapshots(namespace,version) ON DELETE CASCADE
);
CREATE TABLE graph_search_documents (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 namespace TEXT NOT NULL, version TEXT NOT NULL, entity_kind TEXT NOT NULL CHECK(entity_kind IN ('node','edge')), entity_id TEXT NOT NULL CHECK(length(entity_id) > 0), search_text TEXT NOT NULL,
 UNIQUE(namespace,version,entity_kind,entity_id),
 FOREIGN KEY(namespace,version) REFERENCES graph_snapshots(namespace,version) ON DELETE CASCADE
);
CREATE VIRTUAL TABLE graph_search_fts USING fts5(
 search_text,
 content='graph_search_documents',
 content_rowid='id',
 tokenize='unicode61'
);
CREATE TRIGGER graph_search_documents_ai AFTER INSERT ON graph_search_documents BEGIN
 INSERT INTO graph_search_fts(rowid,search_text) VALUES(new.id,new.search_text);
END;
CREATE TRIGGER graph_search_documents_ad AFTER DELETE ON graph_search_documents BEGIN
 INSERT INTO graph_search_fts(graph_search_fts,rowid,search_text) VALUES('delete',old.id,old.search_text);
END;
CREATE TRIGGER graph_search_documents_au AFTER UPDATE OF search_text ON graph_search_documents BEGIN
 INSERT INTO graph_search_fts(graph_search_fts,rowid,search_text) VALUES('delete',old.id,old.search_text);
 INSERT INTO graph_search_fts(rowid,search_text) VALUES(new.id,new.search_text);
END;
CREATE TABLE graph_vector_items (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 namespace TEXT NOT NULL, version TEXT NOT NULL, generation TEXT NOT NULL CHECK(length(generation) > 0), entity_kind TEXT NOT NULL CHECK(entity_kind IN ('node','edge')), entity_id TEXT NOT NULL CHECK(length(entity_id) > 0), dimensions INTEGER NOT NULL CHECK(dimensions > 0),
 UNIQUE(namespace,version,generation,entity_kind,entity_id),
 FOREIGN KEY(namespace,version) REFERENCES graph_snapshots(namespace,version) ON DELETE CASCADE
);
CREATE TABLE graph_tasks (
 id TEXT PRIMARY KEY CHECK(length(id) > 0), namespace TEXT NOT NULL, version TEXT NOT NULL, state TEXT NOT NULL CHECK(state IN ('queued','running','succeeded','failed')), phase TEXT NOT NULL CHECK(length(phase) > 0), progress INTEGER NOT NULL DEFAULT 0 CHECK(progress BETWEEN 0 AND 100), error_json TEXT, created_at TEXT NOT NULL CHECK(length(created_at) > 0), started_at TEXT, finished_at TEXT,
 UNIQUE(namespace,version),
 CHECK(error_json IS NULL OR (json_valid(error_json) AND json_type(error_json) = 'object')),
 FOREIGN KEY(namespace,version) REFERENCES graph_snapshots(namespace,version) ON DELETE CASCADE
);

CREATE INDEX graph_snapshots_namespace_status_version_idx ON graph_snapshots(namespace,status,version);
CREATE INDEX graph_snapshots_namespace_base_version_idx ON graph_snapshots(namespace,base_version,version);
CREATE INDEX graph_tasks_state_created_id_idx ON graph_tasks(state,created_at,id);
CREATE INDEX graph_snapshot_components_namespace_version_state_idx ON graph_snapshot_components(namespace,version,state,component);
CREATE INDEX graph_edges_namespace_version_from_idx ON graph_edges(namespace,version,from_node_id,edge_id);
CREATE INDEX graph_edges_namespace_version_to_idx ON graph_edges(namespace,version,to_node_id,edge_id);
CREATE INDEX graph_search_documents_namespace_version_id_idx ON graph_search_documents(namespace,version,id);
CREATE INDEX graph_vector_items_namespace_version_generation_id_idx ON graph_vector_items(namespace,version,generation,id);

CREATE TRIGGER graph_snapshots_identity_immutable
BEFORE UPDATE OF namespace, version, base_version, schema_version, content_hash, task_id, created_at ON graph_snapshots
BEGIN
 SELECT RAISE(ABORT, 'graph snapshot identity is immutable');
END;

CREATE TRIGGER graph_nodes_immutable
BEFORE UPDATE ON graph_nodes
BEGIN
 SELECT RAISE(ABORT, 'graph node is immutable');
END;

CREATE TRIGGER graph_edges_immutable
BEFORE UPDATE ON graph_edges
BEGIN
 SELECT RAISE(ABORT, 'graph edge is immutable');
END;

CREATE TRIGGER graph_snapshot_staging_immutable
BEFORE UPDATE ON graph_snapshot_staging
BEGIN
 SELECT RAISE(ABORT, 'graph snapshot manifest is immutable');
END;

CREATE TRIGGER graph_vector_items_identity_immutable
BEFORE UPDATE OF namespace, version, generation, entity_kind, entity_id, dimensions ON graph_vector_items
BEGIN
 SELECT RAISE(ABORT, 'graph vector item identity is immutable');
END;

CREATE TRIGGER graph_task_snapshot_match
BEFORE INSERT ON graph_tasks
WHEN NEW.id <> (SELECT task_id FROM graph_snapshots WHERE namespace = NEW.namespace AND version = NEW.version)
BEGIN
 SELECT RAISE(ABORT, 'graph task must match its snapshot task ID');
END;

CREATE TRIGGER graph_task_identity_immutable
BEFORE UPDATE OF id, namespace, version ON graph_tasks
BEGIN
 SELECT RAISE(ABORT, 'graph task snapshot association is immutable');
END;
`}}

// runGraphMigrations owns a ledger separate from legacy schema setup. Each
// migration and its ledger entry commit atomically, and an edited historical
// migration is rejected by checksum on every subsequent open.
func runGraphMigrations(db *sql.DB, migrations []graphMigration) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (component TEXT NOT NULL, version INTEGER NOT NULL, checksum TEXT NOT NULL, applied_at TEXT NOT NULL, PRIMARY KEY(component, version))`); err != nil {
		return err
	}
	for _, migration := range migrations {
		sum := sha256.Sum256([]byte(migration.sql))
		checksum := hex.EncodeToString(sum[:])
		var existing string
		err = tx.QueryRow(`SELECT checksum FROM schema_migrations WHERE component=? AND version=?`, migration.component, migration.version).Scan(&existing)
		if err == nil {
			if existing != checksum {
				return fmt.Errorf("graph migration checksum mismatch: %s/%d", migration.component, migration.version)
			}
			continue
		}
		if err != sql.ErrNoRows {
			return err
		}
		if _, err = tx.Exec(migration.sql); err != nil {
			return err
		}
		if _, err = tx.Exec(`INSERT INTO schema_migrations(component,version,checksum,applied_at) VALUES(?,?,?,?)`, migration.component, migration.version, checksum, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ensureGraphVectorStorage creates the sqlite-vec backing table after the
// checksummed relational migration. The vector dimension is a local runtime
// setting, so it cannot be part of a history checksum; the matching legacy
// vec_chunks table follows the same pattern. Metadata is still fully scoped
// and migrated by graph/1, while this trigger removes virtual rows whenever
// explicit cleanup or a snapshot cascade removes their metadata.
func ensureGraphVectorStorage(db *sql.DB, dims int) error {
	if dims <= 0 {
		return fmt.Errorf("graph vector dimensions must be positive")
	}
	if _, err := db.Exec(fmt.Sprintf(`CREATE VIRTUAL TABLE IF NOT EXISTS graph_vectors USING vec0(item_id INTEGER PRIMARY KEY, embedding float[%d])`, dims)); err != nil {
		return fmt.Errorf("create graph vector storage: %w", err)
	}
	if _, err := db.Exec(`CREATE TRIGGER IF NOT EXISTS graph_vector_items_ad AFTER DELETE ON graph_vector_items BEGIN DELETE FROM graph_vectors WHERE item_id=old.id; END;`); err != nil {
		return fmt.Errorf("create graph vector cleanup trigger: %w", err)
	}
	return nil
}
