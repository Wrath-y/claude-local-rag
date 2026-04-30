"""Write-Ahead Log for crash-consistent write recovery.

Module layout note: named `wal.py` at repo root rather than `storage/wal.py`
because the `storage/` directory is used at runtime for data (manifest.json,
wal.jsonl), which would clash with a Python package of the same name.

Scope (change: wal-crash-recovery):
- append-only JSONL with per-line CRC32
- sequential offset-based iteration tolerant of tail corruption
- atomic truncation via tmp file + os.replace
- no replay logic here — server.py orchestrates replay under the write lock
"""

from __future__ import annotations

import json
import os
import zlib
from dataclasses import asdict, dataclass
from datetime import datetime, timezone
from typing import Any, Dict, Iterator, Optional, Tuple


# ================= Constants =================

WAL_FILENAME = "wal.jsonl"
WAL_TMP_SUFFIX = ".new"


def wal_path_for(data_dir: str) -> str:
    """Return absolute WAL path inside the given storage data dir."""
    return os.path.join(data_dir, WAL_FILENAME)


# ================= Record schema =================


@dataclass
class Record:
    seq: int
    ts: str
    op: str
    payload: Dict[str, Any]

    def to_dict(self) -> Dict[str, Any]:
        return {"seq": self.seq, "ts": self.ts, "op": self.op, "payload": self.payload}


def _now_iso() -> str:
    return datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def make_record(seq: int, op: str, payload: Dict[str, Any]) -> Record:
    return Record(seq=seq, ts=_now_iso(), op=op, payload=payload)


class WALCorruptError(Exception):
    """Raised when a WAL line fails CRC or JSON parsing. Carries the byte offset
    of the bad line so callers can enter read-only mode without continuing."""

    def __init__(self, message: str, offset: int):
        super().__init__(message)
        self.offset = offset


# ================= Encode / decode =================


def encode_record(record: Record) -> bytes:
    """Serialize one record as a single JSONL line ending in `\\n`.

    Format: JSON body (without `crc32`) → compute CRC32 over its UTF-8 bytes → append
    `crc32` field → re-serialize. Consumers decode by stripping `crc32`, recomputing
    over the rest, and comparing.
    """
    body = record.to_dict()
    body_json = json.dumps(body, ensure_ascii=False, sort_keys=True)
    crc = format(zlib.crc32(body_json.encode("utf-8")) & 0xFFFFFFFF, "08x")
    body["crc32"] = crc
    line = json.dumps(body, ensure_ascii=False, sort_keys=True) + "\n"
    return line.encode("utf-8")


def decode_line(raw: bytes, offset: int = 0) -> Record:
    """Parse one JSONL line into a Record, verifying CRC32.

    `offset` is only used to populate WALCorruptError for diagnostics.
    """
    try:
        line = raw.decode("utf-8").rstrip("\n")
        obj = json.loads(line)
    except (UnicodeDecodeError, json.JSONDecodeError) as e:
        raise WALCorruptError(f"line not valid JSON: {e}", offset=offset)

    if not isinstance(obj, dict) or "crc32" not in obj:
        raise WALCorruptError("missing crc32 field", offset=offset)

    claimed_crc = obj.pop("crc32")
    body_json = json.dumps(obj, ensure_ascii=False, sort_keys=True)
    actual_crc = format(zlib.crc32(body_json.encode("utf-8")) & 0xFFFFFFFF, "08x")
    if claimed_crc != actual_crc:
        raise WALCorruptError(
            f"crc32 mismatch: claimed={claimed_crc} actual={actual_crc}",
            offset=offset,
        )

    try:
        return Record(seq=obj["seq"], ts=obj["ts"], op=obj["op"], payload=obj["payload"])
    except KeyError as e:
        raise WALCorruptError(f"missing required field: {e}", offset=offset)


# ================= Append / iterate / truncate =================


def _fsync_dir(path: str) -> None:
    fd = os.open(path, os.O_RDONLY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def append(wal_path: str, record_bytes: bytes) -> int:
    """Append bytes to WAL with fsync on file and its parent directory.

    Returns the file size after append (== new "committed_offset" candidate).
    Creates the file with mode 0o644 if missing.
    """
    directory = os.path.dirname(os.path.abspath(wal_path)) or "."
    os.makedirs(directory, exist_ok=True)

    fd = os.open(wal_path, os.O_WRONLY | os.O_CREAT | os.O_APPEND, 0o644)
    try:
        with os.fdopen(fd, "ab") as f:
            f.write(record_bytes)
            f.flush()
            os.fsync(f.fileno())
    except Exception:
        raise
    _fsync_dir(directory)
    return os.path.getsize(wal_path)


def iter_records(
    wal_path: str, start_offset: int = 0
) -> Iterator[Tuple[int, int, Optional[Record], Optional[WALCorruptError]]]:
    """Yield (start_offset, end_offset, record, error) per line starting at
    byte `start_offset`.

    On a corrupt line, yields (bad_start, bad_start, None, err) and stops —
    callers MUST NOT read further bytes from the file for this session.
    """
    if not os.path.exists(wal_path):
        return
    size = os.path.getsize(wal_path)
    if start_offset >= size:
        return

    with open(wal_path, "rb") as f:
        f.seek(start_offset)
        cursor = start_offset
        while cursor < size:
            line_start = cursor
            line = f.readline()
            if not line:
                break
            cursor += len(line)
            if not line.endswith(b"\n"):
                # Truncated tail — treat as corruption so nothing past here is replayed.
                yield (line_start, line_start, None,
                       WALCorruptError("trailing partial line without newline",
                                       offset=line_start))
                return
            try:
                record = decode_line(line, offset=line_start)
            except WALCorruptError as err:
                yield (line_start, line_start, None, err)
                return
            yield (line_start, cursor, record, None)


def truncate_atomic(wal_path: str) -> None:
    """Replace the WAL with an empty file atomically."""
    directory = os.path.dirname(os.path.abspath(wal_path)) or "."
    os.makedirs(directory, exist_ok=True)
    tmp_path = wal_path + WAL_TMP_SUFFIX
    try:
        fd = os.open(tmp_path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o644)
        try:
            with os.fdopen(fd, "wb") as f:
                f.flush()
                os.fsync(f.fileno())
        except Exception:
            raise
        os.replace(tmp_path, wal_path)
        _fsync_dir(directory)
    except Exception:
        if os.path.exists(tmp_path):
            try:
                os.remove(tmp_path)
            except OSError:
                pass
        raise


def file_size(wal_path: str) -> int:
    return os.path.getsize(wal_path) if os.path.exists(wal_path) else 0
