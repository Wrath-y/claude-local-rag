package graphsnapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/gowebpki/jcs"
)

// Manifest is the sole value covered by a Graph Snapshot content hash. It
// intentionally excludes namespace, version, mode, task, component, and
// embedding metadata so an equivalent full and delta request converge.
type Manifest struct {
	SchemaVersion string `json:"schema_version"`
	Nodes         []Node `json:"nodes"`
	Edges         []Edge `json:"edges"`
}

// CanonicalManifest produces RFC 8785/JCS bytes after sorting final entities
// by stable ID. The input slices are copied before sorting, preserving request
// order for callers that need to report validation errors by original index.
func CanonicalManifest(nodes []Node, edges []Edge) ([]byte, error) {
	normalizedNodes := append([]Node(nil), nodes...)
	normalizedEdges := append([]Edge(nil), edges...)
	sort.Slice(normalizedNodes, func(left, right int) bool { return normalizedNodes[left].ID < normalizedNodes[right].ID })
	sort.Slice(normalizedEdges, func(left, right int) bool { return normalizedEdges[left].ID < normalizedEdges[right].ID })
	payload, err := json.Marshal(Manifest{SchemaVersion: SchemaVersionV1, Nodes: normalizedNodes, Edges: normalizedEdges})
	if err != nil {
		return nil, fmt.Errorf("marshal graph manifest: %w", err)
	}
	canonical, err := jcs.Transform(payload)
	if err != nil {
		return nil, fmt.Errorf("canonicalize graph manifest: %w", err)
	}
	return canonical, nil
}

func CanonicalHash(nodes []Node, edges []Edge) ([]byte, string, error) {
	canonical, err := CanonicalManifest(nodes, edges)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(canonical)
	return canonical, hex.EncodeToString(sum[:]), nil
}
