package graphsnapshot

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCanonicalManifestSortsEntitiesAndUsesJCS(t *testing.T) {
	nodes := []Node{
		{ID: "z", Type: "kind", Label: "Zulu", Text: "€", Properties: json.RawMessage(`{"number":1.2300,"nested":{"b":2,"a":1}}`), Provenance: json.RawMessage(`{}`)},
		{ID: "a", Type: "kind", Label: "Alpha", Text: "text", Properties: json.RawMessage(`{}`), Provenance: json.RawMessage(`{}`)},
	}
	edges := []Edge{{ID: "b", From: "a", To: "z", Type: "uses", RelationKind: "explicit", Confidence: json.Number("1.0"), Properties: json.RawMessage(`{}`), Provenance: json.RawMessage(`{}`)}}
	canonical, hash, err := CanonicalHash(nodes, edges)
	if err != nil {
		t.Fatal(err)
	}
	if hash != strings.ToLower(hash) || len(hash) != 64 {
		t.Fatalf("hash = %q", hash)
	}
	got := string(canonical)
	if strings.Index(got, `"id":"a"`) > strings.Index(got, `"id":"z"`) {
		t.Fatalf("nodes not sorted: %s", got)
	}
	if strings.Contains(got, "1.2300") || !strings.Contains(got, "1.23") {
		t.Fatalf("JCS number formatting missing: %s", got)
	}
	if nodes[0].ID != "z" {
		t.Fatal("canonicalization mutated source slice")
	}
}

func TestCanonicalManifestMatchesRFC8785NumberVector(t *testing.T) {
	// This is the RFC 8785 number vector represented inside a valid node
	// property bag, so graph canonicalization exercises the pinned JCS library.
	node := Node{ID: "n", Type: "kind", Label: "label", Text: "text", Properties: json.RawMessage(`{"numbers":[333333333.33333329,1E30,4.50,2e-3,0.000000000000000000000000001]}`), Provenance: json.RawMessage(`{}`)}
	canonical, err := CanonicalManifest([]Node{node}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"333333333.3333333", "1e+30", "4.5", "0.002", "1e-27"} {
		if !strings.Contains(string(canonical), want) {
			t.Fatalf("canonical number %q missing from %s", want, canonical)
		}
	}
}
