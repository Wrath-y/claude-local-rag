"""Task 6.5 — load_store refuses to start when manifest disagrees with files."""

from __future__ import annotations

import os
import pickle

import pytest


def test_load_store_raises_on_count_mismatch(isolated_store, seed_consistent_state):
    server = isolated_store["server"]

    seed_consistent_state(server, [{"text": "a", "source": "s"}])

    # Tamper chunks file so count grows to 2 while index stays at 1
    with open(server.TEXTS_PATH, "wb") as f:
        pickle.dump([{"text": "a", "source": "s"}, {"text": "b", "source": "s"}], f)

    with pytest.raises(RuntimeError) as exc:
        server.load_store()

    msg = str(exc.value)
    assert "storage" in msg and ("inconsistency" in msg or "mismatch" in msg)


def test_load_store_raises_on_manifest_sha_mismatch(isolated_store, seed_consistent_state):
    """Same chunk count, different content → manifest sha256 check trips."""
    server = isolated_store["server"]

    seed_consistent_state(server, [{"text": "a", "source": "s"}])

    with open(server.TEXTS_PATH, "wb") as f:
        pickle.dump([{"text": "DIFFERENT", "source": "s"}], f)

    with pytest.raises(RuntimeError) as exc:
        server.load_store()

    assert "manifest mismatch" in str(exc.value)


def test_load_store_autogenerates_manifest_when_missing(isolated_store, seed_consistent_state):
    server = isolated_store["server"]

    seed_consistent_state(server, [{"text": "a", "source": "s"}])

    os.remove(server.MANIFEST_PATH)
    assert not os.path.exists(server.MANIFEST_PATH)

    server.load_store()
    assert os.path.exists(server.MANIFEST_PATH)


def test_load_store_cleans_orphan_tempfiles_on_start(isolated_store, seed_consistent_state):
    server = isolated_store["server"]

    seed_consistent_state(server, [])

    tmp_path = isolated_store["tmp_path"]
    (tmp_path / "chunks.pkl.tmp").write_bytes(b"garbage")
    (tmp_path / "index.bin.new").write_bytes(b"garbage")

    server.load_store()

    assert not (tmp_path / "chunks.pkl.tmp").exists()
    assert not (tmp_path / "index.bin.new").exists()
