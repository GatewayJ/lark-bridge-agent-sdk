package bridge

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	appcot "github.com/GatewayJ/lark-bridge-agent-sdk/internal/app/cotpresenter"
	appimpresenter "github.com/GatewayJ/lark-bridge-agent-sdk/internal/app/impresenter"
	agentport "github.com/GatewayJ/lark-bridge-agent-sdk/internal/ports/agent"
)

func TestManagedAuthorizationRemainsVisibleWhileCOTAndRunContinue(t *testing.T) {
	for _, mode := range []appcot.Mode{appcot.ModeBrief, appcot.ModeDetailed} {
		t.Run(string(mode), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			transport := NewFakeLarkTransport(LarkBotIdentity{})
			intake := &managedLarkIntake{transport: transport, cotClient: wrapInternalLarkCOTClient(transport)}
			events := make(chan agentport.AgentEvent, 8)
			finished := make(chan error, 1)
			go func() {
				_, err := intake.presentRun(ctx, managedPresentInput{
					Run: presenterEventRun{events: events}, ChatID: "oc_chat", RunID: "run-auth",
					Options:   appimpresenter.SendOptions{ReplyTo: "om_origin", ReplyInThread: true},
					ReplyMode: appimpresenter.ReplyMarkdown, COTMessages: mode, PrivateChat: true,
					IdleTimeout: 200 * time.Millisecond,
				})
				finished <- err
			}()
			id, kind, message := "auth-1", "authorization", "请授权：https://example.com/authorize?code=abc&state=def"
			toolID, toolName := "wait-auth", "command_execution"
			events <- agentport.AgentEvent{Type: agentport.EventUserAction, ID: &id, Name: &kind, Delta: &message}
			events <- agentport.AgentEvent{Type: agentport.EventUserAction, ID: &id, Name: &kind, Delta: &message}
			events <- agentport.AgentEvent{Type: agentport.EventToolUse, ID: &toolID, Name: &toolName, Input: map[string]any{"command": "lark-cli auth login --device-code redacted"}}
			waitForCondition(t, 2*time.Second, func() bool {
				payload, _ := json.Marshal(transport.UpdatedCOTSnapshot())
				return strings.Contains(string(payload), "等待用户操作") && strings.Contains(string(payload), "TOOL_CALL_START") && len(transport.SentMessageSnapshot()) > 0
			})
			messages := transport.SentMessageSnapshot()
			if len(messages) != 1 || messages[0].Content.Markdown != message || messages[0].Options.ReplyTo != "om_origin" || !messages[0].Options.ReplyInThread {
				t.Fatalf("live authorization prompt=%#v", messages)
			}
			if len(transport.CompletedCOTSnapshot()) != 0 {
				t.Fatal("authorization closed COT")
			}
			select {
			case err := <-finished:
				t.Fatalf("authorization ended the run or triggered idle timeout: %v", err)
			default:
			}
			progress, final := "正在创建会议。", "会议已创建：18:00–18:30。"
			events <- agentport.AgentEvent{Type: agentport.EventToolResult, ID: &toolID}
			events <- agentport.AgentEvent{Type: agentport.EventText, Phase: agentport.TextCommentary, Delta: &progress}
			events <- agentport.AgentEvent{Type: agentport.EventText, Phase: agentport.TextFinalAnswer, Delta: &final}
			events <- agentport.AgentEvent{Type: agentport.EventDone, TerminationReason: agentport.TerminationNormal}
			close(events)
			select {
			case err := <-finished:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal("run did not finish after authorization")
			}
			messages = transport.SentMessageSnapshot()
			if len(messages) != 2 || messages[1].Content.Markdown != final {
				t.Fatalf("final messages=%#v", messages)
			}
			payload, _ := json.Marshal(transport.UpdatedCOTSnapshot())
			if !strings.Contains(string(payload), progress) || strings.Contains(string(payload), final) || strings.Contains(string(payload), "https://example.com/authorize") {
				t.Fatalf("wrong COT routing: %s", payload)
			}
			if len(transport.CompletedCOTSnapshot()) != 1 {
				t.Fatal("COT not completed exactly once at end of run")
			}
		})
	}
}

func TestEventAndCardConversionsPreserveTextPhases(t *testing.T) {
	body, id := "final", "message-2"
	event := Event{Type: EventText, Phase: TextFinalAnswer, ID: &id, Delta: &body}
	if got := fromAgentEvent(toAgentEvent(event)); got.Phase != TextFinalAnswer {
		t.Fatalf("phase lost in event round trip: %#v", got)
	}
	state := ReduceRunCardState(NewRunCardState(RunCardStateInput{}), event)
	state = fromInternalRunCardState(toInternalRunCardState(state))
	if len(state.Blocks) != 1 || state.Blocks[0].Phase != TextFinalAnswer || state.Blocks[0].ID != id {
		t.Fatalf("card metadata lost: %#v", state.Blocks)
	}
}
