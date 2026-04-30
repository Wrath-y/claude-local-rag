"""Unit tests for backup module (no server import required)."""

from __future__ import annotations

import os
import time
import zipfile

import backup


def test_make_backup_packs_only_existing(tmp_path):
    f1 = tmp_path / "a.bin"
    f1.write_bytes(b"hello")
    f2_missing = tmp_path / "never.bin"  # intentionally absent
    dst = str(tmp_path / "out.zip")

    size = backup.make_backup(
        [
            backup.MemberSpec(arcname="a.bin", abs_path=str(f1)),
            backup.MemberSpec(arcname="never.bin", abs_path=str(f2_missing)),
        ],
        dst,
    )

    assert os.path.exists(dst)
    assert size > 0
    with zipfile.ZipFile(dst) as zf:
        assert zf.namelist() == ["a.bin"]


def test_restore_from_extracts_to_staging(tmp_path):
    src = tmp_path / "a.bin"
    src.write_bytes(b"content")
    zip_path = str(tmp_path / "pack.zip")
    backup.make_backup([backup.MemberSpec(arcname="a.bin", abs_path=str(src))], zip_path)

    staging = tmp_path / "staging"
    extracted = backup.restore_from(zip_path, str(staging))
    assert extracted == ["a.bin"]
    assert (staging / "a.bin").read_bytes() == b"content"


def _touch(path, age_days):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text("x")
    ts = time.time() - age_days * 86400
    os.utime(path, (ts, ts))


def test_prune_keeps_recent_days_and_weeks(tmp_path):
    # 12 backups: one per day for 12 days
    for d in range(12):
        _touch(tmp_path / f"rag-{d:02d}.zip", age_days=d)
    # Plus one pre-restore that's 200 days old — must NOT be pruned
    _touch(tmp_path / "pre-restore-old.zip", age_days=200)

    deleted = backup.prune(str(tmp_path), days=3, weeks=1)

    remaining = sorted(os.listdir(tmp_path))
    # pre-restore always kept
    assert "pre-restore-old.zip" in remaining
    # Must have deleted the bulk (started with 12, retention target is small)
    assert len(deleted) >= 7
    # And the total kept non-pre must be <= days + 2*weeks (ISO week boundary can span two weeks)
    non_pre = [f for f in remaining if not f.startswith("pre-restore-")]
    assert len(non_pre) <= 3 + 2 * 1


def test_parse_cron_specific_time(tmp_path):
    next_fire = backup.parse_cron("0 3 * * *")
    # From 2026-01-01 00:00:00 UTC local (the test just requires monotonic next-match)
    import datetime as dt
    from_ts = dt.datetime(2026, 1, 1, 0, 0, 0).timestamp()
    next_ts = next_fire(from_ts)
    next_dt = dt.datetime.fromtimestamp(next_ts)
    assert next_dt.hour == 3
    assert next_dt.minute == 0
    assert next_dt.second == 0


def test_parse_cron_step_minute():
    next_fire = backup.parse_cron("*/15 * * * *")
    import datetime as dt
    from_ts = dt.datetime(2026, 1, 1, 12, 7, 30).timestamp()
    next_ts = next_fire(from_ts)
    next_dt = dt.datetime.fromtimestamp(next_ts)
    assert next_dt.minute in (15, 0, 30, 45)
