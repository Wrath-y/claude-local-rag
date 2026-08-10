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
CREATE TABLE graph_namespaces (namespace TEXT PRIMARY KEY, created_at TEXT NOT NULL);
CREATE TABLE graph_snapshots (
 namespace TEXT NOT NULL, version TEXT NOT NULL, base_version TEXT, schema_version TEXT NOT NULL, content_hash TEXT NOT NULL,
 node_count INTEGER NOT NULL DEFAULT 0, edge_count INTEGER NOT NULL DEFAULT 0, task_id TEXT NOT NULL UNIQUE, status TEXT NOT NULL, query_ready INTEGER NOT NULL DEFAULT 0,
 created_at TEXT NOT NULL, updated_at TEXT NOT NULL, PRIMARY KEY(namespace,version), FOREIGN KEY(namespace) REFERENCES graph_namespaces(namespace) ON DELETE CASCADE);
CREATE TABLE graph_namespace_heads (namespace TEXT PRIMARY KEY, active_version TEXT NOT NULL, FOREIGN KEY(namespace,active_version) REFERENCES graph_snapshots(namespace,version) ON DELETE RESTRICT);
CREATE TABLE graph_snapshot_components (namespace TEXT NOT NULL, version TEXT NOT NULL, component TEXT NOT NULL, state TEXT NOT NULL, generation TEXT, error_json TEXT, warning TEXT, PRIMARY KEY(namespace,version,component), FOREIGN KEY(namespace,version) REFERENCES graph_snapshots(namespace,version) ON DELETE CASCADE);
CREATE TABLE graph_nodes (namespace TEXT NOT NULL, version TEXT NOT NULL, node_id TEXT NOT NULL, node_type TEXT NOT NULL, label TEXT NOT NULL, text TEXT NOT NULL, properties_json TEXT NOT NULL, provenance_json TEXT NOT NULL, PRIMARY KEY(namespace,version,node_id), FOREIGN KEY(namespace,version) REFERENCES graph_snapshots(namespace,version) ON DELETE CASCADE);
CREATE TABLE graph_edges (namespace TEXT NOT NULL, version TEXT NOT NULL, edge_id TEXT NOT NULL, from_node_id TEXT NOT NULL, to_node_id TEXT NOT NULL, edge_type TEXT NOT NULL, relation_kind TEXT NOT NULL, confidence TEXT NOT NULL, properties_json TEXT NOT NULL, provenance_json TEXT NOT NULL, PRIMARY KEY(namespace,version,edge_id), FOREIGN KEY(namespace,version,from_node_id) REFERENCES graph_nodes(namespace,version,node_id) ON DELETE CASCADE, FOREIGN KEY(namespace,version,to_node_id) REFERENCES graph_nodes(namespace,version,node_id) ON DELETE CASCADE);
CREATE TABLE graph_snapshot_staging (namespace TEXT NOT NULL, version TEXT NOT NULL, manifest_json TEXT NOT NULL, PRIMARY KEY(namespace,version), FOREIGN KEY(namespace,version) REFERENCES graph_snapshots(namespace,version) ON DELETE CASCADE);
CREATE TABLE graph_tasks (id TEXT PRIMARY KEY, namespace TEXT NOT NULL, version TEXT NOT NULL, state TEXT NOT NULL, phase TEXT NOT NULL, progress INTEGER NOT NULL DEFAULT 0, error_json TEXT, created_at TEXT NOT NULL, started_at TEXT, finished_at TEXT, FOREIGN KEY(namespace,version) REFERENCES graph_snapshots(namespace,version) ON DELETE CASCADE);
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
