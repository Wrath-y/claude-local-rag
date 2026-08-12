package operability

import "testing"

func TestReduceHealthHonorsCorePrecedenceAndDisabledRerank(t *testing.T) {
	base := HealthInputs{SQLite: CapabilityAvailable, Migrations: CapabilityAvailable, CoreGraphQuery: CapabilityAvailable, Worker: CapabilityAvailable, BM25: CapabilityAvailable, Vector: CapabilityAvailable, Rerank: CapabilityAvailable}
	if got := ReduceHealth(base); got != HealthOK {
		t.Fatalf("healthy=%s", got)
	}
	for _, mutate := range []func(*HealthInputs){func(i *HealthInputs) { i.SQLite = CapabilityUnavailable }, func(i *HealthInputs) { i.Migrations = CapabilityUnavailable }, func(i *HealthInputs) { i.CoreGraphQuery = CapabilityUnavailable }} {
		input := base
		mutate(&input)
		if got := ReduceHealth(input); got != HealthUnavailable {
			t.Fatalf("core=%s", got)
		}
	}
	input := base
	input.Vector = CapabilityDegraded
	if got := ReduceHealth(input); got != HealthDegraded {
		t.Fatalf("vector=%s", got)
	}
	input = base
	input.Rerank, input.RerankDisabled = CapabilityDisabled, true
	if got := ReduceHealth(input); got != HealthOK {
		t.Fatalf("disabled rerank=%s", got)
	}
}
