更新向量库中指定来源的内容：先删除旧 chunks，再重新入库最新内容。

输入参数：$ARGUMENTS（飞书文档链接 或 本地文件路径，可附带 --source）

## --source 参数

若参数中包含 `--source <名称>`，提取该值作为来源标识，并从输入中移除这部分。

未指定 `--source` 时，按以下规则自动推断：
- 飞书文档链接（含 `feishu.cn` / `larksuite.com`）→ `source` = 链接 URL
- 本地文件路径 → `source` = 文件名（basename，不含目录）

## 执行步骤

**第一步：获取最新内容**

根据输入类型：
- 飞书文档链接 → 用 lark-doc 技能获取文档正文
- 本地文件路径 → 读取文件内容

**第二步：删除旧 chunks**

```bash
curl -s -X DELETE "http://127.0.0.1:8765/source?name=<source>"
```

若返回 404（来源不存在），说明该来源从未入库，跳过删除直接入库，并告知用户。

**第三步：写入最新内容**

```bash
curl -s -X POST http://127.0.0.1:8765/ingest \
  -H "Content-Type: application/json" \
  -d "{\"text\": \"<内容>\", \"source\": \"<source>\"}"
```

## 输出

告知用户：
- 删除了多少个旧 chunks（或"首次入库，无需删除"）
- 写入了多少个新 chunks
- 来源标识

服务未启动时提示运行 `./start.sh`。
