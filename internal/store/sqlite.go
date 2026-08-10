package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
	_ "modernc.org/sqlite/vec"
)

// Store wraps a SQLite database with the RAG schema.
type Store struct {
	db          *sql.DB
	dims        int
	feedbackKey []byte
}

// New opens (or creates) a SQLite database at dbPath, applies pragmas, and
// ensures the chunks / vec_chunks / FTS5 schema is present.
func New(dbPath string, dims int) (*Store, error) {
	// 1. Create parent directory if needed.
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("store.New: create dir: %w", err)
	}

	// 2. Open the database.
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("store.New: open db: %w", err)
	}

	// 3. Apply pragmas.
	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA cache_size=-20000",
		"PRAGMA foreign_keys=ON",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			db.Close()
			return nil, fmt.Errorf("store.New: exec %q: %w", p, err)
		}
	}

	// 4. Create schema.
	schema := fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS chunks (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    text       TEXT    NOT NULL,
    source     TEXT    NOT NULL,
    md5        TEXT    NOT NULL,
    parent_text TEXT,
    parent_id  TEXT,
    document_title TEXT,
    document_uri TEXT,
    location TEXT,
	connector_metadata TEXT,
    created_at TEXT    NOT NULL
);

CREATE VIRTUAL TABLE IF NOT EXISTS vec_chunks USING vec0(
    chunk_id  INTEGER PRIMARY KEY,
    embedding float[%d]
);

CREATE VIRTUAL TABLE IF NOT EXISTS chunks_fts USING fts5(
    text,
    content='chunks',
    content_rowid='id',
    tokenize='unicode61'
);

CREATE TRIGGER IF NOT EXISTS chunks_ai AFTER INSERT ON chunks BEGIN
    INSERT INTO chunks_fts(rowid, text) VALUES (new.id, new.text);
END;

CREATE TRIGGER IF NOT EXISTS chunks_ad AFTER DELETE ON chunks BEGIN
    INSERT INTO chunks_fts(chunks_fts, rowid, text) VALUES ('delete', old.id, old.text);
END;
`, dims)

	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("store.New: create schema: %w", err)
	}
	if err := ensureChunkCitationColumns(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("store.New: migrate citation columns: %w", err)
	}
	if err := ensureSyncSchema(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("store.New: migrate sync schema: %w", err)
	}
	if err := ensureFeedbackSchema(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("store.New: migrate feedback schema: %w", err)
	}
	if err := runGraphMigrations(db, graphMigrations); err != nil {
		db.Close()
		return nil, fmt.Errorf("store.New: initialize graph migration ledger: %w", err)
	}
	key, err := loadOrCreateFeedbackKey(dbPath)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("store.New: initialize feedback key: %w", err)
	}

	return &Store{db: db, dims: dims, feedbackKey: key}, nil
}

// ensureChunkCitationColumns upgrades databases created before provenance was
// stored. These columns deliberately remain nullable for legacy chunks.
func ensureChunkCitationColumns(db *sql.DB) error {
	columns := []struct {
		name string
		ddl  string
	}{
		{"document_title", "ALTER TABLE chunks ADD COLUMN document_title TEXT"},
		{"document_uri", "ALTER TABLE chunks ADD COLUMN document_uri TEXT"},
		{"location", "ALTER TABLE chunks ADD COLUMN location TEXT"},
		{"connector_metadata", "ALTER TABLE chunks ADD COLUMN connector_metadata TEXT"},
	}
	for _, column := range columns {
		var exists bool
		rows, err := db.Query("PRAGMA table_info(chunks)")
		if err != nil {
			return err
		}
		for rows.Next() {
			var cid int
			var name, typ string
			var notNull, pk int
			var defaultValue interface{}
			if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
				rows.Close()
				return err
			}
			if name == column.name {
				exists = true
			}
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if !exists {
			if _, err := db.Exec(column.ddl); err != nil {
				return err
			}
		}
	}
	return nil
}

// Close releases the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// DB exposes the underlying *sql.DB for direct access by other packages.
func (s *Store) DB() *sql.DB {
	return s.db
}

// Snapshot creates a consistent SQLite snapshot at destination.
func (s *Store) Snapshot(destination string) error {
	if _, err := os.Stat(destination); err == nil {
		return fmt.Errorf("snapshot destination already exists: %s", destination)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("snapshot stat destination: %w", err)
	}
	if _, err := s.db.Exec("VACUUM INTO ?", destination); err != nil {
		return fmt.Errorf("snapshot database: %w", err)
	}
	return nil
}
