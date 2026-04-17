"""
跨平台 Claude Code Hook 注册脚本。
由 start.sh / start.bat 调用，负责：
  1. 写入 SessionStart hook（幂等，不重复）
  2. 复制 .claude/commands/rag*.md 到 ~/.claude/commands/
"""
import json
import os
import shutil
import sys

SCRIPT_DIR = os.path.dirname(os.path.abspath(__file__))
HOME = os.path.expanduser("~")
SETTINGS_PATH = os.path.join(HOME, ".claude", "settings.json")
COMMANDS_SRC = os.path.join(SCRIPT_DIR, ".claude", "commands")
COMMANDS_DST = os.path.join(HOME, ".claude", "commands")

# Windows: %TEMP%\claude-local-rag.log  /  Unix: /tmp/claude-local-rag.log
if sys.platform == "win32":
    LOG_PATH = os.path.join(os.environ.get("TEMP", "C:\\Temp"), "claude-local-rag.log")
    # Windows 下用 pythonw 后台启动服务
    HOOK_CMD = (
        f"curl -s http://127.0.0.1:8765/health > nul 2>&1 || "
        f"start /B pythonw -m uvicorn server:app --app-dir \"{SCRIPT_DIR}\" --port 8765 >> \"{LOG_PATH}\" 2>&1"
    )
else:
    LOG_PATH = "/tmp/claude-local-rag.log"
    HOOK_CMD = (
        f"curl -s http://127.0.0.1:8765/health > /dev/null 2>&1 || "
        f"(cd \"{SCRIPT_DIR}\" && nohup uvicorn server:app --port 8765 >> {LOG_PATH} 2>&1 &)"
    )


def load_settings():
    os.makedirs(os.path.dirname(SETTINGS_PATH), exist_ok=True)
    if os.path.exists(SETTINGS_PATH):
        with open(SETTINGS_PATH, "r", encoding="utf-8") as f:
            try:
                return json.load(f)
            except json.JSONDecodeError:
                return {}
    return {}


def save_settings(data):
    with open(SETTINGS_PATH, "w", encoding="utf-8") as f:
        json.dump(data, f, indent=2, ensure_ascii=False)


def register_hook(settings):
    hooks = settings.setdefault("hooks", {})
    session_start = hooks.setdefault("SessionStart", [])

    # 移除已有的 claude-local-rag hook（幂等）
    cleaned = []
    for group in session_start:
        group_hooks = [
            h for h in group.get("hooks", [])
            if "claude-local-rag" not in h.get("command", "")
        ]
        if group_hooks:
            cleaned.append({**group, "hooks": group_hooks})
    hooks["SessionStart"] = cleaned

    # 追加新 hook
    hooks["SessionStart"].append({
        "hooks": [{
            "type": "command",
            "command": HOOK_CMD,
            "statusMessage": "启动 RAG 服务...",
            "async": True
        }]
    })
    return settings


def copy_commands():
    os.makedirs(COMMANDS_DST, exist_ok=True)
    for fname in os.listdir(COMMANDS_SRC):
        if fname.startswith("rag") and fname.endswith(".md"):
            src = os.path.join(COMMANDS_SRC, fname)
            dst = os.path.join(COMMANDS_DST, fname)
            shutil.copy2(src, dst)
            print(f"  已写入 {dst}")


if __name__ == "__main__":
    print("[3/5] 注册 /rag 命令...")
    copy_commands()

    print("[4/5] 配置 Claude Code 自动启动 Hook...")
    settings = load_settings()
    settings = register_hook(settings)
    save_settings(settings)
    print(f"  已写入 SessionStart hook → {SETTINGS_PATH}")
