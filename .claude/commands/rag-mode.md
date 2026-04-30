on/off 开启或关闭 RAG 自动检索模式（持久化，重启后依然有效）

输入参数：$ARGUMENTS（on 或 off）

模式由 hook 驱动，通过标志文件 `<Claude 工作目录>/.rag_mode` 持久化（按项目隔离）：
- `on`：写入标志文件，此后每次提交 prompt 时自动检索并注入背景知识
- `off`：删除标志文件，停止自动检索

hook 会自动检测本命令中的 `/rag-mode on` 或 `/rag-mode off` 关键词并完成切换。

告知用户当前模式已切换为 on 或 off。
