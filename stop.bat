@echo off
setlocal EnableDelayedExpansion

rem ── 读取 config.yaml 中的日志语言，默认 zh ──────────────────
set SCRIPT_DIR=%~dp0
set SCRIPT_DIR=%SCRIPT_DIR:~0,-1%
set LANG_CODE=zh
for /f "usebackq tokens=*" %%i in (`findstr /r "lang:" "%SCRIPT_DIR%\config.yaml" 2^>nul`) do (
  set _RAW=%%i
  set _RAW=!_RAW: =!
  set _RAW=!_RAW:"=!
  set _RAW=!_RAW:'=!
  for /f "tokens=2 delims=:" %%j in ("!_RAW!") do set LANG_CODE=%%j
)

if "%LANG_CODE%"=="en" (
  set M_NOT_RUNNING=RAG service is not running
  set M_STOPPED=RAG service stopped (PID
) else (
  set M_NOT_RUNNING=RAG 服务未在运行
  set M_STOPPED=已停止 RAG 服务（PID
)

set PID=
for /f "tokens=5" %%a in ('netstat -ano ^| findstr ":8765 " ^| findstr "LISTENING"') do (
  set PID=%%a
)

if not defined PID (
  echo %M_NOT_RUNNING%
  exit /b 0
)

taskkill /PID %PID% /F >nul 2>&1
echo %M_STOPPED% %PID%^)
