package graphsnapshot

import (
	"encoding/json"
	"testing"
)

const testHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func testNode(id string) Node {
	return Node{ID: id, Type: "entity", Label: id, Text: id, Properties: json.RawMessage(`{}`), Provenance: json.RawMessage(`{}`)}
}
func testEdge(id, from, to string) Edge {
	return Edge{ID: id, From: from, To: to, Type: "links", RelationKind: "explicit", Confidence: json.Number("1"), Properties: json.RawMessage(`{}`), Provenance: json.RawMessage(`{}`)}
}

func TestNormalizeFullRejectsDuplicateAndDanglingGraphFacts(t *testing.T) {
	request := Request{SchemaVersion: SchemaVersionV1, Mode: ModeFull, ContentHash: testHash, Nodes: []Node{testNode("a"), testNode("a")}}
	if _, err := NormalizeFull(request); err == nil || err.Code != CodeDuplicateNodeID {
		t.Fatalf("duplicate nodes = %#v", err)
	}
	request.Nodes = []Node{testNode("a")}
	request.Edges = []Edge{testEdge("e", "a", "missing")}
	if _, err := NormalizeFull(request); err == nil || err.Code != CodeDanglingEdge {
		t.Fatalf("dangling edge = %#v", err)
	}
}

func TestApplyDeltaUsesStrictDeletesAndNeverMutatesBase(t *testing.T) {
	base := Manifest{SchemaVersion: SchemaVersionV1, Nodes: []Node{testNode("a"), testNode("b")}, Edges: []Edge{testEdge("edge", "a", "b")}}
	request := Request{SchemaVersion: SchemaVersionV1, Mode: ModeDelta, BaseVersion: "v1", ContentHash: testHash, NodeDeletes: []string{"a"}}
	if _, err := ApplyDelta(base, request); err == nil || err.Code != CodeDanglingEdge {
		t.Fatalf("delete required endpoint = %#v", err)
	}
	if len(base.Nodes) != 2 || base.Nodes[0].ID != "a" {
		t.Fatalf("base mutated: %#v", base)
	}
	request = Request{SchemaVersion: SchemaVersionV1, Mode: ModeDelta, BaseVersion: "v1", ContentHash: testHash, EdgeDeletes: []string{"edge"}, NodeDeletes: []string{"a"}}
	manifest, err := ApplyDelta(base, request)
	if err != nil || len(manifest.Nodes) != 1 || manifest.Nodes[0].ID != "b" || len(manifest.Edges) != 0 {
		t.Fatalf("delta manifest=%#v error=%#v", manifest, err)
	}
}

func TestApplyDeltaRejectsConflictingOperations(t *testing.T) {
	base := Manifest{SchemaVersion: SchemaVersionV1, Nodes: []Node{testNode("a")}}
	request := Request{SchemaVersion: SchemaVersionV1, Mode: ModeDelta, BaseVersion: "v1", ContentHash: testHash, NodeUpserts: []Node{testNode("a")}, NodeDeletes: []string{"a"}}
	if _, err := ApplyDelta(base, request); err == nil || err.Code != CodeInvalidDeltaOperation {
		t.Fatalf("conflict = %#v", err)
	}
}
