@echo off
setlocal EnableDelayedExpansion
set SCRIPT_DIR=%~dp0
set SCRIPT_DIR=%SCRIPT_DIR:~0,-1%

rem ── 读取 config.yaml 中的日志语言，默认 zh ──────────────────
set LANG_CODE=zh
for /f "usebackq tokens=*" %%i in (`findstr /r "lang:" "%SCRIPT_DIR%\config.yaml" 2^>nul`) do (
  set _RAW=%%i
  set _RAW=!_RAW: =!
  set _RAW=!_RAW:"=!
  set _RAW=!_RAW:'=!
  for /f "tokens=2 delims=:" %%j in ("!_RAW!") do set LANG_CODE=%%j
)

rem ── 消息定义 ─────────────────────────────────────────────────
if "%LANG_CODE%"=="en" (
  set M_CHECK_DEPS=[1/5] Checking dependencies...
  set M_NO_PYTHON=Error: python not found. Install Python 3.8+  https://www.python.org/downloads/
  set M_NO_NODE=   Warning: Node.js not detected (optional, for Feishu): https://nodejs.org
  set M_INSTALL_DEPS=  Installing Python dependencies...
  set M_DEPS_READY=  Python dependencies ready
  set M_CHECK_LARK=[2/5] Checking Feishu CLI...
  set M_NO_LARK=  Warning: lark-cli not found (/rag Feishu ingestion unavailable)
  set M_LARK_GUIDE=  To enable Feishu ingestion, see: https://www.feishu.cn/content/article/7623291503305083853
  set M_LARK_OK=  lark-cli installed
  set M_START_SVC=[5/5] Starting RAG service...
  set M_SVC_RUNNING=  Service already running, skipping
  set M_LOG_PATH=  Log path:
  set M_LOADING=  Loading embedding model, please wait...
  set M_WAITING=  Waiting...
  set M_SVC_OK=  Service started
  set M_TIMEOUT=  Timeout (120s). Check logs: type
  set M_DONE=Setup complete! Restart Claude Code to start using it.
  set M_USAGE=  /rag ^<content or URL^>   -- ingest into vector store
  set M_LOG=  Logs: type "%TEMP%\claude-local-rag.log"
) else (
  set M_CHECK_DEPS=[1/5] 检查依赖...
  set M_NO_PYTHON=错误：未找到 python，请先安装 Python 3.8+  https://www.python.org/downloads/
  set M_NO_NODE=  ⚠  未检测到 Node.js（飞书 CLI 依赖，可选）：https://nodejs.org
  set M_INSTALL_DEPS=  安装 Python 依赖...
  set M_DEPS_READY=  Python 依赖已就绪
  set M_CHECK_LARK=[2/5] 检查飞书 CLI...
  set M_NO_LARK=  ⚠  未检测到 lark-cli（/rag 飞书文档入库功能不可用）
  set M_LARK_GUIDE=  如需使用飞书文档入库，请参考：https://www.feishu.cn/content/article/7623291503305083853
  set M_LARK_OK=  lark-cli 已安装
  set M_START_SVC=[5/5] 启动 RAG 服务（首次）...
  set M_SVC_RUNNING=  服务已在运行，跳过启动
  set M_LOG_PATH=  日志路径：
  set M_LOADING=  正在加载 embedding 模型，请稍候...
  set M_WAITING=  等待中...
  set M_SVC_OK=  服务启动成功
  set M_TIMEOUT=  超时（120s），请查看日志：type
  set M_DONE=安装完成！重启 Claude Code 后即可开箱即用。
  set M_USAGE=  /rag ^<内容或飞书链接^>   — 存入向量库
  set M_LOG=  日志：type "%TEMP%\claude-local-rag.log"
)
rem ─────────────────────────────────────────────────────────────

echo %M_CHECK_DEPS%

where python >nul 2>&1
if errorlevel 1 (
  echo %M_NO_PYTHON%
  exit /b 1
)

where node >nul 2>&1
if errorlevel 1 (
  echo %M_NO_NODE%
)

python -c "import uvicorn" >nul 2>&1
if errorlevel 1 (
  echo %M_INSTALL_DEPS%
  pip install -r "%SCRIPT_DIR%\requirements.txt" -q
) else (
  echo %M_DEPS_READY%
)

echo %M_CHECK_LARK%
where lark-cli >nul 2>&1
if errorlevel 1 (
  echo %M_NO_LARK%
  echo %M_LARK_GUIDE%
) else (
  echo %M_LARK_OK%
)

python "%SCRIPT_DIR%\setup_hook.py"

echo %M_START_SVC%

curl -s http://127.0.0.1:8765/health >nul 2>&1
if not errorlevel 1 (
  echo %M_SVC_RUNNING%
  goto :done
)

set LOG=%TEMP%\claude-local-rag.log
echo %M_LOG_PATH% %LOG%
start /B python -m uvicorn server:app --app-dir "%SCRIPT_DIR%" --port 8765 >> "%LOG%" 2>&1

echo %M_LOADING%
set ELAPSED=0
:wait_loop
timeout /t 2 /nobreak >nul
set /a ELAPSED+=2
curl -s http://127.0.0.1:8765/health >nul 2>&1
if not errorlevel 1 (
  echo %M_SVC_OK% ^(%ELAPSED%s^) ^> http://127.0.0.1:8765
  goto :done
)
if %ELAPSED% geq 120 (
  echo %M_TIMEOUT% "%LOG%"
  exit /b 1
)
echo %M_WAITING% %ELAPSED%s
goto :wait_loop

:done
echo.
echo %M_DONE%
echo %M_USAGE%
echo %M_LOG%
