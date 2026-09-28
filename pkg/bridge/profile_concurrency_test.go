package bridge

import (
	"context"
	"github.com/GatewayJ/lark-bridge-agent-sdk/internal/app/configstore"
	"github.com/GatewayJ/lark-bridge-agent-sdk/internal/compat/apppaths"
	"testing"
)

func TestProfileClientConcurrency(t *testing.T) {
	for _, kind := range []configstore.AgentKind{configstore.AgentCodex, configstore.AgentClaude} {
		for _, tc := range []struct {
			name  string
			value any
			want  int
		}{
			{"default", nil, 10}, {"configured", float64(10), 10}, {"serial", float64(1), 1}, {"capped", float64(100), 50},
		} {
			t.Run(string(kind)+"/"+tc.name, func(t *testing.T) {
				cfg := configstore.RuntimeConfig{ProfileConfig: configstore.ProfileConfig{AgentKind: kind, Codex: &configstore.CodexConfig{InheritCodexHome: true}, Preferences: map[string]any{"maxConcurrentRuns": tc.value}, Workspaces: configstore.Workspaces{Default: t.TempDir()}}}
				client, _, err := profileBridgeClient(cfg, apppaths.Paths{ProfileDir: t.TempDir()}, nil)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(client.ReleaseCommandState)
				result, err := client.HandleCommand(context.Background(), CommandRequest{CommandText: "/status", ScopeID: "test", ChatID: "test", ActorID: "owner", SenderID: "owner", ChatMode: CommandChatModeP2P}, CommandOptions{})
				if err != nil {
					t.Fatal(err)
				}
				if result.Status == nil {
					t.Fatal("缺少状态结果")
				}
				if result.Status.Queue.Cap != tc.want {
					t.Fatalf("并发上限 = %d，预期 %d", result.Status.Queue.Cap, tc.want)
				}
			})
		}
	}
}
