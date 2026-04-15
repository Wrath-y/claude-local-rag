on/off 开启或关闭检索可观测性日志

输入参数：$ARGUMENTS（on 或 off）

根据参数调用切换接口：

```bash
# on
curl -s -X POST "http://127.0.0.1:8765/retrieve/verbose?enabled=true"

# off
curl -s -X POST "http://127.0.0.1:8765/retrieve/verbose?enabled=false"
```

- `on`：开启详细日志，每次检索在服务端输出候选数量、得分明细、rerank 排序等信息
- `off`：关闭详细日志，检索静默执行

告知用户当前 verbose 状态。服务未启动时提示运行 `./start.sh`。
