package handler

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Wrath-y/local-rag/internal/config"
	"github.com/Wrath-y/local-rag/internal/graphsnapshot"
)

const (
	defaultGraphMaxPayloadBytes = 32 << 20
	defaultGraphMaxNodes        = 10000
	defaultGraphMaxEdges        = 100000
)

// GraphSnapshotService is the write-side dependency of the additive /v1
// graph API. Keeping it narrow makes the handler independently testable and
// does not couple legacy endpoints to graph lifecycle storage.
type GraphSnapshotService interface {
	Put(context.Context, string, string, graphsnapshot.Request) (graphsnapshot.SubmissionCheck, *graphsnapshot.Error)
}

// PutGraphSnapshot accepts an immutable graph snapshot. The durable task is
// keyed by the canonical final content hash, so Idempotency-Key is deliberately
// neither read nor interpreted here.
func (h *Handler) PutGraphSnapshot(c *gin.Context) {
	namespace, version, ok := graphSnapshotIdentity(c)
	if !ok {
		return
	}
	if h.graphService == nil {
		writeGraphError(c, graphsnapshot.NewError(graphsnapshot.CodeGraphStoreUnavailable, nil, nil))
		return
	}

	limits := graphLimits(h.deps.Config)
	if c.Request.ContentLength > int64(limits.maxPayloadBytes) {
		writeGraphError(c, graphLimitError("payload_bytes", limits.maxPayloadBytes))
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, int64(limits.maxPayloadBytes))
	request, err := graphsnapshot.DecodeRequest(c.Request.Body)
	if err != nil {
		writeGraphError(c, graphsnapshot.NewError(graphsnapshot.CodeInvalidSnapshotRequest, map[string]any{"field": "body"}, err))
		return
	}
	if graphErr := validateGraphRequestLimits(request, limits); graphErr != nil {
		writeGraphError(c, graphErr)
		return
	}

	result, graphErr := h.graphService.Put(c.Request.Context(), namespace, version, request)
	if graphErr != nil {
		writeGraphError(c, graphErr)
		return
	}
	status := http.StatusAccepted
	if result.Existing && result.Snapshot.Status != graphsnapshot.SnapshotBuilding {
		status = http.StatusOK
	}
	c.JSON(status, result.Snapshot)
}

// GetGraphSnapshot returns the same canonical lifecycle resource used for a
// PUT replay. It cannot inspect staging or unselected derived generations.
func (h *Handler) GetGraphSnapshot(c *gin.Context) {
	namespace, version, ok := graphSnapshotIdentity(c)
	if !ok {
		return
	}
	if h.graphReader == nil {
		writeGraphError(c, graphsnapshot.NewError(graphsnapshot.CodeGraphStoreUnavailable, nil, nil))
		return
	}
	snapshot, found, err := h.graphReader.LookupGraphSnapshot(c.Request.Context(), namespace, version)
	if err != nil {
		writeGraphError(c, graphsnapshot.NewError(graphsnapshot.CodeGraphStoreUnavailable, nil, err))
		return
	}
	if !found {
		writeGraphError(c, graphsnapshot.NewError(graphsnapshot.CodeSnapshotNotFound, map[string]any{"namespace": namespace, "version": version}, nil))
		return
	}
	c.JSON(http.StatusOK, snapshot)
}

func graphSnapshotIdentity(c *gin.Context) (string, string, bool) {
	namespace, version := c.Param("namespace"), c.Param("version")
	if namespace == "" || version == "" {
		writeGraphError(c, graphsnapshot.NewError(graphsnapshot.CodeInvalidSnapshotRequest, map[string]any{"field": "namespace_or_version"}, nil))
		return "", "", false
	}
	return namespace, version, true
}

type graphRequestLimits struct {
	maxPayloadBytes int
	maxNodes        int
	maxEdges        int
}

func graphLimits(cfg *config.Config) graphRequestLimits {
	limits := graphRequestLimits{maxPayloadBytes: defaultGraphMaxPayloadBytes, maxNodes: defaultGraphMaxNodes, maxEdges: defaultGraphMaxEdges}
	if cfg == nil {
		return limits
	}
	if cfg.Graph.MaxPayloadBytes > 0 {
		limits.maxPayloadBytes = cfg.Graph.MaxPayloadBytes
	}
	if cfg.Graph.MaxNodes > 0 {
		limits.maxNodes = cfg.Graph.MaxNodes
	}
	if cfg.Graph.MaxEdges > 0 {
		limits.maxEdges = cfg.Graph.MaxEdges
	}
	return limits
}

func validateGraphRequestLimits(request graphsnapshot.Request, limits graphRequestLimits) *graphsnapshot.Error {
	nodes, edges := len(request.Nodes), len(request.Edges)
	if request.Mode == graphsnapshot.ModeDelta {
		nodes = len(request.NodeUpserts) + len(request.NodeDeletes)
		edges = len(request.EdgeUpserts) + len(request.EdgeDeletes)
	}
	if nodes > limits.maxNodes {
		return graphLimitError("nodes", limits.maxNodes)
	}
	if edges > limits.maxEdges {
		return graphLimitError("edges", limits.maxEdges)
	}
	return nil
}

func graphLimitError(field string, limit int) *graphsnapshot.Error {
	return graphsnapshot.NewError(graphsnapshot.CodeInvalidSnapshotRequest, map[string]any{"field": field, "limit": limit}, nil)
}
