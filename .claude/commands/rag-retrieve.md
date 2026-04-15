从向量库检索与问题相关的内容

输入参数：$ARGUMENTS

POST 问题到 `/retrieve`：

```bash
curl -s -X POST http://127.0.0.1:8765/retrieve \
  -H "Content-Type: application/json" \
  -d "{\"text\": \"$ARGUMENTS\"}"
```

展示返回的相关 chunk 列表（含来源标识）。

服务未启动时提示用户运行 `./start.sh`。
