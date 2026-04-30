"""Task 7.4 smoke equivalent — simulate the full flow:

  1. Fresh startup → empty state
  2. Several successful ingests → disk state consistent
  3. Crash during the middle of an ingest (faiss.write_index raises)
  4. Restart → load_store succeeds and returns the last consistent state
  5. /storage/integrity-check returns 200

Running an actual kill -9 on the live server would interrupt the user's session
and is covered in the user-facing runbook. This test exercises the same invariants
without leaving the pytest process.
"""

from __future__ import annotations

import pickle
from unittest.mock import patch

import numpy as np
from fastapi.testclient import TestClient


def _rand_vecs(n, dim, seed):
    rng = np.random.default_rng(seed)
    v = rng.random((n, dim), dtype=np.float32)
    v /= (np.linalg.norm(v, axis=1, keepdims=True) + 1e-9)
    return v.astype(np.float32)


def test_crash_during_ingest_leaves_previous_state_intact(isolated_store, seed_consistent_state):
    server = isolated_store["server"]
    import faiss

    # Step 1-2: persist a good baseline with 3 chunks
    seed_consistent_state(
        server,
        [
            {"text": "alpha", "source": "s1"},
            {"text": "beta", "source": "s1"},
            {"text": "gamma", "source": "s2"},
        ],
    )
    baseline_chunks = list(server.stored_chunks)
    baseline_ntotal = server.index.ntotal
    baseline_chunks_bytes = open(server.TEXTS_PATH, "rb").read()
    baseline_index_bytes = open(server.INDEX_PATH, "rb").read()
    baseline_manifest_bytes = open(server.MANIFEST_PATH, "rb").read()

    # Step 3: simulate crash mid-ingest by making the FAISS write blow up
    new_index = faiss.clone_index(server.index)
    new_index.add(_rand_vecs(2, server.DIM, seed=7))
    new_chunks = baseline_chunks + [
        {"text": "delta", "source": "s3"},
        {"text": "epsilon", "source": "s3"},
    ]

    import pytest
    with patch("storage.faiss.write_index", side_effect=OSError("disk full")):
        with pytest.raises(OSError):
            server.save_store(new_index=new_index, new_chunks=new_chunks)

    # Disk must be identical to the baseline — no partial writes
    assert open(server.TEXTS_PATH, "rb").read() == baseline_chunks_bytes
    assert open(server.INDEX_PATH, "rb").read() == baseline_index_bytes
    assert open(server.MANIFEST_PATH, "rb").read() == baseline_manifest_bytes

    # Step 4: Restart — simulate by invoking load_store on the same paths
    server.load_store()
    assert len(server.stored_chunks) == len(baseline_chunks)
    assert server.index.ntotal == baseline_ntotal

    # Step 5: integrity check via endpoint
    with TestClient(server.app) as c:
        r = c.get("/storage/integrity-check")
    assert r.status_code == 200
    body = r.json()
    assert body["status"] == "ok"
    assert body["chunks"]["count"] == len(baseline_chunks)
    assert body["index"]["ntotal"] == baseline_ntotal
