-- fixture_version: pre-graph-store/v1
-- This database shape predates schema_migrations and every graph_* table.
CREATE TABLE chunks (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    text TEXT NOT NULL,
    source TEXT NOT NULL,
    md5 TEXT NOT NULL,
    parent_text TEXT,
    parent_id TEXT,
    created_at TEXT NOT NULL
);

INSERT INTO chunks(id,text,source,md5,parent_text,parent_id,created_at)
VALUES(1,'legacy searchable text','legacy-source','legacy-md5',NULL,NULL,'2026-08-10T00:00:00Z');

CREATE VIRTUAL TABLE vec_chunks USING vec0(
    chunk_id INTEGER PRIMARY KEY,
    embedding float[4]
);
INSERT INTO vec_chunks(chunk_id,embedding)
VALUES(1,X'0000803F000000000000000000000000');

CREATE VIRTUAL TABLE chunks_fts USING fts5(
    text,
    content='chunks',
    content_rowid='id',
    tokenize='unicode61'
);
INSERT INTO chunks_fts(rowid,text) VALUES(1,'legacy searchable text');

CREATE TRIGGER chunks_ai AFTER INSERT ON chunks BEGIN
    INSERT INTO chunks_fts(rowid,text) VALUES(new.id,new.text);
END;
CREATE TRIGGER chunks_ad AFTER DELETE ON chunks BEGIN
    INSERT INTO chunks_fts(chunks_fts,rowid,text) VALUES('delete',old.id,old.text);
END;
