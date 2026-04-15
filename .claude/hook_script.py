import sys, json, os, re, requests

data = json.load(sys.stdin)
prompt = data.get('prompt', '')

_dir = os.path.dirname(os.path.abspath(__file__))
# mode on/off 持久化标志文件：存在即为 on，删除即为 off
MODE_FILE = os.path.join(_dir, 'rag_mode')

# ===== 工具函数 =====
def rag_mode_on():
    return os.path.exists(MODE_FILE)

def set_rag_mode(on: bool):
    if on:
        open(MODE_FILE, 'w').close()
    elif os.path.exists(MODE_FILE):
        os.remove(MODE_FILE)

def output(context: str):
    print(json.dumps({
        'hookSpecificOutput': {
            'hookEventName': 'UserPromptSubmit',
            'additionalContext': context
        }
    }))

# ===== 1. mode on/off 检测 =====
# 拦截 /rag mode on|off 指令，写入/删除标志文件后退出，不再走入库或检索逻辑
# 同时兼容旧语法 /rag mode on|off 和新命令 /rag-mode on|off
mode_match = re.search(r'/rag[-\s]mode\s+(on|off)', prompt, re.IGNORECASE)
if mode_match:
    on = mode_match.group(1).lower() == 'on'
    set_rag_mode(on)
    output(f"\n[RAG] 自动检索模式已{'开启' if on else '关闭'}\n")
    sys.exit(0)

# ===== 2. 自动入库检测 =====
# 检测到入库关键词时，把当前 prompt 直接写入向量库
ingest_keywords = ['存入向量库', '写入向量库', '加入向量库']
if any(k in prompt for k in ingest_keywords):
    try:
        r = requests.post('http://127.0.0.1:8765/ingest',
                          json={'text': prompt}, timeout=5)
        result = r.json()
        output(f"\n[RAG] 已写入 {result.get('chunks_added', 0)} 个 chunks\n")
    except Exception as e:
        output(f"\n[RAG] 写入失败: {e}\n")
    sys.exit(0)

# ===== 3. mode on 时自动检索 =====
# 读取持久化标志，若 on 则检索并将结果注入为 additionalContext
# 服务未启动时静默跳过，不影响正常对话
if rag_mode_on() and prompt.strip():
    try:
        r = requests.post('http://127.0.0.1:8765/retrieve',
                          json={'text': prompt}, timeout=5)
        chunks = r.json().get('chunks', [])
        if chunks:
            joined = '\n---\n'.join(chunks)
            output(f"\n[RAG 自动检索结果]\n{joined}\n\n请参考以上内容回答用户问题。若无关则忽略。\n")
    except Exception:
        pass
