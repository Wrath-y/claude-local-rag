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
`}, {component: "graph", version: 2, sql: `
CREATE INDEX graph_edges_query_outgoing_idx
ON graph_edges(namespace,version,from_node_id,relation_kind,edge_type,edge_id);
CREATE INDEX graph_edges_query_incoming_idx
ON graph_edges(namespace,version,to_node_id,relation_kind,edge_type,edge_id);
CREATE INDEX graph_nodes_query_type_idx
ON graph_nodes(namespace,version,node_type,node_id);
`}, {component: "graph", version: 3, sql: `
-- Retrieval generations are independent derived data. They never participate
-- in snapshot readiness, so retention can evict them without changing graph
-- traversal or immutable source records.
CREATE TABLE graph_retrieval_generations (
 namespace TEXT NOT NULL,
 version TEXT NOT NULL,
 component TEXT NOT NULL CHECK(component IN ('fts','vector')),
 generation TEXT NOT NULL CHECK(length(generation) > 0),
 state TEXT NOT NULL CHECK(state IN ('selected','private','evicted')),
 selected INTEGER NOT NULL CHECK(selected IN (0,1)),
 algorithm TEXT NOT NULL CHECK(length(algorithm) > 0),
 provider TEXT,
 model TEXT,
 dimensions INTEGER,
 tokenizer TEXT,
 content_digest TEXT NOT NULL CHECK(length(content_digest) = 64 AND content_digest NOT GLOB '*[^0-9a-f]*'),
 created_at TEXT NOT NULL CHECK(length(created_at) > 0),
 PRIMARY KEY(namespace,version,component,generation),
 CHECK((state='selected' AND selected=1) OR (state IN ('private','evicted') AND selected=0)),
 FOREIGN KEY(namespace,version) REFERENCES graph_snapshots(namespace,version) ON DELETE CASCADE
);
CREATE UNIQUE INDEX graph_retrieval_generations_one_selected_idx
ON graph_retrieval_generations(namespace,version,component) WHERE selected=1;
CREATE INDEX graph_retrieval_generations_lookup_idx
ON graph_retrieval_generations(namespace,version,component,state,generation);

-- FTS used to be keyed only by snapshot. Recreate the external-content table
-- so a private/rebuilt generation can coexist with the selected one.
DROP TRIGGER graph_search_documents_ai;
DROP TRIGGER graph_search_documents_ad;
DROP TRIGGER graph_search_documents_au;
DROP TABLE graph_search_fts;
ALTER TABLE graph_search_documents RENAME TO graph_search_documents_v2;
CREATE TABLE graph_search_documents (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 namespace TEXT NOT NULL,
 version TEXT NOT NULL,
 generation TEXT NOT NULL DEFAULT 'legacy' CHECK(length(generation) > 0),
 entity_kind TEXT NOT NULL CHECK(entity_kind IN ('node','edge')),
 entity_id TEXT NOT NULL CHECK(length(entity_id) > 0),
 search_text TEXT NOT NULL,
 UNIQUE(namespace,version,generation,entity_kind,entity_id),
 FOREIGN KEY(namespace,version) REFERENCES graph_snapshots(namespace,version) ON DELETE CASCADE
);
UPDATE graph_snapshot_components
SET generation='fts-' || (
 SELECT task_id FROM graph_snapshots
 WHERE graph_snapshots.namespace=graph_snapshot_components.namespace
   AND graph_snapshots.version=graph_snapshot_components.version
)
WHERE component='fts' AND state='ready' AND (generation IS NULL OR generation='');
INSERT INTO graph_search_documents(namespace,version,generation,entity_kind,entity_id,search_text)
SELECT legacy.namespace,legacy.version,
 COALESCE(NULLIF(component.generation,''),'legacy'),
 legacy.entity_kind,legacy.entity_id,legacy.search_text
FROM graph_search_documents_v2 AS legacy
LEFT JOIN graph_snapshot_components AS component
 ON component.namespace=legacy.namespace AND component.version=legacy.version AND component.component='fts';
DROP TABLE graph_search_documents_v2;
CREATE VIRTUAL TABLE graph_search_fts USING fts5(
 search_text,
 content='graph_search_documents',
 content_rowid='id',
 tokenize='unicode61'
);
INSERT INTO graph_search_fts(rowid,search_text)
SELECT id,search_text FROM graph_search_documents;
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
CREATE INDEX graph_search_documents_generation_lookup_idx
ON graph_search_documents(namespace,version,generation,entity_kind,entity_id);
CREATE INDEX graph_search_documents_namespace_version_id_idx
ON graph_search_documents(namespace,version,id);

-- graph/1 did not retain all runtime identity information. The lifecycle
-- source hash is a safe immutable digest for migrated rows; newly built
-- generations replace it with their precise derived-data digest.
INSERT INTO graph_retrieval_generations(namespace,version,component,generation,state,selected,algorithm,provider,model,dimensions,tokenizer,content_digest,created_at)
SELECT snapshot.namespace,snapshot.version,'fts',component.generation,'selected',1,'fts5',NULL,NULL,NULL,'unicode61',snapshot.content_hash,snapshot.updated_at
FROM graph_snapshots AS snapshot
JOIN graph_snapshot_components AS component
 ON component.namespace=snapshot.namespace AND component.version=snapshot.version
