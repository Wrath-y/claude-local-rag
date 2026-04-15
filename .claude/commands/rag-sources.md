列出向量库中所有来源及各来源的 chunk 数

GET `/sources`：

```bash
curl -s http://127.0.0.1:8765/sources
```

以表格形式展示各来源名称及 chunk 数。

服务未启动时提示用户运行 `./start.sh`。
