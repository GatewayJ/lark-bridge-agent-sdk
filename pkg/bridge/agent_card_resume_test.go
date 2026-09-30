package bridge

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestAgentCardCallbackResumesCodexSessionAfterBridgeRestart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake uses POSIX sh")
	}
	ctx := context.Background()
	root, cwd := t.TempDir(), t.TempDir()
	request := `<bridge_card>{"id":"app","card":{"schema":"2.0","body":{"elements":[{"tag":"form","name":"app","elements":[{"tag":"input","name":"app_name"},{"tag":"button","name":"submit","form_action_type":"submit"}]}]}}}</bridge_card>`
	message, _ := json.Marshal(map[string]any{"type": "item.completed", "item": map[string]any{"type": "agent_message", "text": request}})
	binary := writeBridgeFakeCodex(t, `
if [ -f issued ]; then
  cat > resumed.prompt
  printf '%s\n' "$@" > resumed.args
  printf '%s\n' '{"type":"thread.started","thread_id":"thread-card"}' '{"type":"item.completed","item":{"type":"agent_message","text":"submitted"}}' '{"type":"turn.completed"}'
else
  cat > initial.prompt
  touch issued
  printf '%s\n' '{"type":"thread.started","thread_id":"thread-card"}'
  cat <<'CARD_EVENT'
`+string(message)+`
CARD_EVENT
  printf '%s\n' '{"type":"turn.completed"}'
fi
`)
	start := func() (*Bridge, *FakeLarkTransport) {
		client, err := NewCodexClient(CodexClientOptions{Binary: binary, ProfileStateDir: filepath.Join(root, "profiles", "codex"), DefaultWorkingDir: cwd, SessionStorePath: filepath.Join(root, "sessions.json"), SessionCatalogPath: filepath.Join(root, "catalog.json"), AllowedUsers: []string{"ou_user"}})
		if err != nil {
			t.Fatal(err)
		}
		auth, err := NewCallbackAuth(CallbackAuthOptions{Keys: []CallbackKey{{Version: 1, Secret: "test-key"}}, NonceStorePath: filepath.Join(root, "nonces.json")})
		if err != nil {
			t.Fatal(err)
		}
		transport := NewFakeLarkTransport(LarkBotIdentity{OpenID: "ou_bot"})
		b, err := New(Options{Home: root, Profile: "codex", Client: client, LarkTransport: transport, LarkManaged: LarkManagedOptions{MessageQuietPeriod: time.Millisecond, MessageReplyMode: LarkReplyMarkdown, CallbackAuth: auth}, AppID: "cli_test", Tenant: RuntimeTenantFeishu})
		if err != nil {
			t.Fatal(err)
		}
		if err := b.Start(ctx); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = b.Shutdown(context.Background()) })
		return b, transport
	}
	b, transport := start()
	emitManagedTestMessage(t, ctx, transport, "om_request", "collect application settings")
	cards := waitBridgeSentCards(t, transport, 1)
	value := findBridgeCallbackValue(t, cards[0].Card)
	// 等待首次执行保存会话，再关闭 bridge。
	deadline := time.Now().Add(bridgeTestWaitTimeout())
	for {
		data, _ := os.ReadFile(filepath.Join(root, "catalog.json"))
		if strings.Contains(string(data), "thread-card") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("session was not saved")
		}
		time.Sleep(time.Millisecond)
	}
	if err := b.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	_, transport = start()
	if err := transport.Emit(ctx, LarkIncomingEvent{Kind: LarkEventCardAction, CardAction: &LarkCardActionInput{EventID: "evt_submit", MessageID: "om_card", ChatID: "oc_dm", ChatType: LarkChatTypeP2P, Operator: LarkActor{OpenID: "ou_user"}, ActionValue: value, FormValue: map[string]any{"environment": "production", "app_name": "example-app"}}}); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(bridgeTestWaitTimeout())
	for {
		data, _ := os.ReadFile(filepath.Join(cwd, "resumed.args"))
		if strings.Contains(string(data), "thread-card") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("resume timed out; messages=%+v", transport.SentMessageSnapshot())
		}
		time.Sleep(time.Millisecond)
	}
	prompt := readBridgeFile(t, filepath.Join(cwd, "resumed.prompt"))
	if !strings.Contains(prompt, "[card-click]") || !strings.Contains(prompt, "production") || !strings.Contains(prompt, "example-app") {
		t.Fatalf("callback missing from prompt: %s", prompt)
	}
	args := readBridgeFile(t, filepath.Join(cwd, "resumed.args"))
	if !strings.Contains(args, "resume\n") || !strings.Contains(args, "thread-card\n") {
		t.Fatalf("session not resumed: %s", args)
	}
}
