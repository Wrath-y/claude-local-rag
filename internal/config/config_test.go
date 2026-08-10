package config

import (
	"os"
	"path/filepath"
	"testing"
)

// writeTemp writes content to a temp file and returns its path.
func writeTemp(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("writeTemp: %v", err)
	}
	return p
}

// TestLoadConfig_Defaults loads a minimal YAML (only server.port set) and
// verifies that applyDefaults fills in all expected zero-value fields.
func TestLoadConfig_Defaults(t *testing.T) {
	p := writeTemp(t, "server:\n  port: 9000\n")

	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// server — explicitly provided, must not be overwritten
	if cfg.Server.Port != 9000 {
		t.Errorf("Server.Port: got %d, want 9000", cfg.Server.Port)
	}

	// embedding defaults
	if cfg.Embedding.Provider != "local" {
		t.Errorf("Embedding.Provider: got %q, want %q", cfg.Embedding.Provider, "local")
	}
	if cfg.Embedding.Model != "BAAI/bge-small-zh-v1.5" {
		t.Errorf("Embedding.Model: got %q", cfg.Embedding.Model)
	}
	if cfg.Embedding.Dims != 512 {
		t.Errorf("Embedding.Dims: got %d, want 512", cfg.Embedding.Dims)
	}
	if cfg.Embedding.DocPrefix != "段落：" {
		t.Errorf("Embedding.DocPrefix: got %q", cfg.Embedding.DocPrefix)
	}
	if cfg.Embedding.QueryPrefix != "查询：" {
		t.Errorf("Embedding.QueryPrefix: got %q", cfg.Embedding.QueryPrefix)
	}

	// rerank defaults
	if cfg.Rerank.Provider != "local" {
		t.Errorf("Rerank.Provider: got %q", cfg.Rerank.Provider)
	}
	if cfg.Rerank.Model != "BAAI/bge-reranker-base" {
		t.Errorf("Rerank.Model: got %q", cfg.Rerank.Model)
	}

	// llm defaults
	if cfg.LLM.Provider != "anthropic" {
		t.Errorf("LLM.Provider: got %q", cfg.LLM.Provider)
	}
	if cfg.LLM.Model != "claude-sonnet-4-6" {
		t.Errorf("LLM.Model: got %q", cfg.LLM.Model)
	}
	if cfg.LLM.APIKeyEnv != "ANTHROPIC_API_KEY" {
		t.Errorf("LLM.APIKeyEnv: got %q", cfg.LLM.APIKeyEnv)
	}
	if cfg.LLM.Timeout != 30 {
		t.Errorf("LLM.Timeout: got %d, want 30", cfg.LLM.Timeout)
	}
	if cfg.LLM.MaxRetries != 2 {
		t.Errorf("LLM.MaxRetries: got %d, want 2", cfg.LLM.MaxRetries)
	}

	// chunk defaults
	if cfg.Chunk.Strategy != "fixed" {
		t.Errorf("Chunk.Strategy: got %q", cfg.Chunk.Strategy)
	}
	if cfg.Chunk.MinTokens != 200 {
		t.Errorf("Chunk.MinTokens: got %d, want 200", cfg.Chunk.MinTokens)
	}
	if cfg.Chunk.MaxTokens != 400 {
		t.Errorf("Chunk.MaxTokens: got %d, want 400", cfg.Chunk.MaxTokens)
	}
	if cfg.Chunk.Semantic.ThresholdPercentile != 90 {
		t.Errorf("Chunk.Semantic.ThresholdPercentile: got %d", cfg.Chunk.Semantic.ThresholdPercentile)
	}
	if cfg.Chunk.Semantic.MinChunkSize != 2 {
		t.Errorf("Chunk.Semantic.MinChunkSize: got %d", cfg.Chunk.Semantic.MinChunkSize)
	}
	if cfg.Chunk.Semantic.MaxChunkSize != 20 {
		t.Errorf("Chunk.Semantic.MaxChunkSize: got %d", cfg.Chunk.Semantic.MaxChunkSize)
	}
	if cfg.Chunk.Agentic.MaxLLMInputTokens != 4000 {
		t.Errorf("Chunk.Agentic.MaxLLMInputTokens: got %d", cfg.Chunk.Agentic.MaxLLMInputTokens)
	}
	if cfg.Chunk.Hierarchical.ParentMaxTokens != 800 {
		t.Errorf("Chunk.Hierarchical.ParentMaxTokens: got %d", cfg.Chunk.Hierarchical.ParentMaxTokens)
	}
	if cfg.Chunk.ContextPrefix.Format != "breadcrumb" {
		t.Errorf("Chunk.ContextPrefix.Format: got %q", cfg.Chunk.ContextPrefix.Format)
	}
	if cfg.Chunk.ContextPrefix.MaxDepth != 3 {
		t.Errorf("Chunk.ContextPrefix.MaxDepth: got %d", cfg.Chunk.ContextPrefix.MaxDepth)
	}

	// retrieve defaults
	if cfg.Retrieve.TopK != 3 {
		t.Errorf("Retrieve.TopK: got %d, want 3", cfg.Retrieve.TopK)
	}
	if cfg.Retrieve.CandidateMultiplier != 10 {
		t.Errorf("Retrieve.CandidateMultiplier: got %d, want 10", cfg.Retrieve.CandidateMultiplier)
	}
	if cfg.Retrieve.RerankCandidates != 9 {
		t.Errorf("Retrieve.RerankCandidates: got %d, want 9", cfg.Retrieve.RerankCandidates)
	}
	if cfg.Retrieve.ContextWindow != 180000 {
		t.Errorf("Retrieve.ContextWindow: got %d, want 180000", cfg.Retrieve.ContextWindow)
	}
	if cfg.Retrieve.ResponseReserve != 8000 {
		t.Errorf("Retrieve.ResponseReserve: got %d, want 8000", cfg.Retrieve.ResponseReserve)
	}
	if cfg.Retrieve.ScoreWeights.Vector != 0.7 {
		t.Errorf("Retrieve.ScoreWeights.Vector: got %v, want 0.7", cfg.Retrieve.ScoreWeights.Vector)
	}
	if cfg.Retrieve.ScoreWeights.BM25 != 0.3 {
		t.Errorf("Retrieve.ScoreWeights.BM25: got %v, want 0.3", cfg.Retrieve.ScoreWeights.BM25)
	}

	// query_rewrite defaults
	if cfg.QueryRewrite.Strategy != "expansion" {
		t.Errorf("QueryRewrite.Strategy: got %q", cfg.QueryRewrite.Strategy)
	}

	// storage defaults
	if cfg.Storage.DBPath != "data/rag.db" {
		t.Errorf("Storage.DBPath: got %q", cfg.Storage.DBPath)
	}
	if cfg.Storage.Backup.Schedule != "0 3 * * *" {
		t.Errorf("Storage.Backup.Schedule: got %q", cfg.Storage.Backup.Schedule)
	}
	if cfg.Storage.Backup.Retention.Days != 7 {
		t.Errorf("Storage.Backup.Retention.Days: got %d", cfg.Storage.Backup.Retention.Days)
	}
	if cfg.Storage.Backup.Retention.Weeks != 4 {
		t.Errorf("Storage.Backup.Retention.Weeks: got %d", cfg.Storage.Backup.Retention.Weeks)
	}

	// sidecar defaults
	if cfg.Sidecar.Port != 8766 {
		t.Errorf("Sidecar.Port: got %d, want 8766", cfg.Sidecar.Port)
	}
	if cfg.Sidecar.HealthInterval != 10 {
		t.Errorf("Sidecar.HealthInterval: got %d, want 10", cfg.Sidecar.HealthInterval)
	}
	if cfg.Sidecar.HealthRetries != 3 {
		t.Errorf("Sidecar.HealthRetries: got %d, want 3", cfg.Sidecar.HealthRetries)
	}
	if cfg.Sidecar.StartupTimeout != 30 {
		t.Errorf("Sidecar.StartupTimeout: got %d, want 30", cfg.Sidecar.StartupTimeout)
	}

	// log defaults
	if cfg.Log.Level != "info" {
		t.Errorf("Log.Level: got %q", cfg.Log.Level)
	}
	if cfg.Log.Format != "json" {
		t.Errorf("Log.Format: got %q", cfg.Log.Format)
	}
	if cfg.Log.Lang != "en" {
		t.Errorf("Log.Lang: got %q", cfg.Log.Lang)
	}
}

