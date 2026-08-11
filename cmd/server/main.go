package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Wrath-y/local-rag/internal/chunk"
	"github.com/Wrath-y/local-rag/internal/config"
	"github.com/Wrath-y/local-rag/internal/graphsnapshot"
	"github.com/Wrath-y/local-rag/internal/handler"
	"github.com/Wrath-y/local-rag/internal/management"
	"github.com/Wrath-y/local-rag/internal/mcpserver"
	"github.com/Wrath-y/local-rag/internal/observe"
	"github.com/Wrath-y/local-rag/internal/provider"
	"github.com/Wrath-y/local-rag/internal/sidecar"
	"github.com/Wrath-y/local-rag/internal/store"
)

func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Accept, X-Request-ID")

		if c.Request.Method == http.MethodOptions {
			c.Status(http.StatusNoContent)
			c.Abort()
			return
		}

		c.Next()
	}
}

func main() {
	cfgPath := "config.yaml"
	if p := os.Getenv("RAG_CONFIG"); p != "" {
		cfgPath = p
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config %q: %v\n", cfgPath, err)
		os.Exit(1)
	}

	// MCP mode: run as MCP server over stdio (no HTTP, no gin)
	if len(os.Args) > 1 && os.Args[1] == "mcp" {
		runMCP(cfg)
		return
	}

	observe.InitLogger(cfg.Log.Level, cfg.Log.Format)
	observe.InitMetrics()

	// Start sidecar (only used when embedding provider is "local").
	sc := sidecar.New(sidecar.Config{
		Provider:       cfg.Embedding.Provider,
		Port:           cfg.Sidecar.Port,
		HealthInterval: time.Duration(cfg.Sidecar.HealthInterval) * time.Second,
		HealthRetries:  cfg.Sidecar.HealthRetries,
		StartupTimeout: time.Duration(cfg.Sidecar.StartupTimeout) * time.Second,
	})
	if err := sc.Start(); err != nil {
		slog.Error("sidecar start failed", "err", err)
		os.Exit(1)
	}
	defer sc.Stop()

	// Init providers.
	embedder, err := provider.NewEmbedProvider(cfg.Embedding, sc.URL())
	if err != nil {
		slog.Error("embed provider init failed", "err", err)
		os.Exit(1)
	}

	reranker, err := provider.NewRerankProvider(cfg.Rerank, sc.URL())
	if err != nil {
		slog.Error("rerank provider init failed", "err", err)
		os.Exit(1)
	}

	// LLM provider is optional — log warning but continue if not configured.
	llm, err := provider.NewLLMProvider(cfg.LLM)
	if err != nil {
		slog.Warn("LLM provider not available (agent and rewrite features disabled)", "err", err)
		llm = nil
	}

	// Init store.
	st, err := store.New(cfg.Storage.DBPath, cfg.Embedding.Dims)
	if err != nil {
		slog.Error("store init failed", "err", err)
		os.Exit(1)
	}
	stores := handler.NewStoreLifecycle(st)
	defer stores.Close()
	graphService := graphsnapshot.NewService(st, nil)
	graphEmbedder := graphsnapshot.WithEmbeddingIdentity(embedder, graphsnapshot.EmbeddingIdentity{
		Algorithm:  graphsnapshot.SearchDocumentFormatV1 + "/embedding",
		Provider:   cfg.Embedding.Provider,
		Model:      cfg.Embedding.Model,
		Dimensions: cfg.Embedding.Dims,
	})
	graphAvailable := true
	if graphErr := st.GraphUnavailable(); graphErr != nil {
		graphAvailable = false
		slog.Error("graph lifecycle unavailable after initialization", "err", graphErr)
	} else if err := graphService.Start(context.Background(), func(ctx context.Context, task graphsnapshot.Task) error {
		return graphService.Dispatch(ctx, task, graphEmbedder)
	}); err != nil {
		graphAvailable = false
		slog.Error("graph lifecycle worker start failed", "err", err)
	}
	defer graphService.Close()

	// Init chunker.
	chunker := chunk.NewChunker(cfg.Chunk, embedder, llm)

	// Build handler. Graph dependencies are supplied only after recovery starts;
	// a graph migration/startup failure leaves every legacy route operational.
	handlerDeps := handler.Deps{
		Config:   cfg,
		Stores:   stores,
		Embedder: embedder,
		Reranker: reranker,
		LLM:      llm,
		Chunker:  chunker,
	}
	if graphAvailable {
		handlerDeps.GraphService = graphService
		handlerDeps.GraphSnapshotReader = st
		handlerDeps.GraphTaskReader = st
		handlerDeps.GraphLifecycle = st
	}
	h := handler.New(handlerDeps)

	// Gin router.
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery(), corsMiddleware())
	v1 := r.Group("/v1")
	v1.Use(handler.GraphRequestID())
	v1.PUT("/graphs/:namespace/snapshots/:version", h.PutGraphSnapshot)
	v1.GET("/graphs/:namespace/snapshots/:version", h.GetGraphSnapshot)
	v1.POST("/graphs/:namespace/snapshots/:version/activate", h.ActivateGraphSnapshot)
	v1.DELETE("/graphs/:namespace/snapshots/:version", h.DeleteGraphSnapshot)
	v1.POST("/graphs/:namespace/traverse", h.TraverseGraph)
	v1.POST("/graphs/:namespace/paths", h.PathsGraph)
	v1.POST("/graphs/:namespace/retrieve", h.RetrieveGraph)
	v1.GET("/tasks/:task_id", h.GetGraphTask)

	// Core routes.
	r.POST("/ingest", h.Ingest)
	r.POST("/sources/:source/syncs", h.SyncSubmit)
	r.GET("/sources/:source/syncs/:task", h.SyncStatus)
	r.GET("/sources/:source/syncs/:task/report", h.SyncReport)
	r.POST("/sources/:source/syncs/:task/retry", h.SyncRetry)
	r.GET("/sources/:source/sync-baseline", h.SyncBaseline)
	r.POST("/retrieve", h.Retrieve)
	r.POST("/citations/validate", h.ValidateCitations)
	r.POST("/hook", h.Hook)
	r.POST("/hook/outcome", h.HookOutcomeReport)
	r.POST("/feedback", h.CreateFeedback)
	r.GET("/feedback", h.ListFeedback)
	r.GET("/feedback/aggregate", h.AggregateFeedback)
	r.GET("/feedback/export", h.ExportFeedback)
	r.POST("/feedback/candidates/convert", h.ConvertCandidates)
	r.GET("/feedback/candidates", h.ListCandidates)
	r.GET("/feedback/candidates/export", h.ExportCandidates)
	r.POST("/feedback/candidates/:id/review", h.ReviewCandidate)

	// Toggle routes.
	r.POST("/rerank/toggle", h.RerankToggle)
	r.POST("/retrieve/verbose", h.VerboseToggle)
	r.POST("/retrieve/dynamic-top-k", h.DynamicTopKToggle)
	r.POST("/retrieve/query-rewrite", h.QueryRewriteToggle)

	// Config routes.
	r.GET("/config/chunk-strategy", h.GetChunkStrategy)
	r.PUT("/config/chunk-strategy", h.SetChunkStrategy)

	// Management routes.
	r.GET("/sources", h.ListSources)
	r.DELETE("/source", h.DeleteSource)
	r.DELETE("/reset", h.Reset)
	r.GET("/stats", h.Stats)
	r.GET("/export", h.Export)
	r.POST("/import", h.Import)

	// Observability routes.
	r.GET("/health", h.Health)
	r.GET("/metrics", h.Metrics)
	r.GET("/storage/integrity-check", h.IntegrityCheck)

	// Backup routes.
	r.POST("/backup/run", h.BackupRun)
	r.GET("/backup/list", h.BackupList)
	r.POST("/backup/restore", h.BackupRestore)

	// Index routes.
	r.POST("/index/rebuild", h.IndexRebuild)
	r.GET("/index/status", h.IndexStatus)

	// Agent routes.
	r.POST("/agent/chat", h.AgentChat)
	r.POST("/agent/permission/:token", h.AgentApprovePermission)
	r.POST("/agent/session", h.AgentCreateSession)
	r.GET("/agent/sessions", h.AgentListSessions)
	r.DELETE("/agent/session/:id", h.AgentDeleteSession)

	// Graceful shutdown.
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		slog.Info("shutting down")
		graphService.Close()
		sc.Stop()
		stores.Close()
		os.Exit(0)
	}()

	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	slog.Info("server starting", "addr", addr)
	if err := r.Run(addr); err != nil {
		slog.Error("server error", "err", err)
		os.Exit(1)
	}
}

