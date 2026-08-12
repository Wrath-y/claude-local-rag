package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Wrath-y/local-rag/internal/graphquery"
	"github.com/Wrath-y/local-rag/internal/graphretrieval"
	"github.com/Wrath-y/local-rag/internal/observe"
	"github.com/Wrath-y/local-rag/internal/operability"
)

// Health returns a bounded, side-effect-free compatibility document. It never
// sends synthetic content to a provider: real provider outcomes are surfaced
// by the operability registry in later wiring instead.
func (h *Handler) Health(c *gin.Context) {
	inputs := operability.HealthInputs{SQLite: operability.CapabilityAvailable, Migrations: operability.CapabilityAvailable, CoreGraphQuery: operability.CapabilityAvailable, Worker: operability.CapabilityAvailable, BM25: operability.CapabilityAvailable, Vector: operability.CapabilityAvailable, Rerank: operability.CapabilityDisabled, RerankDisabled: true}
	dependencies := []operability.Dependency{{Name: "sqlite", State: operability.CapabilityAvailable}, {Name: "graph_migrations", State: operability.CapabilityAvailable}, {Name: "graph_worker", State: operability.CapabilityAvailable}, {Name: "core_graph_query", State: operability.CapabilityAvailable}}
	if h.deps.Stores == nil || h.deps.Stores.Store() == nil || h.deps.Stores.Store().GraphUnavailable() != nil {
		inputs.SQLite, inputs.Migrations, inputs.CoreGraphQuery = operability.CapabilityUnavailable, operability.CapabilityUnavailable, operability.CapabilityUnavailable
		dependencies[0].State, dependencies[0].Reason = operability.CapabilityUnavailable, "STORE_UNAVAILABLE"
		dependencies[1].State, dependencies[1].Reason = operability.CapabilityUnavailable, "MIGRATION_UNAVAILABLE"
	}
	if h.graphService == nil {
		inputs.Worker = operability.CapabilityDegraded
		dependencies[2].State, dependencies[2].Reason = operability.CapabilityDegraded, "WORKER_UNAVAILABLE"
	}
	vectorState := operability.CapabilityAvailable
	vectorDependency := operability.Dependency{Name: "vector", State: vectorState}
	if h.vectorHealth != nil {
		vectorDependency = h.vectorHealth.Check(c.Request.Context())
		vectorState = vectorDependency.State
	} else if h.deps.Embedder == nil {
		vectorState = operability.CapabilityDegraded
		vectorDependency.State, vectorDependency.Reason = vectorState, "PROVIDER_UNAVAILABLE"
	}
	inputs.Vector = vectorState
	rerankDependency := operability.Dependency{Name: "rerank", State: operability.CapabilityDisabled}
	if h.rerankHealth != nil {
		rerankDependency = h.rerankHealth.Check(c.Request.Context())
		inputs.Rerank, inputs.RerankDisabled = rerankDependency.State, false
	}
	status := operability.ReduceHealth(inputs)
	dependencies = append(dependencies, operability.Dependency{Name: "bm25", State: operability.CapabilityAvailable}, vectorDependency, rerankDependency)
	graph := graphLimits(h.deps.Config)
	limits := []operability.Limit{{Name: "snapshot_payload_bytes", Value: graph.maxPayloadBytes}, {Name: "snapshot_nodes", Value: graph.maxNodes}, {Name: "snapshot_edges", Value: graph.maxEdges}, {Name: "traverse_max_depth", Value: graphquery.MaxDepth}, {Name: "traverse_max_nodes", Value: graphquery.MaxNodes}, {Name: "paths_max_paths", Value: graphquery.MaxPaths}, {Name: "retrieval_seed_limit", Value: graphretrieval.MaxSeedLimit}, {Name: "retrieval_result_limit", Value: graphretrieval.MaxResultLimit}, {Name: "retrieval_graph_depth", Value: graphretrieval.MaxGraphDepth}, {Name: "rebuild_components", Value: 3}}
	document := operability.Registry{Status: status, Capabilities: []operability.Capability{{Name: "snapshot_lifecycle", State: operability.CapabilityAvailable}, {Name: "traverse", State: operability.CapabilityAvailable}, {Name: "paths", State: operability.CapabilityAvailable}, {Name: "task_polling", State: operability.CapabilityAvailable}, {Name: "rebuild", State: operability.CapabilityDegraded}, {Name: "bm25", State: operability.CapabilityAvailable}, {Name: "vector", State: vectorState}}, Limits: limits, Dependencies: dependencies}.Document()
	code := http.StatusOK
	if status == operability.HealthUnavailable {
		code = http.StatusServiceUnavailable
	}
	observe.GraphHealthState.WithLabelValues(string(status)).Set(1)
	observe.GraphHealthTransition(string(status))
	c.JSON(code, document)
}

// Metrics serves Prometheus metrics in text exposition format.
func (h *Handler) Metrics(c *gin.Context) {
	data := observe.Render()
	c.Data(http.StatusOK, "text/plain; version=0.0.4; charset=utf-8", data)
}

// IntegrityCheck runs SQLite integrity_check and returns the result.
func (h *Handler) IntegrityCheck(c *gin.Context) {
	result, err := h.management.IntegrityCheck()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	status := "ok"
	code := http.StatusOK
	if result != "ok" {
		status = "error"
		code = http.StatusConflict
	}
	c.JSON(code, gin.H{"status": status, "detail": result})
}
