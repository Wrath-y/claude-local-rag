@echo off
for /f "tokens=5" %%a in ('netstat -ano ^| findstr ":8765 " ^| findstr "LISTENING"') do (
  set PID=%%a
)

if not defined PID (
  echo RAG 服务未在运行
  exit /b 0
)

taskkill /PID %PID% /F >nul 2>&1
echo 已停止 RAG 服务（PID %PID%）
