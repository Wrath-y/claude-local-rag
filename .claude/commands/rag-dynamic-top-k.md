on/off 开启或关闭动态 top_k 模式

输入参数：$ARGUMENTS（on 或 off）

开启后，每次检索前 Hook 会读取当前对话 transcript 估算已用 token 数，
服务端据此自动缩减返回的 chunk 数，避免上下文窗口被撑满。

根据参数调用切换接口：

```bash
# on
curl -s -X POST "http://127.0.0.1:8765/retrieve/dynamic-top-k?enabled=true"

# off
curl -s -X POST "http://127.0.0.1:8765/retrieve/dynamic-top-k?enabled=false"
```

- `on`：开启动态 top_k，上下文接近上限时自动降低返回数量（最低 1）
- `off`：关闭，始终使用 config.yaml 中配置的固定 `top_k` 值

告知用户当前 dynamic_top_k 状态。服务未启动时提示运行 `./start.sh`。
