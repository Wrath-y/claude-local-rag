存入向量库（支持文本、飞书链接、任意网页 URL、本地文件路径、PDF）

输入参数：$ARGUMENTS

## --source 参数

若参数中包含 `--source <名称>`，提取该值作为来源标识，并从输入中移除这部分。

示例：
- `/rag 这段文字 --source 会议纪要Q1` → source = "会议纪要Q1"
- `/rag /path/to/file.txt --source 产品手册v2` → source = "产品手册v2"
- `--source` 可出现在参数任意位置

未指定 `--source` 时，按以下规则自动推断：
- 飞书文档链接 → `source` = 链接 URL
- 任意网页 URL → `source` = 链接 URL
- 本地文件路径 → `source` = 文件名（basename，不含目录）
- 其他文本 → `source` = "manual"

## 内容获取

根据去掉 `--source` 后的剩余输入判断类型：

1. **飞书文档链接**（含 `feishu.cn` / `larksuite.com`）→ 用 lark-doc 技能获取文档正文
2. **任意 HTTP/HTTPS URL**（非飞书）→ 用 WebFetch 工具抓取页面正文，提取纯文本内容
3. **本地文件路径**（以 `/` 或 `./` 开头，或明确是路径格式）→ 用 Read 工具读取文件内容（支持 `.txt` `.md` `.pdf` `.py` 等所有 Read 工具支持的格式）
4. **其他** → 直接作为文本输入

## 写入

POST 内容到 `/ingest`，附带 source：

```bash
curl -s -X POST http://127.0.0.1:8765/ingest \
  -H "Content-Type: application/json" \
  -d "{\"text\": \"<内容>\", \"source\": \"<source>\"}"
```

告知用户写入了多少个 chunk 及来源标识。

服务未启动时提示用户运行 `./start.sh`。
