package store

import (
	"context"
	"fmt"
	"testing"
)

func TestGraphRetrievalRetentionKeepsActiveUnionNewestTwenty(t *testing.T) {
	s, err := New(t.TempDir()+"/rag.db", 2)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	const namespace = "retention"
	const hash = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	if _, err = s.DB().Exec(`INSERT INTO graph_namespaces(namespace,created_at) VALUES(?,?)`, namespace, "2026-08-10T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 22; index++ {
		version := fmt.Sprintf("v%02d", index)
		taskID := fmt.Sprintf("task-%02d", index)
		timestamp := fmt.Sprintf("2026-08-10T00:%02d:00Z", index)
		for _, statement := range []struct {
			query string
			args  []any
		}{
			{`INSERT INTO graph_snapshots(namespace,version,schema_version,content_hash,task_id,status,query_ready,created_at,updated_at) VALUES(?,?,?,?,?,'ready',1,?,?)`, []any{namespace, version, "1.0", hash, taskID, timestamp, timestamp}},
			{`INSERT INTO graph_retrieval_generations(namespace,version,component,generation,state,selected,algorithm,content_digest,created_at) VALUES(?,?, 'fts',?,'selected',1,'fts5',?,?)`, []any{namespace, version, "fts-" + version, hash, timestamp}},
			{`INSERT INTO graph_retrieval_generations(namespace,version,component,generation,state,selected,algorithm,dimensions,content_digest,created_at) VALUES(?,?, 'vector',?,'selected',1,'vec',2,?,?)`, []any{namespace, version, "vector-" + version, hash, timestamp}},
			{`INSERT INTO graph_search_documents(namespace,version,generation,entity_kind,entity_id,search_text) VALUES(?,?,?,'node','node','text')`, []any{namespace, version, "fts-" + version}},
			{`INSERT INTO graph_vector_items(namespace,version,generation,entity_kind,entity_id,dimensions) VALUES(?,?,?,'node','node',2)`, []any{namespace, version, "vector-" + version}},
		} {
			if _, err = s.DB().Exec(statement.query, statement.args...); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err = s.DB().Exec(`INSERT INTO graph_namespace_heads(namespace,active_version) VALUES(?,?)`, namespace, "v00"); err != nil {
		t.Fatal(err)
	}
	if err = s.ApplyGraphRetrievalRetention(context.Background(), namespace); err != nil {
		t.Fatal(err)
	}
	var selected, evicted, retainedFTS, evictedFTS, snapshots int
	if err = s.DB().QueryRow(`SELECT count(*) FROM graph_retrieval_generations WHERE namespace=? AND selected=1`, namespace).Scan(&selected); err != nil || selected != 42 {
		t.Fatalf("selected=%d err=%v, want 42 (21 versions × 2)", selected, err)
	}
	if err = s.DB().QueryRow(`SELECT count(*) FROM graph_retrieval_generations WHERE namespace=? AND state='evicted'`, namespace).Scan(&evicted); err != nil || evicted != 2 {
		t.Fatalf("evicted=%d err=%v", evicted, err)
	}
	if err = s.DB().QueryRow(`SELECT count(*) FROM graph_search_documents WHERE namespace=?`, namespace).Scan(&retainedFTS); err != nil || retainedFTS != 21 {
		t.Fatalf("retained fts=%d err=%v", retainedFTS, err)
	}
	if err = s.DB().QueryRow(`SELECT count(*) FROM graph_search_documents WHERE namespace=? AND version='v01'`, namespace).Scan(&evictedFTS); err != nil || evictedFTS != 0 {
		t.Fatalf("evicted fts rows=%d err=%v", evictedFTS, err)
	}
	var evictedVectors int
	if err = s.DB().QueryRow(`SELECT count(*) FROM graph_vector_items WHERE namespace=? AND version='v01'`, namespace).Scan(&evictedVectors); err != nil || evictedVectors != 0 {
		t.Fatalf("evicted vector rows=%d err=%v", evictedVectors, err)
	}
	if err = s.DB().QueryRow(`SELECT count(*) FROM graph_snapshots WHERE namespace=?`, namespace).Scan(&snapshots); err != nil || snapshots != 22 {
		t.Fatalf("snapshots=%d err=%v", snapshots, err)
	}
	if err = s.ApplyGraphRetrievalRetention(context.Background(), namespace); err != nil {
		t.Fatalf("repeat retention: %v", err)
	}
}

func TestGraphRetrievalRetentionKeepsFewerAndExactlyTwentyWithTimestampTies(t *testing.T) {
	for _, versions := range []int{3, 20} {
		t.Run(fmt.Sprintf("versions-%d", versions), func(t *testing.T) {
			s, err := New(t.TempDir()+"/rag.db", 2)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			const namespace = "tie-retention"
			const hash = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
			if _, err = s.DB().Exec(`INSERT INTO graph_namespaces(namespace,created_at) VALUES(?,?)`, namespace, "2026-08-10T00:00:00Z"); err != nil {
				t.Fatal(err)
			}
			for index := 0; index < versions; index++ {
				version, taskID := fmt.Sprintf("v%02d", index), fmt.Sprintf("task-tie-%02d", index)
				if _, err = s.DB().Exec(`INSERT INTO graph_snapshots(namespace,version,schema_version,content_hash,task_id,status,query_ready,created_at,updated_at) VALUES(?,?,?,?,?,'ready',1,?,?)`, namespace, version, "1.0", hash, taskID, "2026-08-10T00:00:00Z", "2026-08-10T00:00:00Z"); err != nil {
					t.Fatal(err)
				}
				if _, err = s.DB().Exec(`INSERT INTO graph_retrieval_generations(namespace,version,component,generation,state,selected,algorithm,content_digest,created_at) VALUES(?,?, 'fts',?,'selected',1,'fts5',?,?)`, namespace, version, "fts-"+version, hash, "2026-08-10T00:00:00Z"); err != nil {
					t.Fatal(err)
				}
			}
			if err = s.ApplyGraphRetrievalRetention(context.Background(), namespace); err != nil {
				t.Fatal(err)
			}
			var selected, evicted int
			if err = s.DB().QueryRow(`SELECT count(*) FROM graph_retrieval_generations WHERE namespace=? AND selected=1`, namespace).Scan(&selected); err != nil || selected != versions {
				t.Fatalf("selected=%d err=%v", selected, err)
			}
			if err = s.DB().QueryRow(`SELECT count(*) FROM graph_retrieval_generations WHERE namespace=? AND state='evicted'`, namespace).Scan(&evicted); err != nil || evicted != 0 {
				t.Fatalf("evicted=%d err=%v", evicted, err)
			}
		})
	}
}
