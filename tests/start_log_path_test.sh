#!/bin/bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
START_SCRIPT="$REPO_ROOT/start.sh"
WINDOWS_START_SCRIPT="$REPO_ROOT/start.bat"

if ! grep -Fq 'LOG_DIR="$SCRIPT_DIR/logs"' "$START_SCRIPT"; then
    echo "expected root logs directory configuration"
    exit 1
fi

if ! grep -Fq 'LOG_FILE="$LOG_DIR/rag-server-$(date +%Y%m%d).log"' "$START_SCRIPT"; then
    echo "expected daily rag-server filename"
    exit 1
fi

if ! grep -Fq './rag-server >>"$LOG_FILE" 2>&1 &' "$START_SCRIPT"; then
    echo "expected stdout and stderr append redirection to daily log"
    exit 1
fi

if grep -Fq '.run/rag-server.log' "$START_SCRIPT"; then
    echo "legacy .run log path remains"
    exit 1
fi

if ! grep -Fq 'set "LOG_FILE=!CD!\logs\rag-server-!LOG_DATE!.log"' "$WINDOWS_START_SCRIPT"; then
    echo "expected Windows daily rag-server filename"
    exit 1
fi

if ! grep -Fq 'start /b "" rag-server.exe >>"!LOG_FILE!" 2>&1' "$WINDOWS_START_SCRIPT"; then
    echo "expected Windows stdout and stderr append redirection to daily log"
    exit 1
fi

if ! grep -Fq 'set "PATH=!VENV_SCRIPTS!;!PATH!"' "$WINDOWS_START_SCRIPT"; then
    echo "expected Windows local sidecar to inherit the venv PATH"
    exit 1
fi
