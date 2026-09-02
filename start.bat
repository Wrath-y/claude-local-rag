@echo off
setlocal enabledelayedexpansion

cd /d "%~dp0"

:: Check Go
where go >nul 2>&1
if %errorlevel% neq 0 (
    echo Error: Go not installed. Get it from https://go.dev/dl/
    exit /b 1
)

:: Build if needed
if not exist rag-server.exe (
    echo Building rag-server...
    go build -o rag-server.exe ./cmd/server/
    if !errorlevel! neq 0 (
        echo Error: Build failed.
        exit /b 1
    )
)

:: Check if rebuild needed (go.mod newer than binary)
for %%A in (go.mod) do set MOD_TIME=%%~tA
for %%A in (rag-server.exe) do set BIN_TIME=%%~tA
if "!MOD_TIME!" gtr "!BIN_TIME!" (
    echo Rebuilding rag-server...
    go build -o rag-server.exe ./cmd/server/
)

:: Resolve only embedding.provider; an omitted value matches the Go default.
set "PROVIDER=local"
set "IN_EMBEDDING=0"
if exist config.yaml (
    for /f "usebackq tokens=1,* delims=:" %%A in ("config.yaml") do (
        set "RAW_KEY=%%A"
        set "RAW_VALUE=%%B"
        if /i "!RAW_KEY!"=="embedding" (
            set "IN_EMBEDDING=1"
        ) else if "!IN_EMBEDDING!"=="1" (
            if not "!RAW_KEY:~0,1!"==" " (
                set "IN_EMBEDDING=0"
            ) else (
                set "KEY="
                for /f "tokens=* delims= " %%K in ("!RAW_KEY!") do set "KEY=%%K"
                if /i "!KEY!"=="provider" (
                    for /f "tokens=* delims= " %%V in ("!RAW_VALUE!") do set "PROVIDER=%%V"
                    set "PROVIDER=!PROVIDER:"=!"
                    for /f "tokens=1 delims= #" %%V in ("!PROVIDER!") do set "PROVIDER=%%V"
                )
            )
        )
    )
)

:: Setup Python sidecar if local provider.
set "VENV_SCRIPTS="
if /i "!PROVIDER!"=="local" (
    set "PYTHON_BOOTSTRAP="
    where python >nul 2>&1
    if !errorlevel! equ 0 (
        set "PYTHON_BOOTSTRAP=python"
    ) else (
        where py >nul 2>&1
        if !errorlevel! equ 0 set "PYTHON_BOOTSTRAP=py -3"
    )
    if not defined PYTHON_BOOTSTRAP (
        echo Error: Python 3 required for local embedding.
        exit /b 1
    )
    if not exist sidecar\.venv (
        echo Setting up Python sidecar...
        !PYTHON_BOOTSTRAP! -m venv sidecar\.venv
        if !errorlevel! neq 0 exit /b 1
        sidecar\.venv\Scripts\python.exe -m pip install -q -r sidecar\requirements.txt
        if !errorlevel! neq 0 exit /b 1
    )
    set "VENV_SCRIPTS=!CD!\sidecar\.venv\Scripts"
)

:: Stop existing
if exist .rag-server.pid (
    set /p OLD_PID=<.rag-server.pid
    taskkill /PID !OLD_PID! /F >nul 2>&1
    del .rag-server.pid
)

:: Ensure daily server logs are captured under the repository root.
if not exist logs mkdir logs
set "LOG_DATE="
for /f %%D in ('powershell -NoProfile -Command "Get-Date -Format yyyyMMdd"') do set "LOG_DATE=%%D"
if not defined LOG_DATE set "LOG_DATE=current"
set "LOG_FILE=!CD!\logs\rag-server-!LOG_DATE!.log"

:: Start server with the venv first on PATH when local embedding is active.
if defined VENV_SCRIPTS set "PATH=!VENV_SCRIPTS!;!PATH!"
start /b "" rag-server.exe >>"!LOG_FILE!" 2>&1
timeout /t 1 /nobreak >nul

:: Get PID of rag-server.exe
for /f "tokens=2" %%a in ('tasklist /fi "imagename eq rag-server.exe" /fo list ^| findstr "PID:"') do (
    set PID=%%a
)
echo !PID!> .rag-server.pid

:: Wait for health
set ATTEMPTS=0
:healthloop
if !ATTEMPTS! geq 30 (
    echo Warning: server started but health check not responding yet
    goto :done
)
curl -s http://127.0.0.1:8765/health >nul 2>&1
if %errorlevel% equ 0 (
    echo RAG server started ^(PID: !PID!^) at http://127.0.0.1:8765
    goto :done
)
set /a ATTEMPTS+=1
timeout /t 1 /nobreak >nul
goto :healthloop

:done
endlocal
