package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config is the top-level configuration structure.
type Config struct {
	Server       ServerConfig       `yaml:"server"`
	Embedding    EmbeddingConfig    `yaml:"embedding"`
	Rerank       RerankConfig       `yaml:"rerank"`
	LLM          LLMConfig          `yaml:"llm"`
	Chunk        ChunkConfig        `yaml:"chunk"`
	Retrieve     RetrieveConfig     `yaml:"retrieve"`
	QueryRewrite QueryRewriteConfig `yaml:"query_rewrite"`
	Agent        AgentConfig        `yaml:"agent"`
	Storage      StorageConfig      `yaml:"storage"`
	Sidecar      SidecarConfig      `yaml:"sidecar"`
	Sync         SyncConfig         `yaml:"sync"`
	Connectors   ConnectorConfig    `yaml:"connectors"`
	Feedback     FeedbackConfig     `yaml:"feedback"`
	Graph        GraphConfig        `yaml:"graph"`
	Log          LogConfig          `yaml:"log"`
}

// ServerConfig holds HTTP server settings.
type ServerConfig struct {
	Port int `yaml:"port"`
}

// EmbeddingConfig holds embedding model settings.
type EmbeddingConfig struct {
	Provider    string `yaml:"provider"`
	Model       string `yaml:"model"`
	Dims        int    `yaml:"dims"`
	DocPrefix   string `yaml:"doc_prefix"`
	QueryPrefix string `yaml:"query_prefix"`
	APIKeyEnv   string `yaml:"api_key_env"`
	BaseURL     string `yaml:"base_url"`
}

// RerankConfig holds reranker model settings.
type RerankConfig struct {
	Provider  string `yaml:"provider"`
	Model     string `yaml:"model"`
	APIKeyEnv string `yaml:"api_key_env"`
	BaseURL   string `yaml:"base_url"`
}

// LLMConfig holds LLM provider settings.
type LLMConfig struct {
	Provider   string `yaml:"provider"`
	Model      string `yaml:"model"`
	APIKeyEnv  string `yaml:"api_key_env"`
	Timeout    int    `yaml:"timeout"`
	MaxRetries int    `yaml:"max_retries"`
}

// ChunkConfig holds document chunking settings.
type ChunkConfig struct {
	Strategy      string              `yaml:"strategy"`
	MinTokens     int                 `yaml:"min_tokens"`
	MaxTokens     int                 `yaml:"max_tokens"`
	Semantic      SemanticChunkConfig `yaml:"semantic"`
	Agentic       AgenticChunkConfig  `yaml:"agentic"`
	Hierarchical  HierarchicalConfig  `yaml:"hierarchical"`
	ContextPrefix ContextPrefixConfig `yaml:"context_prefix"`
}

// SemanticChunkConfig holds semantic chunking settings.
type SemanticChunkConfig struct {
	ThresholdPercentile int `yaml:"threshold_percentile"`
	MinChunkSize        int `yaml:"min_chunk_size"`
	MaxChunkSize        int `yaml:"max_chunk_size"`
}

// AgenticChunkConfig holds agentic chunking settings.
type AgenticChunkConfig struct {
	GenerateSummary   bool `yaml:"generate_summary"`
	MaxLLMInputTokens int  `yaml:"max_llm_input_tokens"`
}

// HierarchicalConfig holds hierarchical chunking settings.
type HierarchicalConfig struct {
	Enabled         bool `yaml:"enabled"`
	ParentMaxTokens int  `yaml:"parent_max_tokens"`
}

// ContextPrefixConfig holds context prefix (breadcrumb) settings.
type ContextPrefixConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Format   string `yaml:"format"`
	MaxDepth int    `yaml:"max_depth"`
}

// RetrieveConfig holds retrieval settings.
type RetrieveConfig struct {
	TopK                int          `yaml:"top_k"`
	CandidateMultiplier int          `yaml:"candidate_multiplier"`
	RerankCandidates    int          `yaml:"rerank_candidates"`
	Verbose             bool         `yaml:"verbose"`
	DynamicTopK         bool         `yaml:"dynamic_top_k"`
	ContextWindow       int          `yaml:"context_window"`
	ResponseReserve     int          `yaml:"response_reserve"`
	ScoreWeights        ScoreWeights `yaml:"score_weights"`
}

