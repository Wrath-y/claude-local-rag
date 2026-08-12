package operability

// HealthInputs is the bounded outcome of dependency probes. Callers provide
// stable reason codes only; raw provider/store errors never enter this DTO.
type HealthInputs struct {
	SQLite         CapabilityState
	Migrations     CapabilityState
	CoreGraphQuery CapabilityState
	Worker         CapabilityState
	BM25           CapabilityState
	Vector         CapabilityState
	Rerank         CapabilityState
	RerankDisabled bool
}

// ReduceHealth centralizes the documented status precedence: core storage and
// query failures are unavailable, while worker/retrieval degradation keeps
// deterministic graph reads usable.
func ReduceHealth(input HealthInputs) HealthStatus {
	for _, state := range []CapabilityState{input.SQLite, input.Migrations, input.CoreGraphQuery} {
		if state != CapabilityAvailable {
			return HealthUnavailable
		}
	}
	for _, state := range []CapabilityState{input.Worker, input.BM25, input.Vector} {
		if state != CapabilityAvailable {
			return HealthDegraded
		}
	}
	if !input.RerankDisabled && input.Rerank != CapabilityAvailable {
		return HealthDegraded
	}
	return HealthOK
}
