package handler

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Wrath-y/local-rag/internal/config"
	"github.com/Wrath-y/local-rag/internal/graphsnapshot"
	"github.com/Wrath-y/local-rag/internal/store"
)

func TestGraphSnapshotProviderFixtureAcceptanceAndReplay(t *testing.T) {
	fixtureDir := filepath.Join("..", "..", "tests", "contract", "fixtures", "graph-snapshot-v1")
	body, err := os.ReadFile(filepath.Join(fixtureDir, "full.json"))
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.New(filepath.Join(t.TempDir(), "rag.db"), 4)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	service := graphsnapshot.NewService(st, func() (string, error) { return "graph-fixture-task", nil })
	h := New(Deps{Config: &config.Config{}, GraphService: service, GraphSnapshotReader: st, GraphTaskReader: st, GraphLifecycle: st})
	r := newGraphSnapshotRouter(h)
	path := "/v1/graphs/11111111-1111-4111-8111-111111111111/snapshots/22222222-2222-4222-8222-222222222222"
	for _, want := range []int{http.StatusAccepted, http.StatusAccepted} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodPut, path, bytes.NewReader(body)))
		if w.Code != want || !bytes.Contains(w.Body.Bytes(), []byte(`"content_hash":"0cf03cc36cd9fd8c0a62b917ad3ecd3ffda8c0e90d90e8e92ce7da78cf21021f"`)) {
			t.Fatalf("response=%d %s", w.Code, w.Body.String())
		}
	}
}