func TestLoadConfigRejectsInvalidGraphConfig(t *testing.T) {
	p := writeTemp(t, "graph:\n  max_nodes: -1\n")
	if _, err := Load(p); err == nil {
		t.Fatal("Load accepted invalid graph.max_nodes")
	}
	p = writeTemp(t, "graph:\n  search_document_format: unstable\n")
	if _, err := Load(p); err == nil {
		t.Fatal("Load accepted invalid graph.search_document_format")
	}
}

// TestLoadConfig_FullFile loads a complete YAML file and verifies that every
// value is parsed exactly — no field falls back to a default.
func TestLoadConfig_FullFile(t *testing.T) {
	yaml := `
server:
  port: 8765
embedding:
  provider: "local"
  model: "BAAI/bge-small-zh-v1.5"
  dims: 512
  doc_prefix: "段落："
  query_prefix: "查询："
  api_key_env: ""
  base_url: ""
rerank:
  provider: "local"
  model: "BAAI/bge-reranker-base"
  api_key_env: ""
  base_url: ""
llm:
  provider: "anthropic"
  model: "claude-sonnet-4-6"
  api_key_env: "ANTHROPIC_API_KEY"
  timeout: 30
  max_retries: 2
chunk:
  strategy: "fixed"
  min_tokens: 200
  max_tokens: 400
  semantic:
    threshold_percentile: 90
    min_chunk_size: 2
    max_chunk_size: 20
  agentic:
    generate_summary: true
    max_llm_input_tokens: 4000
  hierarchical:
    enabled: false
    parent_max_tokens: 800
  context_prefix:
    enabled: false
    format: "breadcrumb"
    max_depth: 3
retrieve:
  top_k: 3
  candidate_multiplier: 10
  rerank_candidates: 9
  verbose: true
  dynamic_top_k: false
  context_window: 180000
  response_reserve: 8000
  score_weights:
    vector: 0.7
    bm25: 0.3
query_rewrite:
  enabled: false
  strategy: "expansion"
  provider: ""
storage:
  db_path: "data/rag.db"
  backup:
    enabled: true
    schedule: "0 3 * * *"
    retention:
      days: 7
      weeks: 4
sidecar:
  port: 8766
  health_interval: 10
  health_retries: 3
  startup_timeout: 30
log:
  level: "info"
  format: "json"
  lang: "en"
`
	p := writeTemp(t, yaml)

	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// server
	if cfg.Server.Port != 8765 {
		t.Errorf("Server.Port: got %d, want 8765", cfg.Server.Port)
	}

	// embedding
	if cfg.Embedding.Provider != "local" {
		t.Errorf("Embedding.Provider: got %q", cfg.Embedding.Provider)
	}
	if cfg.Embedding.Model != "BAAI/bge-small-zh-v1.5" {
		t.Errorf("Embedding.Model: got %q", cfg.Embedding.Model)
	}
	if cfg.Embedding.Dims != 512 {
		t.Errorf("Embedding.Dims: got %d", cfg.Embedding.Dims)
	}
	if cfg.Embedding.DocPrefix != "段落：" {
		t.Errorf("Embedding.DocPrefix: got %q", cfg.Embedding.DocPrefix)
	}
	if cfg.Embedding.QueryPrefix != "查询：" {
		t.Errorf("Embedding.QueryPrefix: got %q", cfg.Embedding.QueryPrefix)
	}

	// rerank
	if cfg.Rerank.Provider != "local" {
		t.Errorf("Rerank.Provider: got %q", cfg.Rerank.Provider)
	}
	if cfg.Rerank.Model != "BAAI/bge-reranker-base" {
		t.Errorf("Rerank.Model: got %q", cfg.Rerank.Model)
	}

	// llm
	if cfg.LLM.Provider != "anthropic" {
		t.Errorf("LLM.Provider: got %q", cfg.LLM.Provider)
	}
	if cfg.LLM.Model != "claude-sonnet-4-6" {
		t.Errorf("LLM.Model: got %q", cfg.LLM.Model)
	}
	if cfg.LLM.APIKeyEnv != "ANTHROPIC_API_KEY" {
		t.Errorf("LLM.APIKeyEnv: got %q", cfg.LLM.APIKeyEnv)
	}
	if cfg.LLM.Timeout != 30 {
		t.Errorf("LLM.Timeout: got %d", cfg.LLM.Timeout)
	}
	if cfg.LLM.MaxRetries != 2 {
		t.Errorf("LLM.MaxRetries: got %d", cfg.LLM.MaxRetries)
	}

	// chunk
	if cfg.Chunk.Strategy != "fixed" {
		t.Errorf("Chunk.Strategy: got %q", cfg.Chunk.Strategy)
	}
	if cfg.Chunk.MinTokens != 200 {
		t.Errorf("Chunk.MinTokens: got %d", cfg.Chunk.MinTokens)
	}
	if cfg.Chunk.MaxTokens != 400 {
		t.Errorf("Chunk.MaxTokens: got %d", cfg.Chunk.MaxTokens)
	}
	if cfg.Chunk.Semantic.ThresholdPercentile != 90 {
		t.Errorf("Chunk.Semantic.ThresholdPercentile: got %d", cfg.Chunk.Semantic.ThresholdPercentile)
	}
	if cfg.Chunk.Semantic.MinChunkSize != 2 {
		t.Errorf("Chunk.Semantic.MinChunkSize: got %d", cfg.Chunk.Semantic.MinChunkSize)
	}
	if cfg.Chunk.Semantic.MaxChunkSize != 20 {
		t.Errorf("Chunk.Semantic.MaxChunkSize: got %d", cfg.Chunk.Semantic.MaxChunkSize)
	}
	if !cfg.Chunk.Agentic.GenerateSummary {
		t.Errorf("Chunk.Agentic.GenerateSummary: got false, want true")
	}
	if cfg.Chunk.Agentic.MaxLLMInputTokens != 4000 {
		t.Errorf("Chunk.Agentic.MaxLLMInputTokens: got %d", cfg.Chunk.Agentic.MaxLLMInputTokens)
	}
	if cfg.Chunk.Hierarchical.Enabled {
		t.Errorf("Chunk.Hierarchical.Enabled: got true, want false")
	}
	if cfg.Chunk.Hierarchical.ParentMaxTokens != 800 {
		t.Errorf("Chunk.Hierarchical.ParentMaxTokens: got %d", cfg.Chunk.Hierarchical.ParentMaxTokens)
	}
	if cfg.Chunk.ContextPrefix.Enabled {
		t.Errorf("Chunk.ContextPrefix.Enabled: got true, want false")
	}
	if cfg.Chunk.ContextPrefix.Format != "breadcrumb" {
		t.Errorf("Chunk.ContextPrefix.Format: got %q", cfg.Chunk.ContextPrefix.Format)
	}
	if cfg.Chunk.ContextPrefix.MaxDepth != 3 {
		t.Errorf("Chunk.ContextPrefix.MaxDepth: got %d", cfg.Chunk.ContextPrefix.MaxDepth)
	}

	// retrieve
	if cfg.Retrieve.TopK != 3 {
		t.Errorf("Retrieve.TopK: got %d", cfg.Retrieve.TopK)
	}
	if cfg.Retrieve.CandidateMultiplier != 10 {
		t.Errorf("Retrieve.CandidateMultiplier: got %d", cfg.Retrieve.CandidateMultiplier)
	}
	if cfg.Retrieve.RerankCandidates != 9 {
		t.Errorf("Retrieve.RerankCandidates: got %d", cfg.Retrieve.RerankCandidates)
	}
	if !cfg.Retrieve.Verbose {
		t.Errorf("Retrieve.Verbose: got false, want true")
	}
	if cfg.Retrieve.DynamicTopK {
		t.Errorf("Retrieve.DynamicTopK: got true, want false")
	}
	if cfg.Retrieve.ContextWindow != 180000 {
		t.Errorf("Retrieve.ContextWindow: got %d", cfg.Retrieve.ContextWindow)
	}
	if cfg.Retrieve.ResponseReserve != 8000 {
		t.Errorf("Retrieve.ResponseReserve: got %d", cfg.Retrieve.ResponseReserve)
	}
	if cfg.Retrieve.ScoreWeights.Vector != 0.7 {
		t.Errorf("Retrieve.ScoreWeights.Vector: got %v", cfg.Retrieve.ScoreWeights.Vector)
	}
	if cfg.Retrieve.ScoreWeights.BM25 != 0.3 {
		t.Errorf("Retrieve.ScoreWeights.BM25: got %v", cfg.Retrieve.ScoreWeights.BM25)
	}

	// query_rewrite
	if cfg.QueryRewrite.Enabled {
		t.Errorf("QueryRewrite.Enabled: got true, want false")
	}
	if cfg.QueryRewrite.Strategy != "expansion" {
		t.Errorf("QueryRewrite.Strategy: got %q", cfg.QueryRewrite.Strategy)
	}

	// storage
	if cfg.Storage.DBPath != "data/rag.db" {
		t.Errorf("Storage.DBPath: got %q", cfg.Storage.DBPath)
	}
	if !cfg.Storage.Backup.Enabled {
		t.Errorf("Storage.Backup.Enabled: got false, want true")
	}
	if cfg.Storage.Backup.Schedule != "0 3 * * *" {
		t.Errorf("Storage.Backup.Schedule: got %q", cfg.Storage.Backup.Schedule)
	}
	if cfg.Storage.Backup.Retention.Days != 7 {
		t.Errorf("Storage.Backup.Retention.Days: got %d", cfg.Storage.Backup.Retention.Days)
	}
	if cfg.Storage.Backup.Retention.Weeks != 4 {
		t.Errorf("Storage.Backup.Retention.Weeks: got %d", cfg.Storage.Backup.Retention.Weeks)
	}

	// sidecar
	if cfg.Sidecar.Port != 8766 {
		t.Errorf("Sidecar.Port: got %d", cfg.Sidecar.Port)
	}
	if cfg.Sidecar.HealthInterval != 10 {
		t.Errorf("Sidecar.HealthInterval: got %d", cfg.Sidecar.HealthInterval)
	}
	if cfg.Sidecar.HealthRetries != 3 {
		t.Errorf("Sidecar.HealthRetries: got %d", cfg.Sidecar.HealthRetries)
	}
	if cfg.Sidecar.StartupTimeout != 30 {
		t.Errorf("Sidecar.StartupTimeout: got %d", cfg.Sidecar.StartupTimeout)
	}

	// log
	if cfg.Log.Level != "info" {
		t.Errorf("Log.Level: got %q", cfg.Log.Level)
	}
	if cfg.Log.Format != "json" {
		t.Errorf("Log.Format: got %q", cfg.Log.Format)
	}
	if cfg.Log.Lang != "en" {
		t.Errorf("Log.Lang: got %q", cfg.Log.Lang)
	}
}

// TestLoad_MissingFile verifies that Load returns an error for a non-existent path.
func TestLoad_MissingFile(t *testing.T) {
	_, err := Load("/nonexistent/path/config.yaml")
	if err == nil {
		t.Error("expected error for missing file, got nil")
	}
}

// TestLoad_InvalidYAML verifies that Load returns an error for malformed YAML.
func TestLoad_InvalidYAML(t *testing.T) {
	p := writeTemp(t, "server: [unclosed")
	_, err := Load(p)
	if err == nil {
		t.Error("expected error for invalid YAML, got nil")
	}
}
