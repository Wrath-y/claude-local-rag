#!/usr/bin/env bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "[1/5] 检查依赖..."

if ! command -v python3 &>/dev/null; then
  echo "错误：未找到 python3，请先安装 Python 3.8+" && exit 1
fi

if ! command -v node &>/dev/null; then
  echo "  ⚠️  未检测到 Node.js（飞书 CLI 依赖，可选）：https://nodejs.org"
fi

if ! python3 -c "import uvicorn" &>/dev/null; then
  echo "  安装 Python 依赖..."
  pip install -r "$SCRIPT_DIR/requirements.txt" -q
else
  echo "  Python 依赖已就绪"
fi

echo "[2/5] 检查飞书 CLI..."
if ! command -v lark-cli &>/dev/null; then
  echo "  ⚠️  未检测到 lark-cli（/rag 飞书文档入库功能不可用）"
  echo "  如需使用飞书文档入库，请参考以下文档完成安装："
  echo "  👉 https://www.feishu.cn/content/article/7623291503305083853"
else
  echo "  lark-cli 已安装"
fi

python3 "$SCRIPT_DIR/setup_hook.py"

echo "[5/5] 启动 RAG 服务（首次）..."
if curl -s http://127.0.0.1:8765/health > /dev/null 2>&1; then
  echo "  服务已在运行，跳过启动"
else
  cd "$SCRIPT_DIR"
  nohup uvicorn server:app --port 8765 >> /tmp/claude-local-rag.log 2>&1 &
  SERVER_PID=$!

  echo "  正在加载 embedding 模型，请稍候..."
  MAX_WAIT=120
  ELAPSED=0
  while [ $ELAPSED -lt $MAX_WAIT ]; do
    if curl -s http://127.0.0.1:8765/health > /dev/null 2>&1; then
      echo "  服务启动成功（${ELAPSED}s）→ http://127.0.0.1:8765"
      break
    fi
    if ! kill -0 $SERVER_PID 2>/dev/null; then
      echo "  服务进程已退出，请查看日志：tail -f /tmp/claude-local-rag.log"
      exit 1
    fi
    printf "  等待中... %ds\r" $ELAPSED
    sleep 2
    ELAPSED=$((ELAPSED + 2))
  done

  if [ $ELAPSED -ge $MAX_WAIT ]; then
    echo "  超时（${MAX_WAIT}s），请查看日志：tail -f /tmp/claude-local-rag.log"
    exit 1
  fi
fi

echo ""
echo "安装完成！重启 Claude Code 后即可开箱即用。"
echo "  /rag <内容或飞书链接>   — 存入向量库"
echo "  日志：tail -f /tmp/claude-local-rag.log"