WHERE component.component='fts' AND component.state='ready' AND component.generation IS NOT NULL AND component.generation<>'';
INSERT INTO graph_retrieval_generations(namespace,version,component,generation,state,selected,algorithm,provider,model,dimensions,tokenizer,content_digest,created_at)
SELECT snapshot.namespace,snapshot.version,'vector',component.generation,'selected',1,'sqlite-vec',NULL,NULL,
 COALESCE((SELECT MAX(item.dimensions) FROM graph_vector_items AS item WHERE item.namespace=snapshot.namespace AND item.version=snapshot.version AND item.generation=component.generation),0),
 NULL,snapshot.content_hash,snapshot.updated_at
FROM graph_snapshots AS snapshot
JOIN graph_snapshot_components AS component
 ON component.namespace=snapshot.namespace AND component.version=snapshot.version
WHERE component.component='vector' AND component.state='ready' AND component.generation IS NOT NULL AND component.generation<>'';
`}, {component: "graph", version: 4, sql: `
-- graph/4 adds operability state without changing immutable snapshot facts.
-- Rebuild tasks need more than one task per snapshot, so preserve every
-- graph/1 task while replacing the original one-task-per-snapshot table.
DROP TRIGGER graph_task_snapshot_match;
DROP TRIGGER graph_task_identity_immutable;
DROP INDEX graph_tasks_state_created_id_idx;
ALTER TABLE graph_tasks RENAME TO graph_tasks_v3;
CREATE TABLE graph_tasks (
 id TEXT PRIMARY KEY CHECK(length(id) > 0),
 namespace TEXT NOT NULL,
 version TEXT NOT NULL,
 operation TEXT NOT NULL DEFAULT 'snapshot_build' CHECK(operation IN ('snapshot_build','snapshot_rebuild')),
 requested_components_json TEXT NOT NULL DEFAULT '[]' CHECK(json_valid(requested_components_json) AND json_type(requested_components_json)='array'),
 source_hash TEXT,
 submission_request_id TEXT,
 state TEXT NOT NULL CHECK(state IN ('queued','running','succeeded','failed')),
 phase TEXT NOT NULL CHECK(length(phase) > 0),
 progress INTEGER NOT NULL DEFAULT 0 CHECK(progress BETWEEN 0 AND 10000),
 warnings_json TEXT NOT NULL DEFAULT '[]' CHECK(json_valid(warnings_json) AND json_type(warnings_json)='array'),
 error_json TEXT,
 result_json TEXT,
 created_at TEXT NOT NULL CHECK(length(created_at) > 0),
 started_at TEXT,
 finished_at TEXT,
 updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP CHECK(length(updated_at) > 0),
 CHECK((error_json IS NULL OR (json_valid(error_json) AND json_type(error_json)='object')) AND (result_json IS NULL OR (json_valid(result_json) AND json_type(result_json)='object'))),
 FOREIGN KEY(namespace,version) REFERENCES graph_snapshots(namespace,version) ON DELETE CASCADE
);
INSERT INTO graph_tasks(id,namespace,version,operation,requested_components_json,source_hash,state,phase,progress,warnings_json,error_json,created_at,started_at,finished_at,updated_at)
SELECT task.id,task.namespace,task.version,'snapshot_build','[]',snapshot.content_hash,task.state,task.phase,task.progress*100,'[]',task.error_json,task.created_at,task.started_at,task.finished_at,COALESCE(task.finished_at,task.started_at,task.created_at)
FROM graph_tasks_v3 AS task
JOIN graph_snapshots AS snapshot ON snapshot.namespace=task.namespace AND snapshot.version=task.version;
DROP TABLE graph_tasks_v3;
CREATE INDEX graph_tasks_state_created_id_idx ON graph_tasks(state,created_at,id);
CREATE UNIQUE INDEX graph_tasks_one_snapshot_build_idx ON graph_tasks(namespace,version) WHERE operation='snapshot_build';
CREATE TRIGGER graph_task_snapshot_build_match
BEFORE INSERT ON graph_tasks
WHEN NEW.operation='snapshot_build' AND NEW.id <> (SELECT task_id FROM graph_snapshots WHERE namespace=NEW.namespace AND version=NEW.version)
BEGIN
 SELECT RAISE(ABORT, 'graph snapshot build task must match its snapshot task ID');
END;
CREATE TRIGGER graph_task_identity_immutable
BEFORE UPDATE OF id, namespace, version, operation ON graph_tasks
BEGIN
 SELECT RAISE(ABORT, 'graph task identity is immutable');
END;

