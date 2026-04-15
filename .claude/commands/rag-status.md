查看 RAG 服务状态及当前 chunk 总数

GET `/health`：

```bash
curl -s http://127.0.0.1:8765/health
```

展示服务状态及当前 chunk 总数。

服务未启动时提示用户运行 `./start.sh`。
