按来源名称删除向量库中对应的 chunk（不影响其他来源）

输入参数：$ARGUMENTS（来源名称）

1. 向用户确认删除操作（不可恢复）
2. 用户确认后执行：

```bash
curl -s -X DELETE "http://127.0.0.1:8765/source?name=$ARGUMENTS"
```

3. 告知用户删除了多少个 chunk。

服务未启动时提示用户运行 `./start.sh`。
