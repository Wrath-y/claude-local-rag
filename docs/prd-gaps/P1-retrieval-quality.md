# RAG 生产化 P1 — 检索质量工程化

> 优先级：P1（生产期持续优化）
> 期望交付：P0 落地后立即启动，3-4 个迭代周期
> 编写日期：2026-04-30

## 背景

当前检索路径：embedding 余弦相似度 + BM25 混合分，`top_k` 默认 3，可选开启 rerank。没有离线评测集，修改配置后无法量化收益，也无回归防护；分块为固定 200-400 token 窗口，对 markdown 标题/代码块/表格等结构不敏感。

## 目标

- 任意检索参数变更可量化评估效果（recall@k、nDCG、MRR）
- 有回归测试门禁，低于基线自动拦截
- 分块对结构化文档（markdown、代码、PDF 表格）有感知，chunk 与语义边界对齐
- 查询侧具备基础的重写与扩展能力

---

## 需求列表

### 需求 1：离线评测集与评测脚本

**模块**：评测

**核心功能**：

- 定义评测集格式（JSONL）：`{query, expected_source, expected_chunk_ids[], tags[]}`
- 新增 `eval/` 目录与初始种子集（≥ 50 条，覆盖中英文、代码、长文档）
- `scripts/run_eval.py`：执行评测集，输出 recall@{1,3,5,10}、MRR、nDCG@10
- 结果写入 `eval/results/<commit>-<timestamp>.json`，支持两次结果 diff

**验收**：

- 本地一条命令可复现评测、输出报告
- 同一 commit 两次运行结果一致（embedding 缓存有效）

**涉及文件**：新增 `eval/`、`scripts/run_eval.py`

---

### 需求 2：评测门禁与基线管理

**模块**：评测/CI

**核心功能**：

- 提交 PR 时运行评测脚本，与 `eval/baseline.json` 对比
- 核心指标下跌超过阈值（默认 recall@3 下跌 >2%）自动 fail
- 支持 `eval/baseline.json` 更新流程：必须附 PR 说明原因
- 评测失败时输出 diff 报告：哪些 query 回退、命中的 chunk 变化

**验收**：

- CI 中能单独运行评测；故意引入劣化修改会被拦截

**涉及文件**：新增 `.github/workflows/eval.yml`、`scripts/compare_eval.py`

---

### 需求 3：结构感知分块

**模块**：索引/分块

**背景**：固定 token 窗口会把代码块、表格、标题层级切碎，影响检索与展示。

**核心功能**：

- Markdown：按标题层级切分，保留标题作为 chunk 前缀；代码块整块保留
- PDF：保留页号与表格为独立 chunk
- 纯文本：沿用固定窗口但按段落边界优先
- chunk 元数据新增：`structure_type`（heading/code/table/paragraph）、`heading_path`、`page`

**验收**：

- 对同一文档，切出的 chunk 数与固定窗口差异合理（代码块不被切）
- 评测集 recall@3 相比旧分块方式提升 ≥ 3%

**涉及文件**：`server.py` 中 `chunk_text`，新增 `indexing/chunker_*.py`

---

### 需求 4：查询改写与扩展

**模块**：检索

**核心功能**：

- 基础模式：查询前缀/后缀清洗（去掉 "请问"、"帮我看看" 等噪声）
- HyDE 模式（可选）：先用本地小模型生成假设答案再检索
- 多查询扩展：同义词词表 + 可选 LLM 生成 2-3 条改写，并行检索后合并去重
- 所有模式由 `config.yaml` 切换，默认关闭

**验收**：

- 查询 "Redis 穿透" 能召回包含 "缓存击穿/布隆过滤器" 的 chunk
- 扩展开启后评测 recall@3 提升 ≥ 5%（允许 latency 增加 <100ms）

**涉及文件**：`server.py`，新增 `retrieval/query_rewrite.py`

---

### 需求 5：检索可解释性增强

**模块**：可观测性

**核心功能**：

- `/retrieve` 响应中每条 chunk 返回：向量得分、BM25 得分、最终得分、rerank 得分（若启用）
- 新增 `/retrieve/explain` 端点返回更多信息：检索分支耗时、参与召回的 query 列表
- `/rag-verbose on` 输出格式规范化为 JSON，可被下游工具消费

**验收**：

- 前端/CLI 可展示为什么某个 chunk 排第一
- 日志可直接导入评测分析

**涉及文件**：`server.py`

---

### 需求 6：embedding 模型升级与批量重索引

**模块**：索引

**核心功能**：

- 支持在 `config.yaml` 切换 embedding 模型，启动时检测维度不匹配触发「重索引」流程
- `/index/reindex`：后台任务，分批从 chunks.pkl 重新 embed，期间服务只读降级
- 重索引完成后原子切换索引文件，失败保留旧索引
- 进度通过 `/metrics` 暴露：`rag_reindex_progress_ratio`

**验收**：

- 从 bge-small-zh 切换到 bge-m3 可平滑完成，服务不中断检索
- 重索引中途失败可重入、不损坏数据

**涉及文件**：`server.py`，新增 `indexing/reindex.py`

---

### 需求 7：rerank 治理

**模块**：检索

**核心功能**：

- rerank 模型加载改为按需懒加载 + LRU 保留
- 支持按查询标签选择不同 rerank 模型（中文/代码/通用）
- rerank 失败自动回退到一阶段得分，不阻塞响应
- 记录 rerank 前后 top-k 变化率指标

**验收**：

- 首次启用 rerank 不阻塞其他请求
- rerank 模型下载失败时服务可用（降级）

**涉及文件**：`server.py`

---

## 不在本期范围

- Agentic 多跳检索（见 P3）
- 多模态（见 P3）
