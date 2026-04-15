import sys, json, os, requests

data = json.load(sys.stdin)
tool_name = data.get('tool_name', '')
tool_input = data.get('tool_input', {})
tool_response = data.get('tool_response', '')

_dir = os.path.dirname(os.path.abspath(__file__))
AUTO_INDEX_FILE = os.path.join(_dir, 'rag_auto_index')

SOURCE_EXTENSIONS = {
    '.py', '.ts', '.tsx', '.js', '.jsx', '.go', '.java', '.rs',
    '.cpp', '.c', '.h', '.cs', '.rb', '.php', '.swift', '.kt',
    '.scala', '.sh', '.bash', '.zsh', '.vue', '.svelte',
}
MAX_BYTES = 100 * 1024  # 100KB


def auto_index_on():
    return os.path.exists(AUTO_INDEX_FILE)


def should_index(file_path: str) -> bool:
    if not file_path:
        return False
    return os.path.splitext(file_path)[1].lower() in SOURCE_EXTENSIONS


def ingest(content: str, source: str):
    if len(content.encode('utf-8')) > MAX_BYTES:
        return
    try:
        requests.post('http://127.0.0.1:8765/ingest',
                      json={'text': content, 'source': source}, timeout=5)
    except Exception:
        pass


def delete_source(source: str):
    try:
        requests.delete(f'http://127.0.0.1:8765/source',
                        params={'name': source}, timeout=5)
    except Exception:
        pass


if not auto_index_on():
    sys.exit(0)

if tool_name == 'Read':
    file_path = tool_input.get('file_path', '')
    if should_index(file_path) and tool_response:
        ingest(tool_response, file_path)

elif tool_name in ('Edit', 'Write'):
    file_path = tool_input.get('file_path', '')
    if should_index(file_path) and os.path.exists(file_path):
        try:
            with open(file_path, 'r', encoding='utf-8', errors='ignore') as f:
                content = f.read()
            delete_source(file_path)
            ingest(content, file_path)
        except Exception:
            pass
