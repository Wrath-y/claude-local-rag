package operability

import "testing"

func TestRegistryDocumentOrdersEveryCollectionDeterministically(t *testing.T) {
	document := Registry{Status: HealthDegraded, Capabilities: []Capability{{Name: "vector"}, {Name: "paths"}}, Limits: []Limit{{Name: "z", Value: 1}, {Name: "a", Value: 2}}, Dependencies: []Dependency{{Name: "worker"}, {Name: "sqlite"}}}.Document()
	if document.Status != HealthDegraded || document.Service == "" || document.ServiceVersion == "" || document.Capabilities[0].Name != "paths" || document.Limits[0].Name != "a" || document.Dependencies[0].Name != "sqlite" {
		t.Fatalf("document=%+v", document)
	}
}
