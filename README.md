# lark-bridge-agent-sdk

Go SDK for connecting Feishu/Lark PersonalAgent bots to local coding agents such as Codex CLI and Claude Code.

[中文 README](./README.zh.md)

## What It Provides

- `pkg/bridge`: public Go facade for profile bootstrap, in-process bridge startup, service control, Lark/Feishu transport wiring, card rendering, command handling, prompt helpers, telemetry, and lark-cli projection/preflight helpers.
- `cmd/lark-channel-bridge`: Go CLI entrypoint for profile/service workflows and first-run setup helpers.
- `examples/codex-feishu`: minimal Go host that connects a Feishu/Lark app to local Codex.

## Install

```sh
go get github.com/GatewayJ/lark-bridge-agent-sdk/pkg/bridge@latest
```

Optional CLI install:

```sh
go install github.com/GatewayJ/lark-bridge-agent-sdk/cmd/lark-channel-bridge@latest
```

The module targets Go 1.23 or newer.

## Minimal Codex + Feishu Example

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

For a runnable version with environment variables and signal handling, see [examples/codex-feishu](./examples/codex-feishu).

## Documentation

- [Go SDK usage](./docs/go-sdk-usage.md)
- [pkg/bridge facade](./docs/pkg/bridge.md)

## Validate

```sh
go test ./...
```

