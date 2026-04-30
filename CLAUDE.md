# 🧠 Claude Local RAG

## 为什么需要它？

Claude Code 无向量库，记忆系统基于文件笔记（CLAUDE.md），**无语义检索**。

| 原生限制 | 表现 | 本插件的解法 |
|---------|------|-------------|
| 关闭对话即遗忘 | 新对话 Claude 对上次内容一无所知 | 文档存本地向量库，永久保留随时可用 |
| 大文档耗大量 token | 100 页手册贴进对话，读文档就大量费用 | 只检索相关片段，其余不传输 |
| 无法跨文档语义搜索 | Claude 无法同时"记住"多份文档按语义查找 | 所有文档统一索引，按语义返回最相关内容 |

> 🔒 **所有数据本地存储，不上传任何服务器。**

---

## 安装

### 前提

| 工具 | 说明 |
|------|------|
| [Python 3.8+](https://www.python.org/downloads/) | 运行后台服务 |
| [jq](https://jqlang.github.io/jq/download/) | 解析 JSON 配置，Mac 用户运行 `brew install jq` |
| [Node.js 16+](https://nodejs.org)（可选） | 飞书文档入库依赖 |

### 一键安装

```bash
git clone https://github.com/Wrath-y/claude-local-rag
cd claude-local-rag
./start.sh
```

脚本自动完成依赖安装、Hook 注册、服务启动。看到以下提示即成功：

```
安装完成！重启 Claude Code 后即可开箱即用。
```

**重启 Claude Code 后即可使用。**

> 脚本可重复运行，不产生重复配置。移动项目目录需重新运行更新路径。

---

## 使用方法

所有操作在 Claude Code 对话框完成，输入 `/rag` 触发补全提示。

### 📥 存入文档

```bash
/rag 你的文档内容...                              # 直接粘贴文字
/rag https://xxx.feishu.cn/docx/xxx              # 飞书文档链接
/rag /path/to/file.txt                           # 本地文件路径
/rag /path/to/file.txt --source 产品手册v2        # 自定义来源标识
```

| 输入类型 | 自动推断的来源标识 |
|---------|-----------------|
| 直接文字 | `manual` |
| 飞书文档链接 | 链接 URL |
| 本地文件路径 | 文件名（如 `手册.txt`） |

> 📌 检索结果显示 `[来源: xxx]`，可按来源单独删除。

---

### 🔍 检索知识库

```bash
/rag-retrieve Redis 缓存穿透怎么处理？
```

---

### ⚡ 自动检索模式

开启后，每次提交 prompt 自动检索知识库并注入结果，无需手动触发：

```bash
/rag-mode on    # 开启（持久化，重启后依然有效）
/rag-mode off   # 关闭
```

> Hook 驱动，不依赖对话上下文，不受 `/clear` 或 compaction 影响。

---

### 🔄 更新文档

文档变更后重新同步，一条命令替代「删除 + 重新入库」两步：

```bash
/rag-update https://xxx.feishu.cn/docx/xxx
/rag-update /path/to/file.txt --source 产品手册v2
```

---

### 📊 管理知识库

```bash
/rag-status                        # 查看服务状态和 chunk 总数
/rag-sources                       # 列出所有来源及各来源 chunk 数
/rag-source-delete <来源名称>       # 删除指定来源（弹出确认）
/rag-reset                         # 清空全部知识库（弹出确认）
```

---

### 🎯 Rerank 精排

开启后，检索结果经 cross-encoder 二次排序，提升相关性精度：

```bash
/rag-rerank on    # 开启
/rag-rerank off   # 关闭
```

> 首次开启下载 `BAAI/bge-reranker-base` 模型（约 400MB），之后进程内复用。每次检索额外约 50～200ms，不消耗 token。

---

## 命令汇总

| 命令 | 说明 | 额外 Token |
|------|------|:----------:|
| `/rag <内容或链接> [--source <名称>]` | 存入文档，`--source` 自定义来源标识（缺省自动推断） | — |
| `/rag-update <链接或路径> [--source <名称>]` | 更新来源（删旧 + 重新入库），`--source` 需与入库时一致 | — |
| `/rag-retrieve <问题>` | 主动检索 | ✓ 少量 |
| `/rag-mode on/off` | 自动检索模式 | ✓ 开启时 |
| `/rag-rerank on/off` | rerank 精排 | — |
| `/rag-verbose on/off` | 检索可观测性日志 | — |
| `/rag-status` | 服务状态 + chunk 总数 | — |
| `/rag-sources` | 所有来源及各来源 chunk 数 | — |
| `/rag-source-delete <名称>` | 按来源删除（名称需与入库时来源标识完全一致） | — |
| `/rag-reset` | 清空全部知识库 | — |

---

## 工作原理：prompt 是如何被修改的

RAG 通过 **Claude Code Hook 机制**拦截 prompt，发送给模型前注入检索结果：

```
用户输入 prompt
    ↓
UserPromptSubmit Hook（hook_script.py）
    ├─ rag-mode off → 原样发出
    └─ rag-mode on  → POST /retrieve
                        ↓
                      输出 additionalContext
                        ↓
                      注入 system prompt 区域
                        ↓
                      模型看到：[system prompt] + [RAG 结果] + [用户 prompt]
```

`additionalContext` 注入在 system prompt 层，**模型可见，用户侧不显示**，不改变对话结构。

### 检索可观测性

```bash
/rag-verbose on    # 开启详细日志
tail -f /tmp/claude-local-rag.log
```
每条候选显示向量相似度（`vec`）、关键词覆盖率（`kw`）、混合得分（`final`）、来源及文本预览。

### 真实场景示例

团队将内部 API 文档、接口规范、上线 checklist 存入向量库，开启 `rag-mode on`。

用户输入：
```
/api/v2/orders 接口返回 403，排查一下
```

Claude 实际收到（用户不可见）：
```
[RAG 自动检索结果]
[来源: api-spec.md]
/api/v2/orders 需要 scope: orders:read，调用方需在申请 token 时声明该 scope...
---
[来源: auth-guide.md]
Bearer Token 缺少权限时返回 403，检查 token 的 scope 列表...
---
[来源: changelog.md]
v2.3.0 对 /orders 接口增加了 IP 白名单校验，非白名单 IP 同样返回 403...
```

**结果：** Claude 直接定位 scope 缺失、IP 白名单两个方向，不从零猜测。

---

## 配置

编辑 `config.yaml` 调整参数：

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `chunk.min_tokens` | `200` | 每段最小长度 |
| `chunk.max_tokens` | `400` | 每段最大长度 |
| `retrieve.top_k` | `3` | 检索返回段落数 |
| `retrieve.verbose` | `true` | 是否输出检索日志 |
| `rerank.enabled` | `false` | 是否默认开启 rerank |
| `rerank.model` | `BAAI/bge-reranker-base` | rerank 模型 |

## 项目结构

```
claude-local-rag/
├── server.py                   # FastAPI 后台服务
├── config.yaml                 # 配置文件
├── requirements.txt            # Python 依赖
├── start.sh                    # 一键安装脚本
├── stop.sh                     # 停止服务脚本
├── index.bin                   # 向量索引（自动生成）
├── chunks.pkl                  # 文档存储（自动生成）
└── .claude/
    ├── settings.json           # Hook 配置
    ├── hook_script.py          # UserPromptSubmit Hook
    └── commands/               # 斜杠命令定义
        ├── rag.md
        ├── rag-retrieve.md
        ├── rag-mode.md
        └── ...
```