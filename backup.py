"""Backup / restore primitives: zip packaging, retention pruning, minimal cron.

Scope (change: backup-restore-automation):
- make_backup / restore_from — zip pack/unpack of the 4 files (chunks/index/manifest/wal)
- list_backups — enumerate backups/ tree sorted by mtime desc
- prune — bucket-based retention (days / weeks), skips pre-restore-*.zip
- parse_cron — micro cron: supports "M H * * *" and "*/N" in the minute field
"""

from __future__ import annotations

import os
import re
import time
import zipfile
from dataclasses import dataclass
from datetime import datetime, timedelta
from typing import Callable, Iterable, List, Optional


# ================= Pack / unpack =================


@dataclass
class MemberSpec:
    arcname: str          # path inside the zip
    abs_path: str         # path on disk to read from (for make_backup) / write to (for restore)


def make_backup(members: Iterable[MemberSpec], dst_zip: str) -> int:
    """Pack listed files into dst_zip (DEFLATE). Returns final zip size in bytes.

    Only existing files are written; missing files are silently skipped (WAL may
    legitimately be absent). Destination directory is created if needed.
    """
    os.makedirs(os.path.dirname(os.path.abspath(dst_zip)) or ".", exist_ok=True)
    tmp = dst_zip + ".tmp"
    try:
        with zipfile.ZipFile(tmp, "w", zipfile.ZIP_DEFLATED) as zf:
            for m in members:
                if os.path.exists(m.abs_path):
                    zf.write(m.abs_path, m.arcname)
        os.replace(tmp, dst_zip)
    except Exception:
        if os.path.exists(tmp):
            try:
                os.remove(tmp)
            except OSError:
                pass
        raise
    return os.path.getsize(dst_zip)


def restore_from(zip_path: str, staging_dir: str) -> List[str]:
    """Extract zip_path into staging_dir. Returns list of relative file names extracted."""
    os.makedirs(staging_dir, exist_ok=True)
    extracted: List[str] = []
    with zipfile.ZipFile(zip_path, "r") as zf:
        for name in zf.namelist():
            # Guard against zip-slip
            target = os.path.abspath(os.path.join(staging_dir, name))
            if not target.startswith(os.path.abspath(staging_dir) + os.sep) and target != os.path.abspath(staging_dir):
                raise RuntimeError(f"zip entry escapes staging dir: {name}")
            os.makedirs(os.path.dirname(target), exist_ok=True)
            with zf.open(name) as src, open(target, "wb") as dst:
                dst.write(src.read())
            extracted.append(name)
    return extracted


# ================= Listing / pruning =================


def list_backups(backups_dir: str) -> List[dict]:
    """List all .zip under backups_dir (recursive). Returns dicts sorted by mtime desc."""
    if not os.path.isdir(backups_dir):
        return []
    results = []
    for root, _, files in os.walk(backups_dir):
        for fn in files:
            if fn.endswith(".zip"):
                full = os.path.join(root, fn)
                st = os.stat(full)
                results.append({
                    "path": full,
                    "size_bytes": st.st_size,
                    "modified_at": int(st.st_mtime),
                })
    results.sort(key=lambda x: x["modified_at"], reverse=True)
    return results


def prune(backups_dir: str, days: int, weeks: int) -> List[str]:
    """Keep newest per-day for last `days` days + newest per-week for last `weeks` weeks.

    Protects backups/pre-restore-*.zip from deletion regardless of age. Returns list
    of deleted file paths.
    """
    entries = [e for e in list_backups(backups_dir)
               if not os.path.basename(e["path"]).startswith("pre-restore-")]
    now = time.time()

    keep_day: dict = {}
    keep_week: dict = {}
    day_cutoff = now - days * 86400
    week_cutoff = now - weeks * 7 * 86400

    for e in entries:
        dt = datetime.fromtimestamp(e["modified_at"])
        day_key = dt.strftime("%Y-%m-%d")
        week_key = f"{dt.isocalendar()[0]}-W{dt.isocalendar()[1]}"
        if e["modified_at"] >= day_cutoff:
            existing = keep_day.get(day_key)
            if existing is None or e["modified_at"] > existing["modified_at"]:
                keep_day[day_key] = e
        elif e["modified_at"] >= week_cutoff:
            existing = keep_week.get(week_key)
            if existing is None or e["modified_at"] > existing["modified_at"]:
                keep_week[week_key] = e

    keep_paths = {e["path"] for e in list(keep_day.values()) + list(keep_week.values())}
    deleted: List[str] = []
    for e in entries:
        if e["path"] not in keep_paths:
            try:
                os.remove(e["path"])
                deleted.append(e["path"])
            except OSError:
                pass
    return deleted


# ================= Cron =================


_CRON_RE = re.compile(r"^\s*(\S+)\s+(\S+)\s+(\S+)\s+(\S+)\s+(\S+)\s*$")


def parse_cron(expr: str) -> Callable[[float], float]:
    """Return a function that, given a unix timestamp, returns the next fire time.

    Supports: integer literal, "*", and "*/N" in each field.
    Day-of-month / month / day-of-week fields accept only "*" or integer for
    simplicity — if the user wants richer schedules they can pick a longer
    dependency.
    """
    m = _CRON_RE.match(expr)
    if not m:
        raise ValueError(f"unsupported cron expression: {expr!r}")
    minute_f, hour_f, dom_f, mon_f, dow_f = m.groups()

    def matches(value: int, field: str, rng: range) -> bool:
        if field == "*":
            return True
        if field.startswith("*/"):
            try:
                step = int(field[2:])
                return step > 0 and (value - rng.start) % step == 0
            except ValueError:
                return False
        try:
            return value == int(field)
        except ValueError:
            return False

    def next_fire(from_ts: float) -> float:
        # Brute force: step minute-by-minute up to 366 days; simple + correct.
        cursor = datetime.fromtimestamp(from_ts).replace(second=0, microsecond=0) + timedelta(minutes=1)
        for _ in range(366 * 24 * 60):
            if (matches(cursor.minute, minute_f, range(0, 60))
                    and matches(cursor.hour, hour_f, range(0, 24))
                    and matches(cursor.day, dom_f, range(1, 32))
                    and matches(cursor.month, mon_f, range(1, 13))
                    and matches(cursor.weekday(), dow_f, range(0, 7))):
                return cursor.timestamp()
            cursor += timedelta(minutes=1)
        raise RuntimeError(f"no match found in 1 year for cron {expr!r}")

    return next_fire
