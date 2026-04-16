将当前向量库导出为 zip 备份文件，可用于迁移或备份

输入参数：$ARGUMENTS（可选，指定保存路径，默认 ~/rag_backup.zip）

**执行导出**

```bash
curl -s http://127.0.0.1:8765/export -o <保存路径>
```

- 若用户未提供路径，默认保存到 `~/rag_backup.zip`
- 导出成功后，告知用户文件路径和备份包含的 chunk 数（可再调 `/health` 获取）

服务未启动时提示运行 `./start.sh`。
