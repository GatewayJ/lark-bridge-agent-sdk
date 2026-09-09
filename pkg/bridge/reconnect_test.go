package bridge

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestProfileBridgeReconnectCommandKeepsReceivingMessages(t *testing.T) {
	root := t.TempDir()
	writeProfileBridgeTestConfig(t, root, filepath.Join(root, "workspace"))
	transport := &reconnectTestTransport{FakeLarkTransport: NewFakeLarkTransport(LarkBotIdentity{OpenID: "ou_bot", Name: "Bridge Bot"})}
	instance, _, err := NewProfileBridge(context.Background(), ProfileBridgeOptions{
		Home:                    root,
		Profile:                 "codex",
		SkipCheckLarkCLI:        true,
		SkipAgentAvailability:   true,
		DisableDefaultLogger:    true,
		DisableDefaultTelemetry: true,
		LarkTransport:           transport,
		InitialOwnerOpenID:      "ou_user",
		SecretsGetterCommand:    "/bin/bridge",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := instance.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = instance.Shutdown(context.Background()) })

	for idx, command := range []string{"/reconnect", "/reconnect --wait", "/status"} {
		err := transport.Emit(transport.connectionCtx, LarkIncomingEvent{
			Kind: LarkEventMessage,
			Message: &LarkMessageInput{
				MessageID: fmt.Sprintf("om_command_%d", idx),
				ChatID:    "oc_dm",
				ChatType:  LarkChatTypeP2P,
				Sender:    LarkActor{OpenID: "ou_user"},
				Content:   command,
			},
		})
		if err != nil {
			t.Fatalf("%s returned error: %v", command, err)
		}
		messages := transport.SentMessageSnapshot()
		if len(messages) != idx+1 {
			t.Fatalf("%s: got %d replies, want %d", command, len(messages), idx+1)
		}
		if strings.Contains(messages[idx].Content.Markdown, "失败") {
			t.Fatalf("%s failed: %s", command, messages[idx].Content.Markdown)
		}
		if err := transport.connectionCtx.Err(); err != nil {
			t.Fatalf("%s left the connection canceled: %v", command, err)
		}
	}
	if transport.connects != 3 || transport.disconnects != 2 {
		t.Fatalf("connects=%d disconnects=%d, want 3 and 2", transport.connects, transport.disconnects)
	}
	status, err := instance.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !status.Lark.Started || status.Runtime == nil || !status.Runtime.Adapter.Connected || len(status.Runtime.Processes) != 1 {
		t.Fatalf("status after reconnect = %#v", status)
	}
}

func TestBridgeReconnectRetainsTenantAndRejectsConfigurationChanges(t *testing.T) {
	transport := &reconnectTestTransport{FakeLarkTransport: NewFakeLarkTransport(LarkBotIdentity{OpenID: "ou_bot"})}
	instance, err := New(Options{
		Home:          t.TempDir(),
		Profile:       "codex",
		AppID:         "cli_lark",
		Tenant:        RuntimeTenantLark,
		AgentKind:     RuntimeAgentCodex,
		LarkTransport: transport,
		LarkIntake:    LarkIntakeSinkFunc(func(context.Context, LarkNormalizedEvent) error { return nil }),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := instance.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = instance.Shutdown(ctx) })
	if err := instance.Reconnect(ctx, RuntimeReconnectOptions{}); err != nil {
		t.Fatal(err)
	}
	before, err := instance.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if before.Runtime.Entry.Tenant != RuntimeTenantLark {
		t.Fatalf("tenant = %q, want lark", before.Runtime.Entry.Tenant)
	}
	for _, options := range []RuntimeReconnectOptions{
		{AppID: "cli_other"},
		{Tenant: RuntimeTenantFeishu},
		{ConfigPath: filepath.Join(t.TempDir(), "other.json")},
	} {
		if err := instance.Reconnect(ctx, options); !errors.Is(err, ErrBridgeReconnectConfigChanged) {
			t.Fatalf("reconnect with %#v error = %v, want ErrBridgeReconnectConfigChanged", options, err)
		}
	}
	after, err := instance.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !after.Lark.Started || *after.Runtime.Entry != *before.Runtime.Entry || len(after.Runtime.Processes) != 1 || after.Runtime.Processes[0] != before.Runtime.Processes[0] {
		t.Fatalf("rejected configuration change altered runtime: %#v", after.Runtime)
	}
	if transport.connects != 2 || transport.disconnects != 1 {
		t.Fatalf("rejected configuration change touched transport: connects=%d disconnects=%d", transport.connects, transport.disconnects)
	}
}

// Incoming events use the connection's context, as they do in the OAPI SDK.
type reconnectTestTransport struct {
	*FakeLarkTransport
	connectionCtx    context.Context
	connectionCancel context.CancelFunc
	connects         int
	disconnects      int
}

func (t *reconnectTestTransport) Connect(ctx context.Context, handler LarkTransportHandler) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := t.FakeLarkTransport.Connect(ctx, handler); err != nil {
		return err
	}
	t.connectionCtx, t.connectionCancel = context.WithCancel(ctx)
	t.connects++
	return nil
}

func (t *reconnectTestTransport) Disconnect(ctx context.Context) error {
	if err := t.FakeLarkTransport.Disconnect(ctx); err != nil {
		return err
	}
	t.connectionCancel()
	t.disconnects++
	return nil
}

func (t *reconnectTestTransport) SendMessage(ctx context.Context, req LarkSendMessageRequest) (LarkSendResult, error) {
	if err := ctx.Err(); err != nil {
		return LarkSendResult{}, err
	}
	return t.FakeLarkTransport.SendMessage(ctx, req)
}
