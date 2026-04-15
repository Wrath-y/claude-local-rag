开启或关闭 rerank 模式

输入参数：$ARGUMENTS（on 或 off）

根据参数调用切换接口：

```bash
# on
curl -s -X POST "http://127.0.0.1:8765/rerank/toggle?enabled=true"

# off
curl -s -X POST "http://127.0.0.1:8765/rerank/toggle?enabled=false"
```

- `on`：开启 rerank，首次开启会加载 cross-encoder 模型（~400MB，需等待约 10-60s）
- `off`：关闭 rerank，恢复 hybrid score 排序

告知用户当前 rerank 状态。服务未启动时提示运行 `./start.sh`。
