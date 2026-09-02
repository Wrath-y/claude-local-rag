#!/bin/bash
set -e
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

# Build Go binary
if ! command -v go &>/dev/null; then
    echo "Error: Go not installed. Get it from https://go.dev/dl/"
    exit 1
fi

if [ ! -f rag-server ] || [ go.mod -nt rag-server ]; then
    echo "Building rag-server..."
    go build -o rag-server ./cmd/server/
fi

# Setup Python sidecar if local provider
PROVIDER=$(awk '
  /^embedding:/ { in_embedding=1; next }
  in_embedding && /^[^[:space:]]/ { exit }
  in_embedding && /^[[:space:]]+provider:/ { gsub(/"/, "", $2); print $2; exit }
' config.yaml 2>/dev/null)
PROVIDER=${PROVIDER:-local}
VENV_BIN=""
if [ "$PROVIDER" = "local" ]; then
    if ! command -v python3 &>/dev/null; then
        echo "Error: Python3 required for local embedding."
        exit 1
    fi
    if [ ! -d sidecar/.venv ]; then
        echo "Setting up Python sidecar..."
        python3 -m venv sidecar/.venv
        sidecar/.venv/bin/pip install -q -r sidecar/requirements.txt
    fi
    VENV_BIN="$SCRIPT_DIR/sidecar/.venv/bin"
fi

# Stop existing
if [ -f .rag-server.pid ]; then
    kill "$(cat .rag-server.pid)" 2>/dev/null || true
    rm -f .rag-server.pid
fi

# Ensure daily server logs are captured under the repository root.
LOG_DIR="$SCRIPT_DIR/logs"
LOG_FILE="$LOG_DIR/rag-server-$(date +%Y%m%d).log"
mkdir -p "$LOG_DIR"

# Start
if [ -n "$VENV_BIN" ]; then
    PATH="$VENV_BIN:$PATH" ./rag-server >>"$LOG_FILE" 2>&1 &
else
    ./rag-server >>"$LOG_FILE" 2>&1 &
fi
PID=$!
echo "$PID" > .rag-server.pid

# Wait for health
for i in $(seq 1 30); do
    if curl -s http://127.0.0.1:8765/health >/dev/null 2>&1; then
        echo "RAG server started (PID: $PID) at http://127.0.0.1:8765"
        exit 0
    fi
    sleep 1
done
echo "Warning: server started but health check not responding yet"
