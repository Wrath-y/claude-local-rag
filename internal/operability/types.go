// Package operability owns transport-neutral graph health and rebuild types.
package operability

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/Wrath-y/local-rag/internal/graphsnapshot"
)

var (
	ErrReimportRequired    = errors.New("graph snapshot source requires reimport")
	ErrIdempotencyConflict = errors.New("rebuild idempotency key conflicts")
	ErrSnapshotNotFound    = errors.New("graph snapshot not found")
)

type Component string

const (
	ComponentGraphIndexes Component = "graph_indexes"
	ComponentFTS          Component = "fts"
	ComponentVector       Component = "vector"
)

var componentOrder = []Component{ComponentGraphIndexes, ComponentFTS, ComponentVector}

type RebuildRequest struct {
	Components []Component `json:"components"`
}

type RebuildSubmission struct {
	TaskID          string                  `json:"task_id"`
	State           graphsnapshot.TaskState `json:"state"`
	TaskURL         string                  `json:"task_url"`
	Components      []Component             `json:"components"`
	Namespace       string                  `json:"namespace"`
	SnapshotVersion string                  `json:"snapshot_version"`
	Replayed        bool                    `json:"replayed"`
}

type GenerationIdentity struct {
	Component     Component `json:"component"`
	Generation    string    `json:"generation"`
	ContentDigest string    `json:"content_digest"`
	Algorithm     string    `json:"algorithm"`
	Provider      string    `json:"provider,omitempty"`
	Model         string    `json:"model,omitempty"`
	Dimensions    int       `json:"dimensions,omitempty"`
}

type HealthStatus string

const (
	HealthOK          HealthStatus = "ok"
	HealthDegraded    HealthStatus = "degraded"
	HealthUnavailable HealthStatus = "unavailable"
)

type CapabilityState string

const (
	CapabilityAvailable   CapabilityState = "available"
	CapabilityDegraded    CapabilityState = "degraded"
	CapabilityUnavailable CapabilityState = "unavailable"
	CapabilityDisabled    CapabilityState = "disabled"
)

type Capability struct {
	Name   string          `json:"name"`
	State  CapabilityState `json:"state"`
	Reason string          `json:"reason,omitempty"`
}

type Limit struct {
	Name  string `json:"name"`
	Value int    `json:"value"`
}

type Dependency struct {
	Name     string          `json:"name"`
	State    CapabilityState `json:"state"`
	Reason   string          `json:"reason,omitempty"`
	Provider string          `json:"provider,omitempty"`
	Model    string          `json:"model,omitempty"`
}

type Health struct {
	SchemaVersion           string       `json:"schema_version"`
	Status                  HealthStatus `json:"status"`
	Service                 string       `json:"service"`
	ServiceVersion          string       `json:"service_version"`
	APIVersions             []string     `json:"api_versions"`
	SupportedSchemaVersions []string     `json:"supported_schema_versions"`
	Capabilities            []Capability `json:"capabilities"`
	Limits                  []Limit      `json:"limits"`
	Dependencies            []Dependency `json:"dependencies"`
}

func NormalizeComponents(values []Component) ([]Component, error) {
	seen := make(map[Component]struct{}, len(values))
	for _, value := range values {
		if value != ComponentGraphIndexes && value != ComponentFTS && value != ComponentVector {
			return nil, fmt.Errorf("unknown rebuild component %q", value)
		}
		if _, duplicate := seen[value]; duplicate {
			return nil, fmt.Errorf("duplicate rebuild component %q", value)
		}
		seen[value] = struct{}{}
	}
	if len(seen) == 0 {
		return nil, fmt.Errorf("at least one rebuild component is required")
	}
	normalized := make([]Component, 0, len(seen))
	for _, value := range componentOrder {
		if _, ok := seen[value]; ok {
			normalized = append(normalized, value)
		}
	}
	return normalized, nil
}

func ValidateIdempotencyKey(value string) error {
	if len(value) == 0 || len(value) > 128 {
		return fmt.Errorf("idempotency key length is invalid")
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '.' || char == '_' || char == '-' {
			continue
		}
		return fmt.Errorf("idempotency key contains invalid characters")
	}
	return nil
}

func RequestFingerprint(components []Component) string {
	parts := make([]string, len(components))
	for i, component := range components {
		parts[i] = string(component)
	}
	digest := sha256.Sum256([]byte(strings.Join(parts, ",")))
	return hex.EncodeToString(digest[:])
}
