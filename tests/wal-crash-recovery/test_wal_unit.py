"""Tasks 8.1-8.2 — WAL encode/decode + iter_records behavior."""

from __future__ import annotations

import pytest

import wal


def test_encode_decode_roundtrip():
    rec = wal.make_record(seq=1, op="ingest", payload={"text": "hello", "source": "s"})
    raw = wal.encode_record(rec)
    assert raw.endswith(b"\n")
    decoded = wal.decode_line(raw)
    assert decoded.seq == 1
    assert decoded.op == "ingest"
    assert decoded.payload == {"text": "hello", "source": "s"}


def test_decode_detects_crc_tamper():
    rec = wal.make_record(seq=1, op="ingest", payload={"text": "hi", "source": "s"})
    raw = wal.encode_record(rec)
    # Flip one byte inside the JSON body (before the crc32 field) to tamper content
    tampered = bytearray(raw)
    # Replace the first 'h' in "hi" with 'X'
    idx = tampered.index(ord("h"), 10)  # skip past the field names
    tampered[idx] = ord("X")
    with pytest.raises(wal.WALCorruptError):
        wal.decode_line(bytes(tampered))


def test_decode_rejects_bad_json():
    with pytest.raises(wal.WALCorruptError):
        wal.decode_line(b"{not json}\n")


def test_iter_records_stops_at_corruption(tmp_path):
    wal_path = str(tmp_path / "wal.jsonl")
    good1 = wal.encode_record(wal.make_record(1, "ingest", {"text": "a", "source": "s1"}))
    good2 = wal.encode_record(wal.make_record(2, "ingest", {"text": "b", "source": "s2"}))
    bad = b'{"this is garbage not json\n'  # no closing brace
    wal.append(wal_path, good1)
    bad_start_offset = wal.append(wal_path, good2)
    # Append bad line manually bypassing encode
    with open(wal_path, "ab") as f:
        f.write(bad)

    results = list(wal.iter_records(wal_path, start_offset=0))
    # First two yields are valid; third yields corruption error then stops
    assert results[0][2] is not None  # record
    assert results[0][2].seq == 1
    assert results[1][2] is not None
    assert results[1][2].seq == 2
    assert results[2][2] is None
    assert results[2][3] is not None  # error
    assert results[2][3].offset == bad_start_offset
    assert len(results) == 3


def test_iter_records_stops_on_trailing_partial_line(tmp_path):
    wal_path = str(tmp_path / "wal.jsonl")
    good = wal.encode_record(wal.make_record(1, "ingest", {"text": "a", "source": "s1"}))
    partial = good[:-5]  # strip trailing bytes incl newline
    with open(wal_path, "wb") as f:
        f.write(partial)

    results = list(wal.iter_records(wal_path, start_offset=0))
    assert len(results) == 1
    assert results[0][2] is None
    assert results[0][3] is not None


def test_truncate_atomic_empties_file(tmp_path):
    wal_path = str(tmp_path / "wal.jsonl")
    good = wal.encode_record(wal.make_record(1, "ingest", {"text": "a", "source": "s"}))
    wal.append(wal_path, good)
    assert wal.file_size(wal_path) > 0
    wal.truncate_atomic(wal_path)
    assert wal.file_size(wal_path) == 0


def test_append_starts_at_zero_for_new_file(tmp_path):
    wal_path = str(tmp_path / "sub" / "wal.jsonl")
    good = wal.encode_record(wal.make_record(1, "ingest", {"text": "a", "source": "s"}))
    new_offset = wal.append(wal_path, good)
    assert new_offset == len(good)
