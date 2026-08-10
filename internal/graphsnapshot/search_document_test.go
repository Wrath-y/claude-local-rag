package graphsnapshot

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSearchDocumentIsDeterministic(t *testing.T) {
	node := testNode("n")
	node.Properties = json.RawMessage(`{"b":2,"a":1}`)
	first, err := SearchDocument(&node, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := SearchDocument(&node, nil)
	if err != nil || first != second || !strings.Contains(first, `properties={"a":1,"b":2}`) {
		t.Fatalf("documents=%q %q error=%v", first, second, err)
	}
}
