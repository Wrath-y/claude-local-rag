# 生产可用 RAG — 差距分析与 PRD 索引

> 编写日期：2026-04-30
> 基准版本：当前 main（单用户本地 FAISS + pickle，Hook 注入模式）
> 用途：将本项目从「个人本地工具」推进到「团队/企业生产可用」所需补齐的能力清单。

## 现状快照

| 维度 | 现状 | 生产要求 |
|------|------|----------|
| 存储 | pickle + FAISS 内存加载，单进程 | 并发安全、持久化、可备份/回滚 |
| 认证 | 无（localhost bind） | 多用户/多租户、RBAC |
| 可观测 | 单一日志文件 | 指标、追踪、评测、告警 |
| 数据管道 | 手动入库，无增量检测 | 自动同步、版本化、去重 |
| 检索质量 | 向量+BM25 混合，可选 rerank | 离线评测、回归测试、查询改写 |
| 分块 | 固定 token 窗口 | 结构感知（markdown/代码/表格） |
| 生命周期 | 手动 /rag-update | 自动刷新、TTL、失效通知 |
| 模型治理 | 硬编码单模型 | 可切换、A/B、批量重索引 |

## 文档组织

按优先级拆为 4 份独立 PRD，每份均可作为 `/prd-breakdown <路径>` 的输入独立拆解。

| 文件 | 优先级 | 定位 | 典型场景 |
|------|--------|------|----------|
| [P0-production-readiness.md](./P0-production-readiness.md) | P0 | 稳定性/可靠性基线 | 上生产前必须修复的阻断项 |
| [P1-retrieval-quality.md](./P1-retrieval-quality.md) | P1 | 检索效果与工程化 | 效果评测、质量回归 |
| [P2-enterprise-features.md](./P2-enterprise-features.md) | P2 | 多用户/企业能力 | 团队协作、权限、审计 |
| [P3-advanced-capabilities.md](./P3-advanced-capabilities.md) | P3 | 进阶能力 | Agentic 检索、多模态、知识图谱 |

## 推荐实施顺序

1. **第一批**：P0 的「并发安全 + 持久化 + 备份恢复 + 健康监控」——保证现有单用户形态不丢数据、不静默故障。
2. **第二批**：P1 的「离线评测集 + 结构感知分块」——让检索质量可度量、可回归。
3. **第三批**：P2 的「鉴权 + 多租户」——打开团队/企业使用场景。
4. **第四批**：P1 剩余 + P3 按需选做。

## 使用方式

对任一 PRD 生成 openspec 计划：

```
/prd-breakdown docs/prd-gaps/P0-production-readiness.md
```

`/prd-breakdown` 会自动拆分为独立需求、输出技术方案，并按需逐个生成 openspec 开发计划。
