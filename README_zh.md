<div align="center">

# 🧠 Claude Local RAG

**让 Claude Code 拥有跨对话的长期记忆，从你的文档中精准检索知识**

[![Python](https://img.shields.io/badge/Python-3.8+-3776AB?style=flat-square&logo=python&logoColor=white)](https://www.python.org/)
[![FastAPI](https://img.shields.io/badge/FastAPI-0.100+-009688?style=flat-square&logo=fastapi&logoColor=white)](https://fastapi.tiangolo.com/)
[![FAISS](https://img.shields.io/badge/FAISS-Vector_Search-blue?style=flat-square)](https://github.com/facebookresearch/faiss)
[![License](https://img.shields.io/badge/License-MIT-green?style=flat-square)](LICENSE)
[![Claude Code](https://img.shields.io/badge/Claude_Code-Plugin-orange?style=flat-square)](https://claude.ai/code)

[安装](#安装) · [使用方法](#使用方法) · [工作原理](#工作原理prompt-是如何被修改的) · [命令汇总](#命令汇总) · [常见问题](#常见问题)

📖 [English Documentation](README.md)

</div>

---

## 为什么需要它？

Claude Code 原生没有向量数据库，自带的记忆系统基于文件笔记（CLAUDE.md），**不具备语义检索能力**。

| 原生限制 | 表现 | 本插件的解法 |
|---------|------|-------------|
| 关闭对话即遗忘 | 下次开新对话，Claude 对上次讨论的内容一无所知 | 文档存入本地向量库，永久保留，随时可用 |
| 大文档消耗大量 token | 把 100 页手册贴进对话，光读文档就花费大量费用 | 只检索与问题相关的片段，其余不传输 |
| 无法跨文档语义搜索 | Claude 无法同时"记住"多份文档并按语义查找 | 所有存入的文档统一索引，按语义返回最相关内容 |

> 🔒 **所有数据存储在本地，不会上传到任何服务器。**

---

## 安装

### 前提

| 工具 | macOS / Linux | Windows |
|------|--------------|---------|
| [Python 3.8+](https://www.python.org/downloads/) | 必须 | 必须 |
| [Node.js 16+](https://nodejs.org)（可选） | 飞书文档入库时依赖 | 飞书文档入库时依赖 |
| [curl](https://curl.se) | 系统自带 | Windows 10 1803+ 自带 |

### 一键安装

**macOS / Linux**

```bash
git clone https://github.com/Wrath-y/claude-local-rag
cd claude-local-rag
./start.sh
```

**Windows**（命令提示符 / PowerShell，以管理员身份运行）

```bat
git clone https://github.com/Wrath-y/claude-local-rag
cd claude-local-rag
start.bat
```

脚本自动完成依赖安装、Hook 注册、服务启动。看到以下提示即成功：

```
安装完成！重启 Claude Code 后即可开箱即用。
```

**重启 Claude Code 后即可使用。**

> 脚本可重复运行，不会产生重复配置。如果移动了项目目录，需重新运行以更新路径。

---

## 使用方法

所有操作在 Claude Code 对话框中完成，输入 `/rag` 触发补全提示。

### 📥 存入文档

```bash
/rag 你的文档内容...                              # 直接粘贴文字
/rag https://xxx.feishu.cn/docx/xxx              # 飞书文档链接
/rag https://example.com/docs/api                # 任意网页 URL
/rag /path/to/file.txt                           # 本地文件路径（支持 .txt .md .pdf 等）
/rag /path/to/file.txt --source 产品手册v2        # 自定义来源标识
```

| 输入类型 | 自动推断的来源标识 |
|---------|-----------------|
| 直接文字 | `manual` |
| 飞书文档链接 | 链接 URL |
| 任意网页 URL | 链接 URL |
| 本地文件路径 | 文件名（如 `手册.txt`） |

> 📌 检索结果中会显示 `[来源: xxx]`，也可按来源单独删除。

---

### 🔍 检索知识库

```bash
/rag-retrieve Redis 缓存穿透怎么处理？
```

---

### ⚡ 自动检索模式

开启后，每次提交 prompt 时自动检索知识库并注入结果，无需手动触发：

```bash
/rag-mode on    # 开启（持久化，重启后依然有效）
/rag-mode off   # 关闭
```

> 由 Hook 驱动，不依赖对话上下文，不受 `/clear` 或 compaction 影响。

---

### 🤖 代码自动入库

开启后，Claude 每次读取或修改源码文件时自动同步向量库：

```bash
/rag-auto-index on    # 开启（持久化）
/rag-auto-index off   # 关闭
```

| 操作 | 行为 |
|------|------|
| Claude 读取源码文件 | 自动入库（去重） |
| Claude 编辑源码文件 | 自动删旧 chunks + 重新入库 |

> 仅处理 `.py` `.ts` `.go` `.java` `.rs` 等源码文件，跳过 > 100KB 的文件。

---

### 🔄 更新文档

文档内容变更后重新同步，一条命令替代「删除 + 重新入库」两步：

```bash
/rag-update https://xxx.feishu.cn/docx/xxx
/rag-update /path/to/file.txt --source 产品手册v2
```

---

### 📊 管理知识库

```bash
/rag-status                        # 查看服务状态、chunk 总数及检索命中率
/rag-sources                       # 列出所有来源及各来源 chunk 数
/rag-source-delete <来源名称>       # 删除指定来源（弹出确认）
/rag-reset                         # 清空全部知识库（弹出确认）
/rag-export ~/backup.zip           # 导出知识库为 zip（可用于迁移）
/rag-import ~/backup.zip           # 从备份导入（弹出确认，替换现有数据）
```

---

### 🎯 Rerank 精排

开启后，检索结果经过 cross-encoder 二次排序，提升相关性精度：

```bash
/rag-rerank on    # 开启
/rag-rerank off   # 关闭
```

> 首次开启会下载 `BAAI/bge-reranker-base` 模型（约 400MB），之后进程内复用。每次检索额外增加约 50～200ms，不消耗 token。

---

## 命令汇总

| 命令 | 说明 | 额外 Token |
|------|------|:----------:|
| `/rag <内容或链接> [--source <名称>]` | 存入文档，`--source` 自定义来源标识（缺省时自动推断） | — |
| `/rag-update <链接或路径> [--source <名称>]` | 更新已有来源（删旧 + 重新入库），`--source` 指定来源需与入库时一致 | — |
| `/rag-retrieve <问题>` | 主动检索 | ✓ 少量 |
| `/rag-mode on/off` | 自动检索模式 | ✓ 开启时 |
| `/rag-auto-index on/off` | 代码自动入库 | — |
| `/rag-rerank on/off` | rerank 精排 | — |
| `/rag-verbose on/off` | 检索可观测性日志 | — |
| `/rag-status` | 服务状态 + chunk 总数 + 检索命中率统计 | — |
| `/rag-sources` | 列出所有来源及各来源 chunk 数 | — |
| `/rag-source-delete <名称>` | 按来源删除（名称需与入库时的来源标识完全一致） | — |
| `/rag-reset` | 清空全部知识库 | — |
| `/rag-export [路径]` | 导出知识库为 zip 备份（默认 `~/rag_backup.zip`） | — |
| `/rag-import <zip路径>` | 从 zip 备份导入，替换当前知识库（有确认步骤） | — |

---

## 工作原理：prompt 是如何被修改的

RAG 通过 **Claude Code Hook 机制**拦截 prompt，在发送给模型之前注入检索结果：

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

### 检索流水线

```
用户问题
    ↓
① FAISS 向量检索（取 top_k × 3 候选，余弦相似度）
    ↓
② 阈值过滤（相似度 < 0.45 丢弃）
    ↓
③ BM25 混合评分（final = vec × 0.7 + bm25 × 0.3）→ 取 top_k
    ↓
④ Cross-Encoder Rerank（可选，开启后重排 top_k 顺序）
    ↓
返回最终 chunks，注入 system prompt
```

### 入库流水线

```
原始文本（粘贴 / 文件 / URL / 飞书文档）
    ↓
Chunk 切分（按句子边界，200–400 token/块，相邻块保留 2 句重叠）
    ↓
Embedding 缓存命中？→ 是：直接复用向量；否：调用 BGE 模型编码
    ↓
FAISS IndexFlatIP 写入 + BM25 索引重建
    ↓
持久化（index.bin + chunks.pkl）
```

> **关键优化**：Embedding 缓存在服务启动时从 FAISS 向量自动恢复，无需重新 encode。删除来源后重建索引时，所有保留 chunk 均命中缓存，延迟极低。缓存在删来源时同步清理已失效条目，`/rag-update` 频繁更新文档不会造成缓存膨胀。

### 检索可观测性

```bash
/rag-verbose on    # 开启详细日志
tail -f /tmp/claude-local-rag.log
```

```
[retrieve] 查询: '/api/v2/orders 接口返回 403，排查一下'
[retrieve] FAISS 返回 9 个候选（库总量 137）
[retrieve] 阈值过滤（< 0.45）丢弃 6 个，剩余 3 个
  vec=0.774 bm25=0.600 final=0.722 [api-spec.md] '/api/v2/orders 需要 scope: orders:read...'
  vec=0.691 bm25=0.400 final=0.604 [auth-guide.md] 'Bearer Token 缺少权限时返回 403...'
  vec=0.652 bm25=0.200 final=0.516 [changelog.md] 'v2.3.0 增加了 IP 白名单校验'
[retrieve] rerank 后顺序:
  rerank=0.912 'Bearer Token 缺少权限时返回 403...'
  rerank=0.743 '/api/v2/orders 需要 scope: orders:read...'
[retrieve] 最终返回 3 个 chunks
```

每条候选显示向量相似度（`vec`）、BM25 关键词得分（`bm25`）、混合得分（`final`）、来源及文本预览。

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

**结果：** Claude 直接定位到 scope 缺失、IP 白名单两个方向，而不是从零开始猜测。

---

## 服务管理

**macOS / Linux**

```bash
./start.sh                          # 安装依赖 + 启动服务
./stop.sh                           # 停止服务
tail -f /tmp/claude-local-rag.log   # 查看运行日志
```

**Windows**

```bat
start.bat                                    # 安装依赖 + 启动服务
stop.bat                                     # 停止服务
type %TEMP%\claude-local-rag.log             # 查看运行日志
```

---

## 配置

编辑 `config.yaml` 调整参数：

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `chunk.min_tokens` | `200` | 每段最小长度 |
| `chunk.max_tokens` | `400` | 每段最大长度 |
| `retrieve.top_k` | `3` | 检索返回的段落数 |
| `retrieve.verbose` | `true` | 是否输出检索日志 |
| `rerank.enabled` | `false` | 是否默认开启 rerank |
| `rerank.model` | `BAAI/bge-reranker-base` | rerank 模型 |
| `model.name` | `BAAI/bge-small-zh-v1.5` | 向量模型 |
| `embedding.doc_prefix` | `段落：` | 入库时加在文本前的前缀（BGE 模型专用） |
| `embedding.query_prefix` | `查询：` | 检索时加在查询前的前缀（BGE 模型专用） |

### 更换向量模型

`model.name` 支持任意 `sentence-transformers` 兼容模型，直接修改 `config.yaml` 并重启服务即可生效。**切换模型前必须执行 `/rag-reset`**，因为不同模型的向量空间不兼容，用旧向量检索新模型会产生错误结果。

`doc_prefix` / `query_prefix` 是 BGE 系列模型的专用前缀，换用非 BGE 模型时需将两者清空：

```yaml
embedding:
  doc_prefix: ""
  query_prefix: ""
```

常用可替换选项：

| 模型 | 维度 | 语言 | 特点 |
|------|------|------|------|
| `BAAI/bge-small-zh-v1.5`（默认） | 512 | 中文 | 体积小，速度快 |
| `BAAI/bge-base-zh-v1.5` | 768 | 中文 | 精度更高，模型更大 |
| `BAAI/bge-small-en-v1.5` | 512 | 英文 | 英文文档首选 |
| `BAAI/bge-m3` | 1024 | 多语言 | 中英混合文档首选，速度较慢 |
| `sentence-transformers/all-MiniLM-L6-v2` | 384 | 英文 | 通用英文，非 BGE，prefix 需清空 |

### 为什么 top_k 默认是 3？

`top_k = 3` 是在**召回率**与 **token 成本**之间取得平衡的经验值：

- **Token 预算**：每个 chunk 约 200–400 token，3 个合计 ~600–1200 token，不会让检索结果本身成为消耗大头
- **精排质量兜底**：向量检索 → 混合评分 → Rerank 三层过滤，3 个高质量结果优于 10 个良莠不齐的候选
- **"Lost in the Middle"**：LLM 对上下文中间位置的注意力已知会下降，chunk 越多反而影响精度

| 场景 | 建议值 |
|------|--------|
| 默认 / 通用 | `3` |
| 主题分散、多文档综合 | `5` |
| 极度关注 token 成本 | `1–2` |
| 不建议超过 | `8` |

---

## 项目结构

```
claude-local-rag/
├── server.py                   # FastAPI 后台服务
├── config.yaml                 # 配置文件
├── requirements.txt            # Python 依赖
├── setup_hook.py               # 跨平台 Hook 注册脚本（由 start.sh / start.bat 调用）
├── start.sh                    # 一键安装脚本（macOS / Linux）
├── stop.sh                     # 停止服务脚本（macOS / Linux）
├── start.bat                   # 一键安装脚本（Windows）
├── stop.bat                    # 停止服务脚本（Windows）
├── index.bin                   # 向量索引（自动生成）
├── chunks.pkl                  # 文档存储（自动生成）
└── .claude/
    ├── settings.json           # Hook 配置
    ├── hook_script.py          # UserPromptSubmit Hook
    ├── auto_index_hook.py      # PostToolUse Hook（代码自动入库）
    └── commands/               # 斜杠命令定义
        ├── rag.md
        ├── rag-retrieve.md
        ├── rag-mode.md
        ├── rag-auto-index.md
        └── ...
```

---

## 路线图

> 标记当前已实现功能，未勾选项为计划中的改进方向。欢迎通过 Issue 或 PR 参与共建。

**检索质量**

- [x] 向量语义检索（FAISS + BGE Embedding）
- [x] BM25 混合评分（vec × 0.7 + bm25 × 0.3），BM25 替代 bigram 覆盖率，提升长尾词召回
- [x] Cross-Encoder Rerank 精排
- [ ] Chunk 首尾重叠（overlap），避免语义在边界处截断
- [ ] 语义切分（按段落/主题边界，替代当前句子边界切分）
- [x] 动态 top_k（根据剩余上下文窗口自动调整返回数量）

**知识库管理**

- [x] 按来源管理（入库 / 更新 / 删除）
- [x] 支持飞书文档、本地文件、纯文本多种输入
- [x] 代码文件自动入库（PostToolUse Hook）
- [x] Embedding 缓存（跳过已向量化的相同内容，加速重复入库）
- [ ] 定时重新索引（监听文件变更，自动触发 `/rag-update`）
- [x] 知识库导出 / 导入（备份 `index.bin` + `chunks.pkl` 并迁移）

**文档格式支持**

- [x] 纯文本 / Markdown
- [x] 飞书云文档
- [x] PDF 解析入库
- [ ] Word / Excel 文件解析
- [x] 网页 URL 抓取（非飞书）

**可观测性与调优**

- [x] 检索可观测性日志（vec / bm25 / final 评分逐条展示）
- [x] `/rag-verbose on/off` 开关
- [ ] Web 管理界面（可视化查看 chunks、测试检索效果）
- [x] 检索命中率统计（帮助判断入库质量和 top_k 设置是否合理）

---

## 常见问题

<details>
<summary><b>Q：/rag 命令没有出现补全提示？</b></summary>

重启 Claude Code，确保已运行 `./start.sh`。
</details>

<details>
<summary><b>Q：提示"服务未启动"？</b></summary>

运行 `./start.sh` 重新启动服务，或查看日志排查原因：

```bash
tail -f /tmp/claude-local-rag.log
```
</details>

<details>
<summary><b>Q：飞书文档无法读取？</b></summary>

需要先安装并配置 lark-cli，参考[官方文档](https://www.feishu.cn/content/article/7623291503305083853)。
</details>

<details>
<summary><b>Q：检索结果不准确？</b></summary>

- 建议存入结构清晰、语义完整的段落
- 避免存入表格截图或扫描件文字
- 知识库较大时开启 `/rag-rerank on` 提升精度
- 通过 `/rag-verbose on` + 查看日志分析具体命中情况
</details>

<details>
<summary><b>Q：/clear 之后 RAG 还能用吗？</b></summary>

可以。`/clear` 只清空对话上下文，不影响向量库数据、持久化标志文件（rag_mode 等）和后台服务。
</details>

<details>
<summary><b>Q：为什么不支持 Query Rewriting（查询改写）？</b></summary>

Query Rewriting 通过 LLM 将用户问题改写为更适合检索的形式来提升召回率，是常见的 RAG 增强手段。本项目**有意不引入**，原因如下：

- **违背节省 token 的核心目标**：每次改写都需要额外调用 LLM，反而增加消耗
- **同步 Hook 不适合 LLM 调用**：`UserPromptSubmit` Hook 是同步拦截，LLM 调用会引入明显延迟
- **已有三层过滤兜底**：向量语义检索 → 关键词混合评分 → Rerank 精排，覆盖了大多数检索场景

> 如果遇到指代不清的查询（如「那这个呢？」），建议使用 `/rag-retrieve <完整问题>` 主动检索，效果优于依赖自动模式。
</details>