CREATE TABLE graph_task_steps (
 task_id TEXT NOT NULL,
 component TEXT NOT NULL CHECK(component IN ('graph_indexes','fts','vector')),
 state TEXT NOT NULL CHECK(state IN ('pending','building','validated','selected','discarded','failed')),
 generation TEXT,
 content_digest TEXT,
 warning_json TEXT,
 error_json TEXT,
 updated_at TEXT NOT NULL CHECK(length(updated_at)>0),
 PRIMARY KEY(task_id,component),
 CHECK((warning_json IS NULL OR (json_valid(warning_json) AND json_type(warning_json)='array')) AND (error_json IS NULL OR (json_valid(error_json) AND json_type(error_json)='object'))),
 FOREIGN KEY(task_id) REFERENCES graph_tasks(id) ON DELETE CASCADE
);
CREATE INDEX graph_task_steps_state_idx ON graph_task_steps(task_id,state,component);

CREATE TABLE graph_rebuild_idempotency (
 namespace TEXT NOT NULL,
 version TEXT NOT NULL,
 operation TEXT NOT NULL CHECK(operation='snapshot_rebuild'),
 idempotency_key TEXT NOT NULL CHECK(length(idempotency_key)>0),
 request_fingerprint TEXT NOT NULL CHECK(length(request_fingerprint)=64 AND request_fingerprint NOT GLOB '*[^0-9a-f]*'),
 source_hash TEXT NOT NULL CHECK(length(source_hash)=64 AND source_hash NOT GLOB '*[^0-9a-f]*'),
 task_id TEXT NOT NULL UNIQUE,
 created_at TEXT NOT NULL CHECK(length(created_at)>0),
 PRIMARY KEY(namespace,version,operation,idempotency_key),
 FOREIGN KEY(namespace,version) REFERENCES graph_snapshots(namespace,version) ON DELETE CASCADE,
 FOREIGN KEY(task_id) REFERENCES graph_tasks(id) ON DELETE CASCADE
);
CREATE INDEX graph_rebuild_idempotency_task_idx ON graph_rebuild_idempotency(task_id);

-- graph/3's catalog is the existing selected-generation seam. Recreate it
-- only to widen the bounded component enum for graph_indexes.
DROP INDEX graph_retrieval_generations_one_selected_idx;
DROP INDEX graph_retrieval_generations_lookup_idx;
ALTER TABLE graph_retrieval_generations RENAME TO graph_retrieval_generations_v3;
CREATE TABLE graph_retrieval_generations (
 namespace TEXT NOT NULL,
 version TEXT NOT NULL,
 component TEXT NOT NULL CHECK(component IN ('graph_indexes','fts','vector')),
 generation TEXT NOT NULL CHECK(length(generation)>0),
 state TEXT NOT NULL CHECK(state IN ('selected','private','validated','evicted','discarded')),
 selected INTEGER NOT NULL CHECK(selected IN (0,1)),
 algorithm TEXT NOT NULL CHECK(length(algorithm)>0),
 provider TEXT,
 model TEXT,
 dimensions INTEGER,
 tokenizer TEXT,
 content_digest TEXT NOT NULL CHECK(length(content_digest)=64 AND content_digest NOT GLOB '*[^0-9a-f]*'),
 created_at TEXT NOT NULL CHECK(length(created_at)>0),
 PRIMARY KEY(namespace,version,component,generation),
 CHECK((state='selected' AND selected=1) OR (state IN ('private','validated','evicted','discarded') AND selected=0)),
 FOREIGN KEY(namespace,version) REFERENCES graph_snapshots(namespace,version) ON DELETE CASCADE
);
INSERT INTO graph_retrieval_generations(namespace,version,component,generation,state,selected,algorithm,provider,model,dimensions,tokenizer,content_digest,created_at)
SELECT namespace,version,component,generation,state,selected,algorithm,provider,model,dimensions,tokenizer,content_digest,created_at
FROM graph_retrieval_generations_v3;
DROP TABLE graph_retrieval_generations_v3;
CREATE UNIQUE INDEX graph_retrieval_generations_one_selected_idx ON graph_retrieval_generations(namespace,version,component) WHERE selected=1;
CREATE INDEX graph_retrieval_generations_lookup_idx ON graph_retrieval_generations(namespace,version,component,state,generation);

CREATE TABLE graph_index_adjacency (
 namespace TEXT NOT NULL,
 version TEXT NOT NULL,
 component TEXT NOT NULL DEFAULT 'graph_indexes' CHECK(component='graph_indexes'),
 generation TEXT NOT NULL CHECK(length(generation)>0),
 direction TEXT NOT NULL CHECK(direction IN ('outgoing','incoming')),
 node_id TEXT NOT NULL CHECK(length(node_id)>0),
 edge_id TEXT NOT NULL CHECK(length(edge_id)>0),
 relation_kind TEXT NOT NULL CHECK(relation_kind IN ('explicit','inferred')),
 edge_type TEXT NOT NULL CHECK(length(edge_type)>0),
 PRIMARY KEY(namespace,version,generation,direction,node_id,edge_id),
 FOREIGN KEY(namespace,version) REFERENCES graph_snapshots(namespace,version) ON DELETE CASCADE,
 FOREIGN KEY(namespace,version,component,generation) REFERENCES graph_retrieval_generations(namespace,version,component,generation) ON DELETE CASCADE
);
CREATE INDEX graph_index_adjacency_lookup_idx ON graph_index_adjacency(namespace,version,generation,direction,node_id,relation_kind,edge_type,edge_id);
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
