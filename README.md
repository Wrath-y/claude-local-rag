# Claude Local Rag

让 Claude Code 拥有跨对话的长期记忆，并能从你的文档中精准检索知识。

---

## 这解决了什么问题？

**Claude 原生的三个限制：**

| 限制 | 表现 | 本插件的解法 |
|------|------|-------------|
| 关闭对话即遗忘 | 下次开新对话，Claude 对上次讨论的文档一无所知 | 文档存入本地向量库，永久保留，随时可用 |
| 大文档消耗大量 token | 把 100 页手册贴进对话，光读文档就花费大量费用 | 只检索与问题相关的片段，其余不传输 |
| 无法跨文档语义搜索 | Claude 无法同时"记住"你的多份文档并按语义查找 | 所有存入的文档统一索引，按语义返回最相关内容 |

**Claude Code 没有向量数据库**，自带的记忆系统基于文件笔记（CLAUDE.md），不具备语义检索能力。本插件补上了这个缺口。

**所有数据存储在本地，不会上传到任何服务器。**

---

## 安装前提

需要先安装以下工具（点击链接按说明操作）：

- [Python 3.8+](https://www.python.org/downloads/)
- [Node.js 16+](https://nodejs.org)（在使用 飞书 CLI 时依赖）
- [jq](https://jqlang.github.io/jq/download/)（命令行 JSON 工具）：Mac 用户运行 `brew install jq`

---

## 安装

**第一步：克隆项目**

```bash
git clone https://github.com/Wrath-y/claude-local-rag
cd claude-local-rag
```

**第二步：运行安装脚本**

```bash
./start.sh
```

脚本会自动完成所有配置，最后出现以下提示说明安装成功：

```
安装完成！重启 Claude Code 后即可开箱即用。
```

**然后重启 Claude Code。**

> 脚本可以重复运行，不会产生重复配置。

---

## 使用方法

所有操作都在 Claude Code 对话框中输入对应命令完成，输入 `/rag` 可看到所有可用命令。

### 存入文档

将文档存入知识库（只需做一次，数据永久保存）：

```
/rag 你的文档内容...
```

支持三种方式：

| 输入内容 | 示例 | 来源标识（source） |
|----------|------|-------------------|
| 直接粘贴文字 | `/rag 我们公司的请假流程是...` | `manual` |
| 飞书文档链接 | `/rag https://xxx.feishu.cn/docx/xxx` | 链接 URL |
| 本地文件路径 | `/rag /Users/你的名字/文档/手册.txt` | 文件名（如 `手册.txt`） |

来源标识会随 chunk 一起存储，检索结果中会显示 `[来源: xxx]`，也可按来源删除（见下方"管理知识库来源"）。

如需手动指定来源名称，使用 `--source` 参数：

```
/rag 这段文字内容... --source 会议纪要2026Q1
/rag /path/to/file.txt --source 产品手册v2
```

`--source` 可出现在参数任意位置，指定后会覆盖自动推断的来源。

> **Token 消耗**：文档内容的 Embedding 在本地完成，不调用 Claude API。但检索结果在用于对话时，会作为上下文传给 Claude，因此仍会消耗少量 input token。

---

### 检索知识库

主动查询知识库中的内容：

```
/rag-retrieve 你的问题
```

示例：

```
/rag-retrieve 公司的报销流程是什么？
```

> **Token 消耗**：检索操作本身不消耗 token。返回结果如果用于后续对话，会作为上下文占用少量 input token（约 200～600 token）。

---

### 开启自动检索模式

开启后，每次提交 prompt 时 Hook 会自动查询知识库并将结果注入为背景知识，无需手动检索：

```
/rag-mode on
```

关闭：

```
/rag-mode off
```

> **Token 消耗**：开启后每次对话会额外消耗用于注入检索到的背景知识的 token。关闭则无额外消耗。
>
> 该模式**持久化保存**（写入 `.claude/rag_mode` 标志文件），重启 Claude Code 后依然有效，直到显式执行 `/rag mode off`。
>
> 注意：自动检索由 Hook 驱动，不依赖 Claude 的上下文指令，不受对话长度或 compaction 影响。

---

### 查看知识库状态

查看当前知识库中存有多少条内容：

```
/rag-status
```

---

### 管理知识库来源

查看所有已入库的来源及各来源 chunk 数：

```
/rag-sources
```

按来源删除（只删除该来源的 chunk，不影响其他文档）：

```
/rag-source-delete <来源名称>
```

示例：

```
/rag-source-delete 手册.txt
```

---

### 清空知识库

删除所有已存入的文档（操作不可恢复，会要求二次确认）：

```
/rag-reset
```

> **Token 消耗**：不消耗 token。

---

## 命令汇总

| 命令 | 说明 | 额外 Token |
|------|------|-----------|
| `/rag <内容或链接>` | 存入文档 | 无 |
| `/rag-retrieve <问题>` | 主动检索 | 基于文本长度的近似 token 估算 |
| `/rag-mode on` | 开启自动检索（持久化） | 基于文本长度的近似 token 估算 |
| `/rag-mode off` | 关闭自动检索 | 无 |
| `/rag-status` | 查看状态及 chunk 总数 | 无 |
| `/rag-sources` | 列出所有来源及各来源 chunk 数 | 无 |
| `/rag-source-delete <名称>` | 按来源删除 chunk | 无 |
| `/rag-reset` | 清空全部知识库 | 无 |

---

## 服务管理

插件依赖一个后台服务。**首次使用必须运行 `./start.sh`**，它会完成依赖安装并向 Claude Code 注册自动启动配置。此后每次启动 Claude Code 会自动拉起服务，无需再手动操作。

> 注意：如果移动了项目目录，需重新运行 `./start.sh` 更新路径配置。

如需手动控制：

```bash
# 启动
./start.sh

# 停止
./stop.sh

# 查看运行日志
tail -f /tmp/claude-local-rag.log
```

---

## 配置（可选）

如需调整参数，编辑 `config.yaml`：

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `chunk.min_tokens` | `200` | 每段最小长度 |
| `chunk.max_tokens` | `400` | 每段最大长度 |
| `retrieve.top_k` | `3` | 每次检索返回的段落数 |

---

## 项目结构

```text
claude-local-rag/
 ├── server.py              # 后台服务
 ├── config.yaml            # 配置文件
 ├── requirements.txt       # Python 依赖
 ├── start.sh               # 一键安装脚本
 ├── stop.sh                # 停止服务脚本
 ├── index.bin              # 向量索引（运行后自动生成）
 ├── chunks.pkl             # 文档存储（运行后自动生成）
 └── .claude/
     ├── settings.json      # Hook 配置（关键词触发入库）
     └── commands/
         └── rag.md         # /rag 命令定义
```

---

## 常见问题

**Q：`/rag` 命令没有出现补全提示？**
重启 Claude Code，确保已运行 `./start.sh`。

**Q：提示"服务未启动"？**
运行 `./start.sh` 重新启动服务。

**Q：飞书文档无法读取？**
需要先安装并配置 lark-cli，参考[官方文档](https://www.feishu.cn/content/article/7623291503305083853)。

**Q：存入的文档检索结果不准确？**
文档质量直接影响检索效果，建议存入结构清晰、语义完整的段落，避免存入表格截图或扫描件文字。