// runMCP starts the server in MCP (Model Context Protocol) mode.
// It communicates over stdin/stdout using JSON-RPC, NOT HTTP.
// Other agents (Claude Code, Cursor, etc.) connect to this directly.
func runMCP(cfg *config.Config) {
	// In MCP mode, only log errors to stderr (stdout is the protocol channel).
	observe.InitLogger("error", "text")

	// Start sidecar if needed.
	sc := sidecar.New(sidecar.Config{
		Provider:       cfg.Embedding.Provider,
		Port:           cfg.Sidecar.Port,
		HealthInterval: time.Duration(cfg.Sidecar.HealthInterval) * time.Second,
		HealthRetries:  cfg.Sidecar.HealthRetries,
		StartupTimeout: time.Duration(cfg.Sidecar.StartupTimeout) * time.Second,
	})
	if err := sc.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "sidecar start failed: %v\n", err)
		os.Exit(1)
	}
	defer sc.Stop()

	// Init providers.
	embedder, err := provider.NewEmbedProvider(cfg.Embedding, sc.URL())
	if err != nil {
		fmt.Fprintf(os.Stderr, "embed provider: %v\n", err)
		os.Exit(1)
	}
	reranker, _ := provider.NewRerankProvider(cfg.Rerank, sc.URL())
	llm, _ := provider.NewLLMProvider(cfg.LLM)

	// Init store.
	st, err := store.New(cfg.Storage.DBPath, cfg.Embedding.Dims)
	if err != nil {
		fmt.Fprintf(os.Stderr, "store init: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	// Init chunker.
	chunker := chunk.NewChunker(cfg.Chunk, embedder, llm)

	// Run MCP server (blocks until client disconnects).
	managementService := management.New(management.Deps{
		Config:   cfg,
		Store:    st,
		Embedder: embedder,
		Chunker:  chunker,
	})
	deps := mcpserver.Deps{
		Config:     cfg,
		Store:      st,
		Embedder:   embedder,
		Reranker:   reranker,
		LLM:        llm,
		Chunker:    chunker,
		Management: managementService,
	}
	if err := mcpserver.Run(context.Background(), deps); err != nil {
		fmt.Fprintf(os.Stderr, "MCP server error: %v\n", err)
		os.Exit(1)
	}
}
