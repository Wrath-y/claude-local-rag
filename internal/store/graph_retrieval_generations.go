package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"
)

type graphRetrievalGeneration struct {
	Component  string
	Generation string
	Algorithm  string
	Provider   string
	Model      string
	Dimensions *int
	Tokenizer  string
	Digest     string
}

// upsertSelectedGraphRetrievalGenerationForSnapshot records only derived-index
// metadata. The explicit snapshot identity prevents unscoped generation writes.
func upsertSelectedGraphRetrievalGenerationForSnapshot(tx *sql.Tx, namespace, version string, generation graphRetrievalGeneration) error {
	if generation.Component != "fts" && generation.Component != "vector" {
		return fmt.Errorf("unknown graph retrieval component %q", generation.Component)
	}
	if generation.Generation == "" || generation.Algorithm == "" || len(generation.Digest) != 64 {
		return fmt.Errorf("invalid graph retrieval generation metadata")
	}
	_, err := tx.Exec(`
INSERT INTO graph_retrieval_generations(namespace,version,component,generation,state,selected,algorithm,provider,model,dimensions,tokenizer,content_digest,created_at)
VALUES(?,?,?,?,'selected',1,?,?,?,?,?,?,?)
ON CONFLICT(namespace,version,component,generation) DO UPDATE SET
 state=excluded.state,selected=excluded.selected,algorithm=excluded.algorithm,provider=excluded.provider,model=excluded.model,
 dimensions=excluded.dimensions,tokenizer=excluded.tokenizer,content_digest=excluded.content_digest,created_at=excluded.created_at`,
		namespace, version, generation.Component, generation.Generation, generation.Algorithm,
		nullableString(generation.Provider), nullableString(generation.Model), generation.Dimensions, nullableString(generation.Tokenizer), generation.Digest, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func graphDerivedDigest(parts ...string) string {
	hash := sha256.New()
	for _, part := range parts {
		_, _ = hash.Write([]byte(part))
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}
