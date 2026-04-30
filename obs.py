"""Structured-event logging helper.

Emits a single JSON line per call to stdout so downstream log pipelines can parse
events without regex-matching free-form text. Intentionally minimal: no logger
hierarchy, no file destinations.
"""

from __future__ import annotations

import json
import sys
from datetime import datetime, timezone
from typing import Any


def _now_iso() -> str:
    return datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%S.%fZ")


def structured_log(event: str, **kv: Any) -> None:
    payload = {"ts": _now_iso(), "event": event}
    payload.update(kv)
    # Use separators to keep output compact and stable for downstream parsers.
    sys.stdout.write(json.dumps(payload, ensure_ascii=False, separators=(",", ":")) + "\n")
    sys.stdout.flush()
