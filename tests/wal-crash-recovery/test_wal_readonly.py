"""Task 8.5 — WAL corruption forces read-only degradation."""

from __future__ import annotations

from fastapi.testclient import TestClient


def test_corrupt_wal_puts_service_in_readonly_mode(isolated_store, seed_consistent_state):
    server = isolated_store["server"]
    import wal as wal_mod
    import storage as storage_mod

    seed_consistent_state(server, [{"text": "baseline", "source": "base"}])

    # Craft a WAL where the first entry is valid (will be replayed) and the second is corrupt
    good = wal_mod.encode_record(wal_mod.make_record(1, "ingest",
                                                     {"text": "良好 数据 一 条。", "source": "good-src"}))
    bad = b'{"this is garbage json\n'
    wal_mod.append(server.WAL_PATH, good)
    with open(server.WAL_PATH, "ab") as f:
        f.write(bad)

    # Reset manifest offsets so replay tries from scratch
    manifest = storage_mod.read_manifest(server.MANIFEST_PATH)
    manifest.wal.committed_offset = 0
    manifest.wal.committed_seq = 0
    storage_mod.write_manifest(server.MANIFEST_PATH, manifest)

    server.load_store()

    # Service entered read-only degradation
    assert server._wal_readonly_reason is not None
    assert "corrupt" in server._wal_readonly_reason.lower()

    # Write endpoints return 503; retrieve still works
    with TestClient(server.app) as c:
        r = c.post("/ingest", json={"text": "读写 被 阻止 因为 WAL 坏了。", "source": "post-corrupt"})
        assert r.status_code == 503
        r2 = c.post("/retrieve", json={"text": "baseline"})
        assert r2.status_code == 200

    # Health surfaces the reason
    with TestClient(server.app) as c:
        h = c.get("/health")
    body = h.json()
    assert body["wal_readonly_reason"] is not None
