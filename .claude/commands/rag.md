本地 RAG 向量库操作（http://127.0.0.1:8765）。

输入参数：$ARGUMENTS

## 子命令解析

根据 $ARGUMENTS 的第一个词判断操作类型：

### ingest — 存入向量库（默认，可省略）

```
/rag ingest <内容或链接>
/rag <内容或链接>        ← 省略 ingest 也触发此操作
```

执行步骤：
1. 判断输入类型：
   - 飞书文档链接（含 `feishu.cn` / `larksuite.com`）→ 用 lark-doc 技能获取文档正文
   - 本地文件路径 → 读取文件内容
   - 其他 → 直接作为文本
2. POST 内容到 `/ingest`：
   ```bash
   curl -s -X POST http://127.0.0.1:8765/ingest \
     -H "Content-Type: application/json" \
     -d "{\"text\": \"<内容>\"}"
   ```
3. 告知用户写入了多少个 chunk

---

### retrieve — 检索向量库

```
/rag retrieve <问题>
/rag search <问题>       ← search 为 retrieve 的别名
```

执行步骤：
1. POST 问题到 `/retrieve`：
   ```bash
   curl -s -X POST http://127.0.0.1:8765/retrieve \
     -H "Content-Type: application/json" \
     -d "{\"text\": \"<问题>\"}"
   ```
2. 展示返回的相关 chunk 列表

---

### status — 查看服务状态

```
/rag status
```

执行步骤：
1. GET `/health`：
   ```bash
   curl -s http://127.0.0.1:8765/health
   ```
2. 展示服务状态及当前 chunk 总数

---

### reset — 清空向量库

```
/rag reset
```

执行步骤：
1. 向用户确认是否清空（不可恢复）
2. 用户确认后执行：
   ```bash
   curl -s -X DELETE http://127.0.0.1:8765/reset
   ```
3. 告知用户已清空

---

### mode on — 激活自动检索模式

```
/rag mode on
```

告知用户：RAG 自动检索模式已激活。然后在本次对话剩余部分，**每次回答前**先调用 `/retrieve` 检索与当前问题相关的内容，将检索结果作为背景知识参考后再回答。若检索结果与问题无关，则忽略检索结果正常回答。

---

### mode off — 关闭自动检索模式

```
/rag mode off
```

告知用户：RAG 自动检索模式已关闭。恢复正常对话，不再自动检索向量库。

---

## 注意事项
- 服务未启动时提示用户运行 `./start.sh`
- 飞书文档需已配置 lark-cli 认证
- reset 操作需二次确认，避免误操作
