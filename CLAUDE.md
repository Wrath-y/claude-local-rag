# CLAUDE.md

## 项目名称

Local RAG Plugin（本地知识检索插件）

---

## 1. 项目简介

本地运行的 RAG（Retrieval-Augmented Generation）系统，为 Claude Code 提供文档检索能力。

核心目标：
- 本地运行，无需云端服务
- 使用 embedding 提升上下文准确性
- 减少 token 消耗
- 提供稳定的知识检索能力

---

## 2. 核心架构

```
Claude Code（/rag 命令 / 关键词触发 / mode on 自动检索）
    ↓
FastAPI 本地服务（port 8765）
    ↓
Embedding（BAAI/bge-small-zh-v1.5，sentence-transformers）
    ↓
向量检索（FAISS IndexFlatIP，余弦相似度）
    ↓
返回相关文档 chunks
```

---

## 3. 技术选型

| 模块 | 技术 |
|------|------|
| 服务框架 | FastAPI + uvicorn |
| Embedding | BAAI/bge-small-zh-v1.5（sentence-transformers） |
| 向量数据库 | FAISS IndexFlatIP |
| 持久化 | index.bin（FAISS）+ chunks.pkl（pickle） |
| 语言 | Python 3.8+ |

---

## 4. API 接口

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/ingest` | 文档入库 |
| POST | `/retrieve` | 向量检索，返回 top-k chunks |
| GET | `/health` | 健康检查，返回 chunk 总数 |
| DELETE | `/reset` | 清空向量库和持久化文件 |

### /ingest 请求

```json
{ "text": "文档内容" }
```

### /retrieve 请求 / 响应

```json
// 请求
{ "text": "用户问题" }

// 响应
{ "chunks": ["相关文本1", "相关文本2", "相关文本3"] }
```

---

## 5. 关键设计

### Embedding 策略

- 文档前缀：`段落：<内容>`
- 查询前缀：`查询：<问题>`
- `normalize_embeddings=True`（余弦相似度等价于内积）

### Chunk 策略

- 按句子边界切分（`。！？.!?`）
- 目标区间：200～400 token（以字符数估算）
- 保留语义完整性，不按固定字符数硬切

### 相似度

- FAISS IndexFlatIP（内积 = 归一化向量的余弦相似度）

---

## 6. 项目结构

```
claude-local-rag/
 ├── server.py              # FastAPI 服务主体
 ├── config.yaml            # 模型、chunk、top-k、端口配置
 ├── requirements.txt       # Python 依赖
 ├── start.sh             # 一键安装：依赖 + Claude Code 配置 + 启动服务
 ├── stop.sh                # 停止服务
 ├── index.bin              # FAISS 索引（运行后生成）
 ├── chunks.pkl             # 文本块存储（运行后生成）
 └── .claude/
     ├── settings.json      # UserPromptSubmit Hook（关键词入库触发）
     └── commands/
         └── rag.md         # /rag 斜杠命令定义
```

---

## 7. Claude Code 集成

### 自动启动

`start.sh` 向 `~/.claude/settings.json` 写入 `SessionStart` Hook，Claude Code 启动时自动拉起服务（已运行则跳过）。

### /rag 斜杠命令

定义在 `.claude/commands/rag.md`，`start.sh` 同步复制到 `~/.claude/commands/`（全局可用）。

| 命令 | 说明 |
|------|------|
| `/rag <内容/链接>` | 存入向量库（支持飞书链接、文本、文件路径） |
| `/rag retrieve <问题>` | 主动检索 |
| `/rag mode on/off` | 开启/关闭本次会话自动检索 |
| `/rag status` | 查看服务状态和 chunk 数 |
| `/rag reset` | 清空向量库（二次确认） |

### 关键词触发入库

`.claude/settings.json` 中的 `UserPromptSubmit` Hook 检测关键词（`存入向量库` / `写入向量库` / `加入向量库` / `入库`），触发时注入 `additionalContext` 提示 Claude 调用 `/ingest`。

---

## 8. 性能预期

| 模块 | 延迟 |
|------|------|
| embedding（单次） | ~10ms |
| FAISS 检索 | <5ms |
| 服务冷启动（含模型加载） | 30～120s |

---

## 9. 配置项（config.yaml）

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `model.name` | `BAAI/bge-small-zh-v1.5` | embedding 模型 |
| `embedding.doc_prefix` | `段落：` | 文档向量化前缀 |
| `embedding.query_prefix` | `查询：` | 查询向量化前缀 |
| `chunk.min_tokens` | `200` | 最小 chunk 长度 |
| `chunk.max_tokens` | `400` | 最大 chunk 长度 |
| `retrieve.top_k` | `3` | 检索返回数量 |
| `server.port` | `8765` | 服务端口 |

---

## 10. 注意事项

- embedding 仅用于检索，不参与生成
- 文档质量直接影响检索效果，建议存入结构清晰的段落
- 模型首次启动需下载（~25MB），之后从本地缓存加载
- 向量库数据持久化在 `index.bin` / `chunks.pkl`，`reset` 会删除这两个文件

---

## 11. 后续优化方向

- rerank 模型（提升检索精度）
- 语义切分（替代当前句子边界切分）
- 多文档管理（按来源分组检索）
- embedding 缓存（避免重复向量化相同文本）
