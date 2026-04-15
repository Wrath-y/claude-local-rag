on/off 开启或关闭代码自动入库模式（持久化，重启后依然有效）

输入参数：$ARGUMENTS（on 或 off）

模式由 PostToolUse hook 驱动，通过标志文件 `.claude/rag_auto_index` 持久化：

- `on`：写入标志文件，此后 Claude 每次读取或修改源码文件时自动同步向量库
- `off`：删除标志文件，停止自动同步

hook 的行为规则：
- **Read**：读取源码文件后自动入库（跳过 > 100KB 的文件）
- **Edit / Write**：修改源码文件后自动删除旧 chunks 并重新入库，保持向量库与代码同步
- 仅处理源码文件（.py .ts .go .java .rs .cpp 等），跳过配置文件、日志、二进制等

切换方式：写入或删除 `.claude/rag_auto_index` 标志文件：

```bash
# on
touch .claude/rag_auto_index

# off
rm -f .claude/rag_auto_index
```

告知用户当前模式已切换为 on 或 off。
