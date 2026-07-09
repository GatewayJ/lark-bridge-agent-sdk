# lark-bridge-agent-sdk

用于把飞书 / Lark PersonalAgent 机器人接到本地 Codex CLI、Claude Code 等编程 agent 的 Go SDK。

[English README](./README.md)

## 提供什么

- `pkg/bridge`：公开 Go SDK facade，覆盖 profile 初始化、进程内 bridge 启动、系统服务控制、飞书 / Lark transport、卡片渲染、命令处理、提示词构造、telemetry、lark-cli projection / preflight 等能力。
- `cmd/lark-channel-bridge`：Go CLI 入口，用于 profile / service 工作流和首次配置辅助流程。
- `examples/codex-feishu`：最小 Go 程序示例，把飞书 / Lark app 接到本地 Codex。

## 安装

```sh
go get github.com/GatewayJ/lark-bridge-agent-sdk/pkg/bridge@latest
```

如果需要 CLI：

```sh
go install github.com/GatewayJ/lark-bridge-agent-sdk/cmd/lark-channel-bridge@latest
```

当前 module 目标版本是 Go 1.23 或更高。

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
	})
	if err != nil {
		log.Fatal(err)
	}

	instance, _, err := bridge.NewProfileBridge(ctx, bridge.ProfileBridgeOptions{
		Home:    "./.lark-channel",
		Profile: "codex",
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

带环境变量、信号处理和 owner/allowed users 的可运行版本见 [examples/codex-feishu](./examples/codex-feishu)。

## 文档

- [Go SDK 使用说明](./docs/go-sdk-usage.md)
- [pkg/bridge facade](./docs/pkg/bridge.md)

## 验证

```sh
go test ./...
```

