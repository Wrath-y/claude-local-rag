package observe

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestIngestTotal_Increments(t *testing.T) {
	InitMetrics()

	IngestTotal.WithLabelValues("ok").Inc()
	IngestTotal.WithLabelValues("ok").Inc()
	IngestTotal.WithLabelValues("error").Inc()

	if got := testutil.ToFloat64(IngestTotal.WithLabelValues("ok")); got != 2 {
		t.Errorf("expected 2 ok increments, got %v", got)
	}
	if got := testutil.ToFloat64(IngestTotal.WithLabelValues("error")); got != 1 {
		t.Errorf("expected 1 error increment, got %v", got)
	}
}

func TestRender_ContainsMetricName(t *testing.T) {
	InitMetrics()

	IngestTotal.WithLabelValues("ok").Inc()

	output := string(Render())
	if !strings.Contains(output, "rag_ingest_total") {
		t.Errorf("expected Render() output to contain 'rag_ingest_total', got:\n%s", output)
	}
}

func TestGraphMetricsUseOnlyBoundedLabels(t *testing.T) {
	InitMetrics()
	GraphHealthState.WithLabelValues("degraded").Set(1)
	GraphTaskTransitions.WithLabelValues("snapshot_rebuild", "running").Inc()
	GraphTaskQueueDepth.Set(2)
	GraphTaskDuration.WithLabelValues("snapshot_rebuild", "succeeded").Observe(0.1)
	GraphRebuildComponentOutcomes.WithLabelValues("vector", "succeeded").Inc()
	GraphRebuildComponentDuration.WithLabelValues("vector", "succeeded").Observe(0.1)
	GraphRecoveryTotal.Inc()
	output := string(Render())
	for _, want := range []string{"rag_graph_health_state", "rag_graph_task_transitions_total", "rag_graph_task_queue_depth", "rag_graph_task_duration_seconds", "rag_graph_rebuild_component_outcomes_total", "rag_graph_rebuild_component_duration_seconds", "rag_graph_recovery_total"} {
		if !strings.Contains(output, want) {
			t.Fatalf("metric %q missing from %s", want, output)
		}
	}
	for _, forbidden := range []string{"namespace=", "task_id=", "request_id=", "generation="} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("unbounded label %q in %s", forbidden, output)
		}
	}
}
