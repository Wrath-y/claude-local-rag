"""Task 6.5 — ingest/retrieve emit parseable JSON events to stdout."""

from __future__ import annotations

import json

from fastapi.testclient import TestClient


def _collect_events(captured_stdout: str, event_name: str):
    events = []
    for line in captured_stdout.splitlines():
        line = line.strip()
        if not (line.startswith("{") and line.endswith("}")):
            continue
        try:
            obj = json.loads(line)
        except json.JSONDecodeError:
            continue
        if obj.get("event") == event_name:
            events.append(obj)
    return events


def test_ingest_emits_structured_event(isolated_store, capsys):
    server = isolated_store["server"]
    with TestClient(server.app) as c:
        r = c.post("/ingest", json={"text": "可 观察性 测试 专用 文字。", "source": "evt-ingest"})
    assert r.status_code == 200

    out = capsys.readouterr().out
    events = _collect_events(out, "ingest_done")
    assert len(events) >= 1
    e = events[-1]
    assert e["source"] == "evt-ingest"
    assert "chunks_added" in e
    assert "status" in e


def test_retrieve_emits_structured_event(isolated_store, seed_consistent_state, capsys):
    server = isolated_store["server"]
    seed_consistent_state(server, [{"text": "some baseline content here", "source": "b"}])

    with TestClient(server.app) as c:
        r = c.post("/retrieve", json={"text": "baseline"})
    assert r.status_code == 200

    out = capsys.readouterr().out
    events = _collect_events(out, "retrieve_done")
    assert len(events) >= 1
    e = events[-1]
    assert isinstance(e["hit"], bool)
    assert isinstance(e["latency_ms"], (int, float))
    assert "returned_chunks" in e