// ScoreWeights holds the blend weights for hybrid search.
type ScoreWeights struct {
	Vector float64 `yaml:"vector"`
	BM25   float64 `yaml:"bm25"`
}

// QueryRewriteConfig holds query-rewriting settings.
type QueryRewriteConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Strategy string `yaml:"strategy"`
	Provider string `yaml:"provider"`
}

// AgentConfig bounds the v1 read-only tool loop. Values are deliberately
// conservative defaults and can be tuned after production latency data.
type AgentConfig struct {
	MaxRounds       int `yaml:"max_rounds"`
	MaxToolCalls    int `yaml:"max_tool_calls"`
	DeadlineSeconds int `yaml:"deadline_seconds"`
	MaxContextBytes int `yaml:"max_context_bytes"`
	MaxResultBytes  int `yaml:"max_result_bytes"`
	MaxTopK         int `yaml:"max_top_k"`
}

// StorageConfig holds persistence settings.
type StorageConfig struct {
	DBPath string       `yaml:"db_path"`
	Backup BackupConfig `yaml:"backup"`
}

// BackupConfig holds backup schedule and retention settings.
type BackupConfig struct {
	Enabled   bool            `yaml:"enabled"`
	Schedule  string          `yaml:"schedule"`
	Retention RetentionConfig `yaml:"retention"`
}

// RetentionConfig holds backup retention policy.
type RetentionConfig struct {
	Days  int `yaml:"days"`
	Weeks int `yaml:"weeks"`
}

// SidecarConfig holds sidecar process settings.
type SidecarConfig struct {
	Port           int `yaml:"port"`
	HealthInterval int `yaml:"health_interval"`
	HealthRetries  int `yaml:"health_retries"`
	StartupTimeout int `yaml:"startup_timeout"`
}

// SyncConfig controls the opt-in, durable incremental source synchronizer.
type SyncConfig struct {
	Enabled               bool `yaml:"enabled"`
	Workers               int  `yaml:"workers"`
	MaxSnapshotBytes      int  `yaml:"max_snapshot_bytes"`
	MaxAttempts           int  `yaml:"max_attempts"`
	TaskRetentionHours    int  `yaml:"task_retention_hours"`
	ReportRetentionHours  int  `yaml:"report_retention_hours"`
	StagingRetentionHours int  `yaml:"staging_retention_hours"`
}

// ConnectorConfig contains conservative ceilings for document and repository
// loaders. Request values may only lower these ceilings.
type ConnectorConfig struct {
	AllowedLocalPaths []string `yaml:"allowed_local_paths"`
	AllowedURLSchemes []string `yaml:"allowed_url_schemes"`
	MaxSourceBytes    int64    `yaml:"max_source_bytes"`
	MaxDocuments      int      `yaml:"max_documents"`
	MaxExtractedBytes int64    `yaml:"max_extracted_bytes"`
	MaxDurationSecs   int      `yaml:"max_duration_seconds"`
	MaxGitFiles       int      `yaml:"max_git_files"`
	MaxGitFileBytes   int64    `yaml:"max_git_file_bytes"`
	MaxGitTotalBytes  int64    `yaml:"max_git_total_bytes"`
	Exclusions        []string `yaml:"exclusions"`
}

// FeedbackConfig controls the local-only retrieval feedback ledger. Query
// excerpts are deliberately opt-in: fingerprints are the default identifier.
type FeedbackConfig struct {
	Enabled                bool `yaml:"enabled"`
	RetentionDays          int  `yaml:"retention_days"`
	StoreQueryExcerpt      bool `yaml:"store_query_excerpt"`
	QueryExcerptMaxChars   int  `yaml:"query_excerpt_max_chars"`
	NoteMaxChars           int  `yaml:"note_max_chars"`
	ReviewNoteMaxChars     int  `yaml:"review_note_max_chars"`
	ExportMaxRecords       int  `yaml:"export_max_records"`
	CandidateConversionMax int  `yaml:"candidate_conversion_max"`
}

