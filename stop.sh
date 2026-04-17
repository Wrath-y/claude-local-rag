#!/usr/bin/env bash

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# 读取 config.yaml 中的日志语言，默认 zh
LANG_CODE=$(grep -A1 '^log:' "$SCRIPT_DIR/config.yaml" 2>/dev/null \
  | grep 'lang:' \
  | sed "s/.*lang:.*[\"']\([a-z]*\)[\"'].*/\1/" \
  | tr -d '[:space:]')
LANG_CODE=${LANG_CODE:-zh}

if [ "$LANG_CODE" = "en" ]; then
  M_NOT_RUNNING="RAG service is not running"
  M_STOPPED="RAG service stopped (PID %s)"
else
  M_NOT_RUNNING="RAG 服务未在运行"
  M_STOPPED="已停止 RAG 服务（PID %s）"
fi

PID=$(lsof -ti tcp:8765 2>/dev/null)

if [ -z "$PID" ]; then
  echo "$M_NOT_RUNNING"
  exit 0
fi

kill "$PID"
printf "$M_STOPPED\n" "$PID"
