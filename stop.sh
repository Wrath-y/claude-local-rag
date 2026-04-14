#!/usr/bin/env bash

PID=$(lsof -ti tcp:8765 2>/dev/null)

if [ -z "$PID" ]; then
  echo "RAG 服务未在运行"
  exit 0
fi

kill "$PID"
echo "已停止 RAG 服务（PID $PID）"
