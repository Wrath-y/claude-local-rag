package handler

import (
	"time"

	"github.com/Wrath-y/local-rag/internal/chunk"
	"github.com/Wrath-y/local-rag/internal/citation"
	"github.com/Wrath-y/local-rag/internal/config"
	"github.com/Wrath-y/local-rag/internal/document"
	"github.com/Wrath-y/local-rag/internal/graphsnapshot"
	"github.com/Wrath-y/local-rag/internal/management"
	"github.com/Wrath-y/local-rag/internal/observe"
	"github.com/Wrath-y/local-rag/internal/provider"
	"github.com/Wrath-y/local-rag/internal/sourcesync"
	"github.com/Wrath-y/local-rag/internal/store"
)

// Deps holds all external dependencies for the handler layer.
type Deps struct {
	Config     *config.Config
	Store      *store.Store // Deprecated: New wraps this in Stores when needed.
	Stores     *StoreLifecycle
	Restore    *RestoreService
	Embedder   provider.EmbedProvider
	Reranker   provider.RerankProvider // may be nil if disabled
	LLM        provider.LLMProvider    // may be nil
	Chunker    chunk.Chunker
	Management *management.Service
	Sync       *sourcesync.Service
	// GraphService and GraphSnapshotReader are used only by the additive /v1
	// graph lifecycle endpoints. Legacy handlers remain independent of them.
	GraphService        GraphSnapshotService
	GraphSnapshotReader graphsnapshot.ExistingSnapshotReader
	GraphTaskReader     GraphTaskReader
	// LoaderRegistry and FeishuResolver make input resolution explicit and
	// replaceable in tests. A nil resolver safely rejects Feishu URLs.
	LoaderRegistry *document.Registry
	FeishuResolver document.FeishuResolver
}

// Handler is the HTTP handler collection.
type Handler struct {
	deps             Deps
	indexRebuild     *indexRebuildCoordinator
	hookObservations *observe.HookObservations
	citations        *citation.Manager
	management       *management.Service
	agent            agentState
	ingestService    document.Service
	syncService      *sourcesync.Service
	graphService     GraphSnapshotService
	graphReader      graphsnapshot.ExistingSnapshotReader
	graphTaskReader  GraphTaskReader

	// runtime toggleable state
	rerankEnabled        bool
	verboseEnabled       bool
	dynamicTopKEnabled   bool
	queryRewriteEnabled  bool
	queryRewriteStrategy string
	chunkStrategy        string
}

// New creates a Handler with the given dependencies.
func New(deps Deps) *Handler {
	if deps.Stores == nil && deps.Store != nil {
		deps.Stores = NewStoreLifecycle(deps.Store)
	}
	if deps.Restore == nil && deps.Stores != nil && deps.Config != nil {
		deps.Restore = NewRestoreService(deps.Stores, deps.Config.Storage.DBPath, deps.Config.Embedding.Dims)
	}
	if deps.Management == nil {
		var currentStore *store.Store
		if deps.Stores != nil {
			currentStore = deps.Stores.Store()
		} else {
			currentStore = deps.Store
		}
		deps.Management = management.New(management.Deps{Config: deps.Config, Store: currentStore, Embedder: deps.Embedder, Chunker: deps.Chunker, Lifecycle: deps.Stores})
	}
	strategy := ""
	if deps.Config != nil {
		strategy = deps.Config.Chunk.Strategy
		if strategy == "" {
			strategy = "fixed"
		}
	}
	h := &Handler{
		deps:                 deps,
		management:           deps.Management,
		graphService:         deps.GraphService,
		graphReader:          deps.GraphSnapshotReader,
		graphTaskReader:      deps.GraphTaskReader,
		queryRewriteStrategy: "expansion",
		chunkStrategy:        strategy,
	}
	if deps.LoaderRegistry == nil {
		deps.LoaderRegistry = document.BuiltinRegistryWithOptions(deps.FeishuResolver, connectorOptions(deps.Config))
		h.deps.LoaderRegistry = deps.LoaderRegistry
	}
	h.ingestService = document.Service{
		Registry: deps.LoaderRegistry,
		Pipeline: document.PipelineFunc(h.ingestDocument),
	}
	if deps.Sync != nil {
		h.syncService = deps.Sync
	} else if deps.Stores != nil {
		h.syncService = sourcesync.New(deps.Stores.Store(), deps.Chunker, deps.Embedder, deps.Config)
	}
	if h.syncService != nil && deps.Config != nil && deps.Config.Sync.Enabled {
		if err := h.syncService.Start(); err != nil {
			panic("start sync dispatcher: " + err.Error())
		}
	}
	dims := 0
	if deps.Config != nil {
		dims = deps.Config.Embedding.Dims
	}
	h.indexRebuild = newIndexRebuildCoordinator(deps.Stores, deps.Embedder, dims, deps.Management)
	h.hookObservations = observe.NewHookObservations()
	h.citations = citation.NewManager(time.Hour)
	return h
}

func connectorOptions(cfg *config.Config) document.Options {
	if cfg == nil {
		return document.Options{}
	}
	return document.Options{AllowedLocalPaths: cfg.Connectors.AllowedLocalPaths, AllowedURLSchemes: cfg.Connectors.AllowedURLSchemes, Exclusions: cfg.Connectors.Exclusions, Limits: document.Limits{SourceBytes: cfg.Connectors.MaxSourceBytes, Documents: cfg.Connectors.MaxDocuments, ExtractedBytes: cfg.Connectors.MaxExtractedBytes, DurationSecs: cfg.Connectors.MaxDurationSecs, GitFiles: cfg.Connectors.MaxGitFiles, GitFileBytes: cfg.Connectors.MaxGitFileBytes, GitTotalBytes: cfg.Connectors.MaxGitTotalBytes}}
}
