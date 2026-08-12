package observe

import "log/slog"

// GraphEvent emits a deliberately small operational record. Callers provide
// only bounded event/code/component values and safe correlation IDs; request
// bodies, graph data, vectors, provider payloads, SQL, and paths never cross
// this boundary.
func GraphEvent(event, operation, component, taskID, requestID, code string) {
	LogGraphEvent(slog.Default(), event, operation, component, taskID, requestID, code)
}

func LogGraphEvent(logger *slog.Logger, event, operation, component, taskID, requestID, code string) {
	attrs := []any{"event", event}
	if operation != "" {
		attrs = append(attrs, "operation", operation)
	}
	if component != "" {
		attrs = append(attrs, "component", component)
	}
	if taskID != "" {
		attrs = append(attrs, "task_id", taskID)
	}
	if requestID != "" {
		attrs = append(attrs, "request_id", requestID)
	}
	if code != "" {
		attrs = append(attrs, "code", code)
	}
	logger.Info("graph operation", attrs...)
}
