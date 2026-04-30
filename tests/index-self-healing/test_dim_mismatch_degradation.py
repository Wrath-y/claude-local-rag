"""Task 6.3 — index dim != model DIM on startup degrades to read-only (no raise)."""

from __future__ import annotations

import pickle

import faiss
import pytest


def test_dim_mismatch_enters_readonly(isolated_store, monkeypatch):
    server = isolated_store["server"]
    import storage as storage_mod

    # Build an index of a different dimension (simulating a prior model)
    wrong_dim = server.DIM + 1
    foreign_index = faiss.IndexFlatIP(wrong_dim)
    faiss.write_index(foreign_index, server.INDEX_PATH)

    # Empty chunks.pkl keeps count == ntotal == 0 so manifest check passes
    with open(server.TEXTS_PATH, "wb") as f:
        pickle.dump([], f)

    # Build manifest matching the foreign index (dim=wrong_dim) so verify_manifest
    # passes for the manifest layer; the dim-vs-DIM check is a separate gate.
    import os
    os.makedirs(server.STORAGE_DIR, exist_ok=True)
    manifest = storage_mod.build_manifest_from_files(
        server.TEXTS_PATH, server.INDEX_PATH, 0, foreign_index,
        wal_path="wal.jsonl", wal_committed_offset=0, wal_committed_seq=0,
    )
    storage_mod.write_manifest(server.MANIFEST_PATH, manifest)

    # load_store must not raise; must enter degraded/read-only state
    server.load_store()

    assert server._wal_readonly_reason is not None
    assert "dim mismatch" in server._wal_readonly_reason
