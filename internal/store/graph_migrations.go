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
