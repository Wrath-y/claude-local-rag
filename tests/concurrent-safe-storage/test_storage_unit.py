"""Unit tests for the storage module (batches 1-2 of concurrent-safe-storage)."""

from __future__ import annotations

import json
import os
import pickle
from unittest.mock import patch

import pytest

import storage


# ---------- atomic_write_bytes ----------


def test_atomic_write_creates_file(tmp_path):
    target = tmp_path / "a.bin"
    storage.atomic_write_bytes(str(target), b"hello")
    assert target.read_bytes() == b"hello"


def test_atomic_write_overwrites_existing(tmp_path):
    target = tmp_path / "a.bin"
    target.write_bytes(b"old")
    storage.atomic_write_bytes(str(target), b"new")
    assert target.read_bytes() == b"new"


def test_atomic_write_leaves_no_tmp(tmp_path):
    target = tmp_path / "a.bin"
    storage.atomic_write_bytes(str(target), b"x")
    assert not (tmp_path / "a.bin.tmp").exists()


def test_atomic_write_failure_preserves_original(tmp_path):
    target = tmp_path / "a.bin"
    target.write_bytes(b"original")

    original_replace = os.replace

    def boom(src, dst):
        raise OSError("simulated replace failure")

    with patch("storage.os.replace", side_effect=boom):
        with pytest.raises(OSError):
            storage.atomic_write_bytes(str(target), b"new")

    assert target.read_bytes() == b"original"
    assert not (tmp_path / "a.bin.tmp").exists()
    # Sanity: confirm patched symbol restored
    assert os.replace is original_replace


# ---------- sha256_of_file / cleanup_orphan_tempfiles ----------


def test_sha256_stable(tmp_path):
    f = tmp_path / "f.bin"
    f.write_bytes(b"abc")
    h1 = storage.sha256_of_file(str(f))
    h2 = storage.sha256_of_file(str(f))
    assert h1 == h2
    assert len(h1) == 64


def test_cleanup_orphan_tempfiles_removes_only_suffixed(tmp_path):
    (tmp_path / "chunks.pkl").write_bytes(b"real")
    (tmp_path / "chunks.pkl.tmp").write_bytes(b"orphan1")
    (tmp_path / "index.bin.new").write_bytes(b"orphan2")
    (tmp_path / "keep.txt").write_bytes(b"keep")

    removed = storage.cleanup_orphan_tempfiles(str(tmp_path))

    assert set(os.path.basename(p) for p in removed) == {"chunks.pkl.tmp", "index.bin.new"}
    assert (tmp_path / "chunks.pkl").exists()
    assert (tmp_path / "keep.txt").exists()


def test_cleanup_orphan_nonexistent_dir(tmp_path):
    missing = tmp_path / "nope"
    assert storage.cleanup_orphan_tempfiles(str(missing)) == []


# ---------- Manifest read/write roundtrip ----------


def _make_manifest():
    return storage.ManifestV1(
        version=storage.MANIFEST_VERSION,
        committed_at="2026-04-30T00:00:00Z",
        chunks=storage.ChunksSummary(path="chunks.pkl", count=3, sha256="c" * 64),
        index=storage.IndexSummary(path="index.bin", dim=16, ntotal=3, sha256="i" * 64),
    )


def test_manifest_roundtrip(tmp_path):
    p = tmp_path / "manifest.json"
    m = _make_manifest()
    storage.write_manifest(str(p), m)

    loaded = storage.read_manifest(str(p))
    assert loaded is not None
    assert loaded.chunks.count == 3
    assert loaded.index.dim == 16
    assert loaded.chunks.sha256 == "c" * 64


def test_read_manifest_missing_returns_none(tmp_path):
    assert storage.read_manifest(str(tmp_path / "nope.json")) is None


def test_read_manifest_wrong_version_returns_none(tmp_path):
    p = tmp_path / "manifest.json"
    p.write_text(json.dumps({"version": 999, "committed_at": "x",
                              "chunks": {}, "index": {}}))
    assert storage.read_manifest(str(p)) is None


def test_read_manifest_invalid_json_returns_none(tmp_path):
    p = tmp_path / "manifest.json"
    p.write_text("not json at all {")
    assert storage.read_manifest(str(p)) is None


# ---------- verify_manifest ----------


class _FakeIndex:
    def __init__(self, dim, ntotal):
        self.d = dim
        self.ntotal = ntotal


def test_verify_manifest_all_consistent(tmp_path):
    chunks_path = tmp_path / "chunks.pkl"
    index_path = tmp_path / "index.bin"
    chunks_path.write_bytes(pickle.dumps(["a", "b", "c"]))
    index_path.write_bytes(b"fake-index-bytes")

    idx = _FakeIndex(dim=16, ntotal=3)
    manifest = storage.build_manifest_from_files(
        str(chunks_path), str(index_path), chunks_count=3, index_obj=idx
    )

    mismatches = storage.verify_manifest(
        manifest, str(chunks_path), 3, str(index_path), idx
    )
    assert mismatches == []


def test_verify_manifest_detects_count_mismatch(tmp_path):
    chunks_path = tmp_path / "chunks.pkl"
    index_path = tmp_path / "index.bin"
    chunks_path.write_bytes(pickle.dumps(["a", "b", "c"]))
    index_path.write_bytes(b"data")

    idx = _FakeIndex(dim=8, ntotal=3)
    manifest = storage.build_manifest_from_files(
        str(chunks_path), str(index_path), chunks_count=3, index_obj=idx
    )

    # Simulate chunks file grew but manifest not updated
    chunks_path.write_bytes(pickle.dumps(["a", "b", "c", "d"]))

    mismatches = storage.verify_manifest(
        manifest, str(chunks_path), 4, str(index_path), idx
    )
    fields = {m.field for m in mismatches}
    assert "chunks.count" in fields
    assert "chunks.sha256" in fields
