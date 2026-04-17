#!/usr/bin/env bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# 读取 config.yaml 中的日志语言，默认 zh
LANG_CODE=$(grep -A1 '^log:' "$SCRIPT_DIR/config.yaml" 2>/dev/null \
  | grep 'lang:' \
  | sed "s/.*lang:.*[\"']\([a-z]*\)[\"'].*/\1/" \
  | tr -d '[:space:]')
LANG_CODE=${LANG_CODE:-zh}

# ── 消息定义 ──────────────────────────────────────────────
if [ "$LANG_CODE" = "en" ]; then
  M_CHECK_DEPS="[1/5] Checking dependencies..."
  M_NO_PYTHON="Error: python3 not found. Install Python 3.8+"
  M_NO_NODE="  ⚠️  Node.js not detected (optional, for Feishu): https://nodejs.org"
  M_INSTALL_DEPS="  Installing Python dependencies..."
  M_DEPS_READY="  Python dependencies ready"
  M_CHECK_LARK="[2/5] Checking Feishu CLI..."
  M_NO_LARK="  ⚠️  lark-cli not found (/rag Feishu ingestion unavailable)"
  M_LARK_GUIDE="  To enable Feishu ingestion, see: https://www.feishu.cn/content/article/7623291503305083853"
  M_LARK_OK="  lark-cli installed"
  M_START_SVC="[5/5] Starting RAG service..."
  M_SVC_RUNNING="  Service already running, skipping"
  M_LOADING="  Loading embedding model, please wait..."
  M_SVC_OK="  Service started (%ds) → http://127.0.0.1:8765"
  M_SVC_EXITED="  Service process exited. Check logs: tail -f /tmp/claude-local-rag.log"
  M_WAITING="  Waiting... %ds"
  M_TIMEOUT="  Timeout (%ds). Check logs: tail -f /tmp/claude-local-rag.log"
  M_DONE="Setup complete! Restart Claude Code to start using it."
  M_USAGE="  /rag <content or URL>   — ingest into vector store"
  M_LOG="  Logs: tail -f /tmp/claude-local-rag.log"
else
  M_CHECK_DEPS="[1/5] 检查依赖..."
  M_NO_PYTHON="错误：未找到 python3，请先安装 Python 3.8+"
  M_NO_NODE="  ⚠️  未检测到 Node.js（飞书 CLI 依赖，可选）：https://nodejs.org"
  M_INSTALL_DEPS="  安装 Python 依赖..."
  M_DEPS_READY="  Python 依赖已就绪"
  M_CHECK_LARK="[2/5] 检查飞书 CLI..."
  M_NO_LARK="  ⚠️  未检测到 lark-cli（/rag 飞书文档入库功能不可用）"
  M_LARK_GUIDE="  如需使用飞书文档入库，请参考：https://www.feishu.cn/content/article/7623291503305083853"
  M_LARK_OK="  lark-cli 已安装"
  M_START_SVC="[5/5] 启动 RAG 服务（首次）..."
  M_SVC_RUNNING="  服务已在运行，跳过启动"
  M_LOADING="  正在加载 embedding 模型，请稍候..."
  M_SVC_OK="  服务启动成功（%ds）→ http://127.0.0.1:8765"
  M_SVC_EXITED="  服务进程已退出，请查看日志：tail -f /tmp/claude-local-rag.log"
  M_WAITING="  等待中... %ds"
  M_TIMEOUT="  超时（%ds），请查看日志：tail -f /tmp/claude-local-rag.log"
  M_DONE="安装完成！重启 Claude Code 后即可开箱即用。"
  M_USAGE="  /rag <内容或飞书链接>   — 存入向量库"
  M_LOG="  日志：tail -f /tmp/claude-local-rag.log"
fi
# ─────────────────────────────────────────────────────────

echo "$M_CHECK_DEPS"

if ! command -v python3 &>/dev/null; then
  echo "$M_NO_PYTHON" && exit 1
fi

if ! command -v node &>/dev/null; then
  echo "$M_NO_NODE"
fi

if ! python3 -c "import uvicorn" &>/dev/null; then
  echo "$M_INSTALL_DEPS"
  pip install -r "$SCRIPT_DIR/requirements.txt" -q
else
  echo "$M_DEPS_READY"
fi

echo "$M_CHECK_LARK"
if ! command -v lark-cli &>/dev/null; then
  echo "$M_NO_LARK"
  echo "$M_LARK_GUIDE"
else
  echo "$M_LARK_OK"
fi

python3 "$SCRIPT_DIR/setup_hook.py"

echo "$M_START_SVC"
if curl -s http://127.0.0.1:8765/health > /dev/null 2>&1; then
  echo "$M_SVC_RUNNING"
else
  cd "$SCRIPT_DIR"
  nohup uvicorn server:app --port 8765 >> /tmp/claude-local-rag.log 2>&1 &
  SERVER_PID=$!

  echo "$M_LOADING"
  MAX_WAIT=120
  ELAPSED=0
  while [ $ELAPSED -lt $MAX_WAIT ]; do
    if curl -s http://127.0.0.1:8765/health > /dev/null 2>&1; then
      printf "$M_SVC_OK\n" $ELAPSED
      break
    fi
    if ! kill -0 $SERVER_PID 2>/dev/null; then
      echo "$M_SVC_EXITED"
      exit 1
    fi
    printf "$M_WAITING\r" $ELAPSED
    sleep 2
    ELAPSED=$((ELAPSED + 2))
  done

  if [ $ELAPSED -ge $MAX_WAIT ]; then
    printf "$M_TIMEOUT\n" $MAX_WAIT
    exit 1
  fi
fi

echo ""
echo "$M_DONE"
echo "$M_USAGE"
echo "$M_LOG"
