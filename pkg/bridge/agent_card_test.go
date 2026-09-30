package bridge

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GatewayJ/lark-bridge-agent-sdk/internal/app/cardauth"
	appcot "github.com/GatewayJ/lark-bridge-agent-sdk/internal/app/cotpresenter"
	"github.com/GatewayJ/lark-bridge-agent-sdk/internal/app/impresenter"
	appintake "github.com/GatewayJ/lark-bridge-agent-sdk/internal/app/intake"
	"github.com/GatewayJ/lark-bridge-agent-sdk/internal/compat/agentoutput"
	agentport "github.com/GatewayJ/lark-bridge-agent-sdk/internal/ports/agent"
)

func TestManagedAgentCardSubmittedAfterRunAndRestart(t *testing.T) {
	for _, cot := range []appcot.Mode{appcot.ModeOff, appcot.ModeBrief} {
		t.Run(string(cot), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "nonces.json")
			newAuth := func() *CallbackAuth {
				a, err := NewCallbackAuth(CallbackAuthOptions{Keys: []CallbackKey{{Version: 1, Secret: "test-key"}}, NonceStorePath: path})
				if err != nil {
					t.Fatal(err)
				}
				return a
			}
			transport := NewFakeLarkTransport(LarkBotIdentity{})
			intake := &managedLarkIntake{transport: transport, callbackAuth: newAuth(), callbackTTL: time.Hour, cotClient: wrapInternalLarkCOTClient(transport)}
			message := `<bridge_card>{"id":"application","card":{"schema":"2.0","body":{"elements":[{"tag":"form","name":"app","elements":[{"tag":"input","name":"app_name","required":true},{"tag":"select_static","name":"environment","required":true,"options":[{"text":{"tag":"plain_text","content":"Test"},"value":"test"},{"text":{"tag":"plain_text","content":"Production"},"value":"production"}]},{"tag":"button","name":"submit","form_action_type":"submit","text":{"tag":"plain_text","content":"Submit"}}]}]}}}</bridge_card>`
			event, ok := agentoutput.UserAction(message)
			if !ok {
				t.Fatal("card protocol was not recognized")
			}
			metadata := RunMetadata{RunID: "completed-run", ScopeID: "oc_group", PolicyFingerprint: "fp"}
			intake.registerActiveRun(metadata.ScopeID, metadata)
			_, err := intake.presentRun(context.Background(), managedPresentInput{
				Run:    bridgeTestRun{events: []agentport.AgentEvent{event, event, {Type: agentport.EventDone}}},
				ChatID: "oc_group", ReplyMode: impresenter.ReplyMarkdown, COTMessages: cot, RunID: metadata.RunID, ScopeID: metadata.ScopeID,
				SendAgentCard: intake.agentCardSender(metadata, appintake.MessageInput{ChatID: "oc_group", Sender: appintake.Actor{OpenID: "ou_operator"}}, impresenter.SendOptions{}),
			})
			if err != nil {
				t.Fatal(err)
			}
			intake.unregisterActiveRun(metadata.ScopeID, metadata.RunID)
			cards := transport.SentCardSnapshot()
			if len(cards) != 1 {
				t.Fatalf("cards=%d", len(cards))
			}
			card := cards[0].Card
			body := card["body"].(map[string]any)
			form := body["elements"].([]any)[0].(map[string]any)
			button := form["elements"].([]any)[2].(map[string]any)
			value := button["behaviors"].([]any)[0].(map[string]any)["value"].(map[string]any)
			if value["__bridge_cb"] != true || value["bridge_token"] == "" {
				t.Fatal("missing callback signature")
			}
			// 重建认证对象，验证待提交请求不依赖之前的执行记录。
			intake = &managedLarkIntake{callbackAuth: newAuth()}
			queue := &captureCardQueue{}
			d := NewCardActionDispatcher(CardActionDispatcherOptions{PendingVerifier: intake.pendingCardVerifier(), ActiveRuns: intake, Enqueuer: queue})
			input := testCardAction(value, map[string]any{"environment": "production", "app_name": "example-app"})
			result, err := d.Dispatch(context.Background(), input)
			if err != nil || result.Outcome != CardDispatchEnqueued || len(queue.events) != 1 {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			text := queue.events[0].Message.Content
			if !strings.Contains(text, `"environment":"production"`) || !strings.Contains(text, `"app_name":"example-app"`) || strings.Contains(text, "bridge_token") {
				t.Fatalf("callback=%s", text)
			}
			if _, err := d.Dispatch(context.Background(), input); err == nil {
				t.Fatal("duplicate submission accepted")
			}
		})
	}
}

func TestManagedAgentCardSendFailureCancelsPendingCallback(t *testing.T) {
	auth := newTestCallbackAuth(t, "failed-card")
	transport := &agentCardFailTransport{FakeLarkTransport: NewFakeLarkTransport(LarkBotIdentity{})}
	intake := &managedLarkIntake{transport: transport, callbackAuth: auth, callbackTTL: time.Hour}
	var card map[string]any
	_ = json.Unmarshal([]byte(`{"schema":"2.0","body":{"elements":[{"tag":"button","behaviors":[{"type":"callback","value":{"choice":"a"}}]}]}}`), &card)
	err := intake.agentCardSender(RunMetadata{RunID: "run", ScopeID: "chat", PolicyFingerprint: "fp"}, appintake.MessageInput{ChatID: "chat", Sender: appintake.Actor{OpenID: "user"}}, impresenter.SendOptions{})(context.Background(), card)
	if err == nil {
		t.Fatal("send failure hidden")
	}
	button := transport.card["body"].(map[string]any)["elements"].([]any)[0].(map[string]any)
	token := button["behaviors"].([]any)[0].(map[string]any)["value"].(map[string]any)["bridge_token"].(string)
	if result := auth.auth.VerifyPending(token, cardauth.VerifyExpected{Scope: "chat", ChatID: "chat", OperatorOpenID: "user", Action: "agent_callback"}, map[string]any{"choice": "a"}); result.OK {
		t.Fatal("failed delivery remained pending")
	}
}

type agentCardFailTransport struct {
	*FakeLarkTransport
	card map[string]any
}

func (t *agentCardFailTransport) SendCard(_ context.Context, req LarkSendCardRequest) (LarkSendResult, error) {
	t.card = req.Card
	return LarkSendResult{}, ErrNilClient
}