// GraphConfig bounds the additive graph lifecycle without changing legacy
// chunk/retrieval defaults. These values are ceilings, not request overrides.
type GraphConfig struct {
	MaxPayloadBytes      int    `yaml:"max_payload_bytes"`
	MaxNodes             int    `yaml:"max_nodes"`
	MaxEdges             int    `yaml:"max_edges"`
	SearchDocumentFormat string `yaml:"search_document_format"`
}

// LogConfig holds logging settings.
type LogConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
	Lang   string `yaml:"lang"`
}

// Load reads a YAML config file from path and returns a populated Config.
// Defaults are applied for any zero-value fields after parsing.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read file %q: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("config: parse YAML %q: %w", path, err)
	}

	applyDefaults(&cfg)
	if err := validate(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func validate(cfg *Config) error {
	if cfg.Sync.Workers < 1 {
		return fmt.Errorf("config: sync.workers must be positive")
	}
	if cfg.Sync.MaxSnapshotBytes < 1 {
		return fmt.Errorf("config: sync.max_snapshot_bytes must be positive")
	}
	if cfg.Sync.MaxAttempts < 1 {
		return fmt.Errorf("config: sync.max_attempts must be positive")
	}
	if cfg.Sync.TaskRetentionHours < 1 || cfg.Sync.ReportRetentionHours < 1 || cfg.Sync.StagingRetentionHours < 1 {
		return fmt.Errorf("config: sync retention hours must be positive")
	}
	if cfg.Connectors.MaxSourceBytes < 1 || cfg.Connectors.MaxDocuments < 1 || cfg.Connectors.MaxExtractedBytes < 1 || cfg.Connectors.MaxDurationSecs < 1 || cfg.Connectors.MaxGitFiles < 1 || cfg.Connectors.MaxGitFileBytes < 1 || cfg.Connectors.MaxGitTotalBytes < 1 {
		return fmt.Errorf("config: connector limits must be positive")
	}
	if cfg.Feedback.RetentionDays < 1 || cfg.Feedback.QueryExcerptMaxChars < 1 || cfg.Feedback.NoteMaxChars < 1 || cfg.Feedback.ReviewNoteMaxChars < 1 || cfg.Feedback.ExportMaxRecords < 1 || cfg.Feedback.CandidateConversionMax < 1 {
		return fmt.Errorf("config: feedback limits and retention_days must be positive")
	}
	if cfg.Graph.MaxPayloadBytes < 1 || cfg.Graph.MaxNodes < 1 || cfg.Graph.MaxEdges < 1 || cfg.Graph.SearchDocumentFormat != "graph-search-v1" {
		return fmt.Errorf("config: graph limits or search_document_format are invalid")
	}
	return nil
}

// applyDefaults fills in sensible defaults for any zero-value fields.
func applyDefaults(cfg *Config) {
	// Server
	if cfg.Server.Port == 0 {
		cfg.Server.Port = 8765
	}

	// Embedding
	if cfg.Embedding.Provider == "" {
		cfg.Embedding.Provider = "local"
	}
	if cfg.Embedding.Model == "" {
		cfg.Embedding.Model = "BAAI/bge-small-zh-v1.5"
	}
	if cfg.Embedding.Dims == 0 {
		cfg.Embedding.Dims = 512
	}
	if cfg.Embedding.DocPrefix == "" {
		cfg.Embedding.DocPrefix = "段落："
	}
	if cfg.Embedding.QueryPrefix == "" {
		cfg.Embedding.QueryPrefix = "查询："
	}

	// Rerank
	if cfg.Rerank.Provider == "" {
		cfg.Rerank.Provider = "local"
	}
	if cfg.Rerank.Model == "" {
		cfg.Rerank.Model = "BAAI/bge-reranker-base"
	}

	// LLM
	if cfg.LLM.Provider == "" {
		cfg.LLM.Provider = "anthropic"
	}
	if cfg.LLM.Model == "" {
		cfg.LLM.Model = "claude-sonnet-4-6"
	}
	if cfg.LLM.APIKeyEnv == "" {
		cfg.LLM.APIKeyEnv = "ANTHROPIC_API_KEY"
	}
	if cfg.LLM.Timeout == 0 {
		cfg.LLM.Timeout = 30
	}
	if cfg.LLM.MaxRetries == 0 {
		cfg.LLM.MaxRetries = 2
	}

	// Chunk
	if cfg.Chunk.Strategy == "" {
		cfg.Chunk.Strategy = "fixed"
	}
	if cfg.Chunk.MinTokens == 0 {
		cfg.Chunk.MinTokens = 200
	}
	if cfg.Chunk.MaxTokens == 0 {
		cfg.Chunk.MaxTokens = 400
	}
	if cfg.Chunk.Semantic.ThresholdPercentile == 0 {
		cfg.Chunk.Semantic.ThresholdPercentile = 90
	}
	if cfg.Chunk.Semantic.MinChunkSize == 0 {
		cfg.Chunk.Semantic.MinChunkSize = 2
	}
	if cfg.Chunk.Semantic.MaxChunkSize == 0 {
		cfg.Chunk.Semantic.MaxChunkSize = 20
	}
	if cfg.Chunk.Agentic.MaxLLMInputTokens == 0 {
		cfg.Chunk.Agentic.MaxLLMInputTokens = 4000
	}
	if cfg.Chunk.Hierarchical.ParentMaxTokens == 0 {
		cfg.Chunk.Hierarchical.ParentMaxTokens = 800
	}
	if cfg.Chunk.ContextPrefix.Format == "" {
		cfg.Chunk.ContextPrefix.Format = "breadcrumb"
	}
	if cfg.Chunk.ContextPrefix.MaxDepth == 0 {
		cfg.Chunk.ContextPrefix.MaxDepth = 3
	}

	// Retrieve
	if cfg.Retrieve.TopK == 0 {
		cfg.Retrieve.TopK = 3
	}
	if cfg.Retrieve.CandidateMultiplier == 0 {
		cfg.Retrieve.CandidateMultiplier = 10
	}
	if cfg.Retrieve.RerankCandidates == 0 {
		cfg.Retrieve.RerankCandidates = 9
	}
	if cfg.Retrieve.ContextWindow == 0 {
		cfg.Retrieve.ContextWindow = 180000
	}
	if cfg.Retrieve.ResponseReserve == 0 {
		cfg.Retrieve.ResponseReserve = 8000
	}
	if cfg.Retrieve.ScoreWeights.Vector == 0 && cfg.Retrieve.ScoreWeights.BM25 == 0 {
		cfg.Retrieve.ScoreWeights.Vector = 0.7
		cfg.Retrieve.ScoreWeights.BM25 = 0.3
	}

	// QueryRewrite
	if cfg.QueryRewrite.Strategy == "" {
		cfg.QueryRewrite.Strategy = "expansion"
	}

	// Agent tool loop
	if cfg.Agent.MaxRounds == 0 {
		cfg.Agent.MaxRounds = 4
	}
	if cfg.Agent.MaxToolCalls == 0 {
		cfg.Agent.MaxToolCalls = 3
	}
	if cfg.Agent.DeadlineSeconds == 0 {
		cfg.Agent.DeadlineSeconds = 20
	}
	if cfg.Agent.MaxContextBytes == 0 {
		cfg.Agent.MaxContextBytes = 24000
	}
	if cfg.Agent.MaxResultBytes == 0 {
		cfg.Agent.MaxResultBytes = 12000
	}
	if cfg.Agent.MaxTopK == 0 {
		cfg.Agent.MaxTopK = 3
	}

	// Storage
	if cfg.Storage.DBPath == "" {
		cfg.Storage.DBPath = "data/rag.db"
	}
	if cfg.Storage.Backup.Schedule == "" {
		cfg.Storage.Backup.Schedule = "0 3 * * *"
	}
	if cfg.Storage.Backup.Retention.Days == 0 {
		cfg.Storage.Backup.Retention.Days = 7
	}
	if cfg.Storage.Backup.Retention.Weeks == 0 {
		cfg.Storage.Backup.Retention.Weeks = 4
	}

	// Sidecar
	if cfg.Sidecar.Port == 0 {
		cfg.Sidecar.Port = 8766
	}
	if cfg.Sidecar.HealthInterval == 0 {
		cfg.Sidecar.HealthInterval = 10
	}
	if cfg.Sidecar.HealthRetries == 0 {
		cfg.Sidecar.HealthRetries = 3
	}
	if cfg.Sidecar.StartupTimeout == 0 {
		cfg.Sidecar.StartupTimeout = 30
	}

	// Incremental source sync stays opt-in for backwards compatibility.
	if cfg.Sync.Workers == 0 {
		cfg.Sync.Workers = 1
	}
	if cfg.Sync.MaxSnapshotBytes == 0 {
		cfg.Sync.MaxSnapshotBytes = 16 << 20
	}
	if cfg.Sync.MaxAttempts == 0 {
		cfg.Sync.MaxAttempts = 3
	}
	if cfg.Sync.TaskRetentionHours == 0 {
		cfg.Sync.TaskRetentionHours = 24 * 7
	}
	if cfg.Sync.ReportRetentionHours == 0 {
		cfg.Sync.ReportRetentionHours = 24 * 30
	}
	if cfg.Sync.StagingRetentionHours == 0 {
		cfg.Sync.StagingRetentionHours = 24
	}

	// Connectors are opt-in at the request surface. Their defaults are bounded
	// and local-only; URL loading still rejects non-public destinations.
	if len(cfg.Connectors.AllowedLocalPaths) == 0 {
		cfg.Connectors.AllowedLocalPaths = []string{"."}
	}
	if len(cfg.Connectors.AllowedURLSchemes) == 0 {
		cfg.Connectors.AllowedURLSchemes = []string{"http", "https"}
	}
	if cfg.Connectors.MaxSourceBytes == 0 {
		cfg.Connectors.MaxSourceBytes = 20 << 20
	}
	if cfg.Connectors.MaxDocuments == 0 {
		cfg.Connectors.MaxDocuments = 500
	}
	if cfg.Connectors.MaxExtractedBytes == 0 {
		cfg.Connectors.MaxExtractedBytes = 50 << 20
	}
	if cfg.Connectors.MaxDurationSecs == 0 {
		cfg.Connectors.MaxDurationSecs = 60
	}
	if cfg.Connectors.MaxGitFiles == 0 {
		cfg.Connectors.MaxGitFiles = 2000
	}
	if cfg.Connectors.MaxGitFileBytes == 0 {
		cfg.Connectors.MaxGitFileBytes = 2 << 20
	}
	if cfg.Connectors.MaxGitTotalBytes == 0 {
		cfg.Connectors.MaxGitTotalBytes = 50 << 20
	}

	// Feedback is local-only and capture is enabled by default once local
	// storage is available. Plaintext query excerpts remain default-deny.
	if !cfg.Feedback.Enabled {
		cfg.Feedback.Enabled = true
	}
	if cfg.Feedback.RetentionDays == 0 {
		cfg.Feedback.RetentionDays = 30
	}
	if cfg.Feedback.QueryExcerptMaxChars == 0 {
		cfg.Feedback.QueryExcerptMaxChars = 256
	}
	if cfg.Feedback.NoteMaxChars == 0 {
		cfg.Feedback.NoteMaxChars = 1000
	}
	if cfg.Feedback.ReviewNoteMaxChars == 0 {
		cfg.Feedback.ReviewNoteMaxChars = 1000
	}
	if cfg.Feedback.ExportMaxRecords == 0 {
		cfg.Feedback.ExportMaxRecords = 10000
	}
	if cfg.Feedback.CandidateConversionMax == 0 {
		cfg.Feedback.CandidateConversionMax = 1000
	}

	if cfg.Graph.MaxPayloadBytes == 0 {
		cfg.Graph.MaxPayloadBytes = 32 << 20
	}
	if cfg.Graph.MaxNodes == 0 {
		cfg.Graph.MaxNodes = 10000
	}
	if cfg.Graph.MaxEdges == 0 {
		cfg.Graph.MaxEdges = 100000
	}
	if cfg.Graph.SearchDocumentFormat == "" {
		cfg.Graph.SearchDocumentFormat = "graph-search-v1"
	}

	// Log
	if cfg.Log.Level == "" {
		cfg.Log.Level = "info"
	}
	if cfg.Log.Format == "" {
		cfg.Log.Format = "json"
	}
	if cfg.Log.Lang == "" {
		cfg.Log.Lang = "en"
	}
}
