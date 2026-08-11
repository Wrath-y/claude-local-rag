// Package graphsnapshot defines the transport-neutral immutable graph
// lifecycle model. Persistence and HTTP adapters depend on these types; the
// model intentionally has no SQLite, Gin, or provider imports.
package graphsnapshot

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

const SchemaVersionV1 = "1.0"

type Mode string

const (
	ModeFull  Mode = "full"
	ModeDelta Mode = "delta"
)

type ComponentName string

const (
	ComponentGraph  ComponentName = "graph"
	ComponentFTS    ComponentName = "fts"
	ComponentVector ComponentName = "vector"
)

type ComponentState string

const (
	ComponentPending     ComponentState = "pending"
	ComponentBuilding    ComponentState = "building"
	ComponentReady       ComponentState = "ready"
	ComponentFailed      ComponentState = "failed"
	ComponentUnavailable ComponentState = "unavailable"
)

type SnapshotStatus string

const (
	SnapshotBuilding SnapshotStatus = "building"
	SnapshotReady    SnapshotStatus = "ready"
	SnapshotFailed   SnapshotStatus = "failed"
)

type TaskState string

const (
	TaskQueued    TaskState = "queued"
	TaskRunning   TaskState = "running"
	TaskSucceeded TaskState = "succeeded"
	TaskFailed    TaskState = "failed"
)

// Node and Edge keep properties/provenance as raw JSON. The strict decoder
// validates JSON syntax, duplicate members, and number spellings before these
// bytes reach canonicalization; RawMessage prevents float64 conversion.
type Node struct {
	ID         string          `json:"id"`
	Type       string          `json:"type"`
	Label      string          `json:"label"`
	Text       string          `json:"text"`
	Properties json.RawMessage `json:"properties"`
	Provenance json.RawMessage `json:"provenance"`
}

type Edge struct {
	ID           string          `json:"id"`
	From         string          `json:"from"`
	To           string          `json:"to"`
	Type         string          `json:"type"`
	RelationKind string          `json:"relation_kind"`
	Confidence   json.Number     `json:"confidence"`
	Properties   json.RawMessage `json:"properties"`
	Provenance   json.RawMessage `json:"provenance"`
}

// Request accepts either a complete graph or a set of operations over a base
// version. Validation and normalization decide which fields are legal for the
// requested mode; keeping one request type makes the HTTP contract explicit.
type Request struct {
	SchemaVersion string `json:"schema_version"`
	Mode          Mode   `json:"mode"`
	BaseVersion   string `json:"base_version,omitempty"`
	ContentHash   string `json:"content_hash"`

	Nodes []Node `json:"nodes,omitempty"`
	Edges []Edge `json:"edges,omitempty"`

	NodeUpserts []Node   `json:"node_upserts,omitempty"`
	NodeDeletes []string `json:"node_deletes,omitempty"`
	EdgeUpserts []Edge   `json:"edge_upserts,omitempty"`
	EdgeDeletes []string `json:"edge_deletes,omitempty"`
}

type Component struct {
	Name       ComponentName  `json:"name"`
	State      ComponentState `json:"state"`
	Generation string         `json:"generation,omitempty"`
	Error      *Error         `json:"error,omitempty"`
}

type Snapshot struct {
	Namespace     string         `json:"namespace"`
	Version       string         `json:"version"`
	BaseVersion   *string        `json:"base_version"`
	SchemaVersion string         `json:"schema_version"`
	ContentHash   string         `json:"content_hash"`
	NodeCount     int            `json:"node_count"`
	EdgeCount     int            `json:"edge_count"`
	TaskID        string         `json:"task_id"`
	Status        SnapshotStatus `json:"status"`
	QueryReady    bool           `json:"query_ready"`
	Components    []Component    `json:"components"`
	Warnings      []string       `json:"warnings"`
}

type Task struct {
	ID                  string          `json:"task_id"`
	Operation           string          `json:"operation"`
	Namespace           string          `json:"namespace"`
	Version             string          `json:"snapshot_version"`
	State               TaskState       `json:"state"`
	Phase               string          `json:"phase"`
	Progress            int             `json:"progress"`
	Warnings            []string        `json:"warnings"`
	SourceHash          string          `json:"source_hash,omitempty"`
	SubmissionRequestID string          `json:"submission_request_id,omitempty"`
	CreatedAt           time.Time       `json:"created_at"`
	StartedAt           *time.Time      `json:"started_at,omitempty"`
	FinishedAt          *time.Time      `json:"finished_at,omitempty"`
	Result              json.RawMessage `json:"result,omitempty"`
	Error               *Error          `json:"error,omitempty"`
}

// DecodeRequest rejects duplicate object members at every depth and preserves
// numeric tokens. It also rejects unknown request fields, trailing values, and
// malformed JSON before the request reaches graph validation.
func DecodeRequest(reader io.Reader) (Request, error) {
	body, err := io.ReadAll(reader)
	if err != nil {
		return Request{}, fmt.Errorf("read graph request: %w", err)
	}
	var request Request
	if err := DecodeStrictJSON(body, &request); err != nil {
		return Request{}, err
	}
	return request, nil
}

// DecodeStrictJSON is the shared /v1 JSON binding primitive. It rejects
// duplicate members at every depth, unknown fields, and trailing values while
// retaining numeric tokens for callers that need canonical validation.
func DecodeStrictJSON(body []byte, target any) error {
	if err := rejectDuplicateMembers(body); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode graph request: %w", err)
	}
	if token, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return fmt.Errorf("decode graph request: trailing value %v", token)
		}
		return fmt.Errorf("decode graph request: %w", err)
	}
	return nil
}

func rejectDuplicateMembers(body []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := inspectValue(decoder); err != nil {
		return fmt.Errorf("decode graph request: %w", err)
	}
	if token, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return fmt.Errorf("decode graph request: trailing value %v", token)
		}
		return fmt.Errorf("decode graph request: %w", err)
	}
	return nil
}

func inspectValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, objectOrArray := token.(json.Delim)
	if !objectOrArray {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]struct{}{}
		for decoder.More() {
			keyToken, keyErr := decoder.Token()
			if keyErr != nil {
				return keyErr
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("object key is not a string")
			}
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("duplicate object member %q", key)
			}
			seen[key] = struct{}{}
			if err := inspectValue(decoder); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	case '[':
		for decoder.More() {
			if err := inspectValue(decoder); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	default:
		return fmt.Errorf("invalid JSON delimiter %q", string(delimiter))
	}
}

func normalizedID(value string) string { return strings.TrimSpace(value) }
