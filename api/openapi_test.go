package api

import (
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestOpenAPIGraphLifecycleDocumentHasRequiredSurface(t *testing.T) {
	data, err := os.ReadFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(data, &document); err != nil {
		t.Fatalf("parse openapi.yaml: %v", err)
	}
	if document["openapi"] != "3.0.3" {
		t.Fatalf("openapi version=%v", document["openapi"])
	}
	paths, ok := document["paths"].(map[string]any)
	if !ok {
		t.Fatal("paths missing")
	}
	for _, path := range []string{"/v1/graphs/{namespace}/snapshots/{version}", "/v1/graphs/{namespace}/snapshots/{version}/activate", "/v1/tasks/{task_id}"} {
		if _, ok := paths[path]; !ok {
			t.Fatalf("path %s missing", path)
		}
	}
	components := document["components"].(map[string]any)
	schemas := components["schemas"].(map[string]any)
	for _, name := range []string{"SnapshotRequest", "Snapshot", "Task", "Component", "GraphError"} {
		if _, ok := schemas[name]; !ok {
			t.Fatalf("schema %s missing", name)
		}
	}
}
