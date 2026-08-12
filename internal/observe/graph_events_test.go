package observe

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestGraphEventContainsOnlySafeOperationalFields(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	LogGraphEvent(logger, "task_terminal", "snapshot_rebuild", "vector", "task-1", "request-1", "INTERNAL_ERROR")
	text := output.String()
	for _, required := range []string{`"event":"task_terminal"`, `"operation":"snapshot_rebuild"`, `"component":"vector"`, `"task_id":"task-1"`, `"request_id":"request-1"`, `"code":"INTERNAL_ERROR"`} {
		if !strings.Contains(text, required) {
			t.Fatalf("missing %s in %s", required, text)
		}
	}
	for _, forbidden := range []string{"body", "embedding", "secret", "SELECT", "/tmp/"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("unsafe %s in %s", forbidden, text)
		}
	}
}
