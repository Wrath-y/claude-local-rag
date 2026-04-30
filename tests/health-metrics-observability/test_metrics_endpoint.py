"""Tasks 6.1-6.3 — /metrics endpoint surface + counter updates."""

from __future__ import annotations

from fastapi.testclient import TestClient


def test_metrics_endpoint_returns_prometheus_text(isolated_store):
    server = isolated_store["server"]
    with TestClient(server.app) as c:
        r = c.get("/metrics")
    assert r.status_code == 200
    assert "text/plain" in r.headers["content-type"]
    body = r.text
    for name in (
        "rag_ingest_total",
        "rag_retrieve_total",
        "rag_retrieve_latency_seconds",
        "rag_chunk_total",
        "rag_wal_replaying",
    ):
        assert name in body, f"missing metric: {name}"


def _ingest_count(body: str, result_label: str) -> float:
    """Extract the rag_ingest_total counter value for a given label from scraped text."""
    import re
    pattern = rf'rag_ingest_total\{{result="{result_label}"\}}\s+([\d.e+]+)'
    match = re.search(pattern, body)
    return float(match.group(1)) if match else 0.0


def test_ingest_increments_counter(isolated_store):
    server = isolated_store["server"]
    # prometheus-client counters have process-wide state; read baseline first.
    with TestClient(server.app) as c:
        baseline = _ingest_count(c.get("/metrics").text, "ok")
        r = c.post("/ingest", json={"text": "一段 文字 用于 测试 指标 的 变化。", "source": "m1"})
        assert r.status_code == 200
        after = _ingest_count(c.get("/metrics").text, "ok")
    assert after == baseline + 1


def test_retrieve_increments_counter_and_histogram(isolated_store, seed_consistent_state):
    server = isolated_store["server"]
    seed_consistent_state(server, [{"text": "baseline one", "source": "b"}])

    with TestClient(server.app) as c:
        r = c.post("/retrieve", json={"text": "baseline"})
        assert r.status_code == 200
        body = c.get("/metrics").text

    assert "rag_retrieve_latency_seconds_count" in body
