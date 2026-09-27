`scripts/eval_bridge_prompt.py` 使用本机 Codex CLI 比较两份固定提示，要求显式指定相同模型和推理强度。两份提示在同一临时目录交替运行，每次使用独立会话。

```sh
git show <baseline-commit>:internal/presentation/prompt/bridge_system_prompt.txt > /tmp/bridge-baseline.txt
python3 scripts/eval_bridge_prompt.py \
  --baseline /tmp/bridge-baseline.txt \
  --prompt internal/presentation/prompt/bridge_system_prompt.txt \
  --model <model> --effort <effort> --repeats 5 \
  --output /tmp/bridge-evaluation.json
```

验证群聊授权限制、无法签名时的选择方式、补充问题后的结束条件、即时授权提示与前台等待、恢复会话后授权成功的身份配置顺序。模型返回计划中的命令和消息，不执行飞书操作；出现工具调用会使当前提示的验证失败。

输出记录每次判断结果、耗时和 CLI 报告的 token 使用量。输入 token 包含 CLI 自带的上下文，缓存 token 单独记录。脚本不修改本机模型配置，但沿用认证和用户配置；两组测试必须在同一次运行中完成，以保持这些条件一致。

此测试验证模型对规则的理解。实际工具执行、真实浏览器授权、飞书消息投递和真实 resume 由其他功能测试或人工验证负责。不能根据这组离线判断宣称全部任务完成率提高。

2026-09-27 使用 `codex-cli 0.157.0`、`gpt-6-astra`、`medium` 完成五轮对照，旧提示取自 `866fc78`，每轮包括五个独立场景。两组均未调用工具，缓存输入 token 均为零。

| 提示 | 通过场景 | 平均输入 token | 耗时中位数（秒） | 平均耗时（秒） |
| --- | --- | --- | --- | --- |
| baseline | 25/25 | 22051 | 24.27 | 25.04 |
| current | 25/25 | 20905 | 26.53 | 26.98 |

本次对照的新提示输入 token 减少约 5.2%，该比例包含 CLI 自带上下文。两组规则判断均通过；样本数量有限，耗时结果不足以证明执行速度改善。
