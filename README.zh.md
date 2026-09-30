# lark-bridge-agent-sdk

用于把飞书 / Lark PersonalAgent 机器人接到本地 Codex CLI、Claude Code 等编程 agent 的 Go SDK。

[English README](./README.md)

## 提供什么

- `pkg/bridge`：公开 Go SDK facade，覆盖 profile 初始化、进程内 bridge 启动、系统服务控制、飞书 / Lark transport、卡片渲染、命令处理、提示词构造、telemetry、lark-cli projection / preflight 等能力。
- `cmd/lark-channel-bridge`：Go CLI 入口，用于 profile / service 工作流和首次配置辅助流程。
- `examples/codex-feishu`：最小 Go 程序示例，把飞书 / Lark app 接到本地 Codex。

普通接入方建议优先使用稳定的 profile / service facade：
`BootstrapProfileConfig`、`NewProfileBridge`、`StartProfileService` 和
`NewProfileServiceController`。更底层的命令、卡片、配置、runtime、fake
transport 与 lark-cli helper 在 v1 前属于高级 / 实验 API。

## 来源与致谢

这个 SDK 从 JavaScript 优先的
[lark-coding-agent-bridge](https://github.com/GatewayJ/lark-coding-agent-bridge)
项目中拆出，并尽量保持它已经验证过的 bridge 语义、提示词契约、飞书 /
Lark 运行行为、卡片和 telemetry 兼容性。感谢 JS 版本先把产品形态和边界跑通，
这个 Go SDK 以它作为兼容基线。

## 安装

```sh
go get github.com/GatewayJ/lark-bridge-agent-sdk/pkg/bridge@latest
```

如果需要 CLI：

```sh
go install github.com/GatewayJ/lark-bridge-agent-sdk/cmd/lark-channel-bridge@latest
```

当前 module 目标版本是 Go 1.23 或更高。

## 聊天中的配置与状态

管理员发送 `/config` 后，可以在“默认工作目录（cwd）”输入已存在的绝对路径或 `~/目录`。提交后保存到当前 profile，并从后续任务开始生效。通过 `/cd` 设置了独立目录的会话继续使用该目录。

发送 `/status` 可以查看当前工作目录，以及当前 Codex 账户的额度使用比例、重置时间和可用的 credits 信息。重置时间标明 UTC。查询失败时显示“暂时无法获取”，其他状态仍然显示。

过程消息发送失败后，bridge 继续接收任务结果并发送最终回复。最终回复遇到网络错误或限流时最多尝试三次，同一段内容重试使用相同消息标识。已有过程消息会尝试结束，并记录失败原因。

## 最小 Codex + 飞书例子

```go
package main

import (
	"context"
	"log"

	bridge "github.com/GatewayJ/lark-bridge-agent-sdk/pkg/bridge"
)

func main() {
	ctx := context.Background()

	_, err := bridge.BootstrapProfileConfig(bridge.BootstrapProfileOptions{
		RootDir:          "./.lark-channel",
		Profile:          "codex",
		AgentKind:        bridge.ConfigAgentCodex,
		AppID:            "cli_xxx",
		AppSecret:        bridge.SecretReference(bridge.SecretRef{Source: bridge.SecretSourceEnv, ID: "LARK_APP_SECRET"}),
		Tenant:           bridge.LarkCLITenantFeishu,
		DefaultWorkspace: "./workspace",
		Access: bridge.ConfigProfileAccess{
			AllowedUsers: []string{"ou_xxx"},
			Admins:       []string{"ou_xxx"},
		},
	})
	if err != nil {
		log.Fatal(err)
	}

	instance, _, err := bridge.NewProfileBridge(ctx, bridge.ProfileBridgeOptions{
		Home:               "./.lark-channel",
		Profile:            "codex",
		InitialOwnerOpenID: "ou_xxx",
	})
	if err != nil {
		log.Fatal(err)
	}

	if err := instance.Start(ctx); err != nil {
		log.Fatal(err)
	}
	defer instance.Shutdown(context.Background())

	select {}
}
```

把 `ou_xxx` 替换成 app 创建人或首个授权用户的 `open_id`。空 access 列表会按
deny-by-default 处理。带环境变量、信号处理和 owner/allowed users 的可运行版本见
[examples/codex-feishu](./examples/codex-feishu)。

## 文档

开启 CoT 后，过程播报和工具记录保留在 CoT，聊天中只发送单独的最终答案。
授权、确认和补充问题会立即作为普通消息显示，不依赖抽屉入口；发送提示不会
结束运行或关闭 CoT。OAuth 仍在同一轮前台等待，授权完成后继续。自定义适配器
可使用 `Event.Phase` 和 `EventUserAction`，详见 [消息协议](./docs/pkg/bridge.md)。

- [Go SDK 使用说明](./docs/go-sdk-usage.md)
- [pkg/bridge facade](./docs/pkg/bridge.md)

## 验证

```sh
go test ./...
```
