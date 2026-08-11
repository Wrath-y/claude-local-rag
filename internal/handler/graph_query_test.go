package handler

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Wrath-y/local-rag/internal/config"
	"github.com/Wrath-y/local-rag/internal/store"
)

func graphQueryHandler(t *testing.T) *Handler {
	t.Helper()
	st, err := store.New(t.TempDir()+"/rag.db", 4)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	const now = "2026-08-11T00:00:00Z"
	if _, err = st.DB().Exec(`INSERT INTO graph_namespaces(namespace,created_at) VALUES('project',?)`, now); err != nil {
		t.Fatal(err)
	}
	if _, err = st.DB().Exec(`INSERT INTO graph_snapshots(namespace,version,schema_version,content_hash,node_count,edge_count,task_id,status,query_ready,created_at,updated_at) VALUES('project','candidate','1.0',?,3,2,'task','ready',1,?,?)`, strings.Repeat("a", 64), now, now); err != nil {
		t.Fatal(err)
	}
	for _, node := range []struct{ id, typ string }{{"a", "service"}, {"b", "database"}, {"c", "queue"}} {
		if _, err = st.DB().Exec(`INSERT INTO graph_nodes(namespace,version,node_id,node_type,label,text,properties_json,provenance_json) VALUES('project','candidate',?,?,?,?,?,?)`, node.id, node.typ, node.id, node.id, `{}`, `{}`); err != nil {
			t.Fatal(err)
		}
	}
	for _, edge := range []struct{ id, from, to, kind string }{{"a-b", "a", "b", "explicit"}, {"b-c", "b", "c", "inferred"}} {
		if _, err = st.DB().Exec(`INSERT INTO graph_edges(namespace,version,edge_id,from_node_id,to_node_id,edge_type,relation_kind,confidence,properties_json,provenance_json) VALUES('project','candidate',?,?,?,?,?,?,?,?)`, edge.id, edge.from, edge.to, "depends_on", edge.kind, "0.8", `{}`, `{}`); err != nil {
			t.Fatal(err)
		}
	}
	return New(Deps{Config: &config.Config{}, Store: st})
}

func graphQueryRouter(h *Handler) *gin.Engine {
	r := gin.New()
	r.Use(GraphRequestID())
	r.POST("/v1/graphs/:namespace/traverse", h.TraverseGraph)
	r.POST("/v1/graphs/:namespace/paths", h.PathsGraph)
	return r
}

func TestGraphQueryHandlersUseStrictVersionedContract(t *testing.T) {
	r := graphQueryRouter(graphQueryHandler(t))
	req := httptest.NewRequest(http.MethodPost, "/v1/graphs/project/traverse", bytes.NewBufferString(`{"snapshot_version":"candidate","start_node_ids":["a"]}`))
	req.Header.Set(graphRequestIDHeader, "query-request-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"resolved_snapshot_version":"candidate"`) || !strings.Contains(w.Body.String(), `"content_hash":"`+strings.Repeat("a", 64)+`"`) {
		t.Fatalf("response=%d %s", w.Code, w.Body.String())
	}
	if w.Header().Get(graphRequestIDHeader) != "query-request-1" {
		t.Fatalf("request id=%q", w.Header().Get(graphRequestIDHeader))
	}

	for _, body := range []string{`{"snapshot_version":"candidate","start_node_ids":["a"],"unknown":true}`, `{"snapshot_version":"candidate","start_node_ids":["a"],"start_node_ids":["b"]}`, `{"snapshot_version":"candidate","start_node_ids":["missing"]}`} {
		w = httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/graphs/project/traverse", bytes.NewBufferString(body)))
		if body == `{"snapshot_version":"candidate","start_node_ids":["missing"]}` {
			if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), `"code":"NODE_NOT_FOUND"`) {
				t.Fatalf("missing node=%d %s", w.Code, w.Body.String())
			}
		} else if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `"code":"INVALID_GRAPH_QUERY"`) {
			t.Fatalf("strict body=%d %s", w.Code, w.Body.String())
		}
	}
}

func TestGraphPathsHandlerDefaultsToExplicitRelationships(t *testing.T) {
	r := graphQueryRouter(graphQueryHandler(t))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/graphs/project/paths", bytes.NewBufferString(`{"snapshot_version":"candidate","source_node_ids":["a"],"target_node_ids":["c"]}`)))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"paths":[]`) {
		t.Fatalf("default path response=%d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/graphs/project/paths", bytes.NewBufferString(`{"snapshot_version":"candidate","source_node_ids":["a"],"target_node_ids":["c"],"relationship_kinds":["explicit","inferred"]}`)))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"edge_ids":["a-b","b-c"]`) {
		t.Fatalf("opt in response=%d %s", w.Code, w.Body.String())
	}
}
