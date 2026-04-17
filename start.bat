@echo off
setlocal EnableDelayedExpansion
set SCRIPT_DIR=%~dp0
set SCRIPT_DIR=%SCRIPT_DIR:~0,-1%

echo [1/5] 检查依赖...

where python >nul 2>&1
if errorlevel 1 (
  echo 错误：未找到 python，请先安装 Python 3.8+  https://www.python.org/downloads/
  exit /b 1
)

where node >nul 2>&1
if errorlevel 1 (
  echo   ⚠  未检测到 Node.js（飞书 CLI 依赖，可选）：https://nodejs.org
)

python -c "import uvicorn" >nul 2>&1
if errorlevel 1 (
  echo   安装 Python 依赖...
  pip install -r "%SCRIPT_DIR%\requirements.txt" -q
) else (
  echo   Python 依赖已就绪
)

echo [2/5] 检查飞书 CLI...
where lark-cli >nul 2>&1
if errorlevel 1 (
  echo   ⚠  未检测到 lark-cli（/rag 飞书文档入库功能不可用）
  echo   如需使用飞书文档入库，请参考：https://www.feishu.cn/content/article/7623291503305083853
) else (
  echo   lark-cli 已安装
)

python "%SCRIPT_DIR%\setup_hook.py"

echo [5/5] 启动 RAG 服务（首次）...

curl -s http://127.0.0.1:8765/health >nul 2>&1
if not errorlevel 1 (
  echo   服务已在运行，跳过启动
  goto :done
)

set LOG=%TEMP%\claude-local-rag.log
echo   日志路径：%LOG%
start /B python -m uvicorn server:app --app-dir "%SCRIPT_DIR%" --port 8765 >> "%LOG%" 2>&1

echo   正在加载 embedding 模型，请稍候...
set ELAPSED=0
:wait_loop
timeout /t 2 /nobreak >nul
set /a ELAPSED+=2
curl -s http://127.0.0.1:8765/health >nul 2>&1
if not errorlevel 1 (
  echo   服务启动成功（%ELAPSED%s） ^> http://127.0.0.1:8765
  goto :done
)
if %ELAPSED% geq 120 (
  echo   超时（120s），请查看日志：type "%LOG%"
  exit /b 1
)
echo   等待中... %ELAPSED%s
goto :wait_loop

:done
echo.
echo 安装完成！重启 Claude Code 后即可开箱即用。
echo   /rag ^<内容或飞书链接^>   — 存入向量库
echo   日志：type "%TEMP%\claude-local-rag.log"
