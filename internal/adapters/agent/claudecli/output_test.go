package claudecli

import (
	"context"
	"runtime"
	"testing"

	"github.com/GatewayJ/lark-bridge-agent-sdk/internal/domain/permissions"
	agentport "github.com/GatewayJ/lark-bridge-agent-sdk/internal/ports/agent"
)

func TestAdapterUsesResultAsFinalAndRejectsFailedResults(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake uses POSIX sh")
	}
	for _, tc := range []struct {
		name, result, wantFinal string
		wantType                agentport.EventType
	}{
		{"success", `{"type":"result","subtype":"success","result":"Final answer","is_error":false}`, "Final answer", agentport.EventDone},
		{"empty", `{"type":"result","subtype":"success","result":"","is_error":false}`, "", agentport.EventDone},
		{"error flag", `{"type":"result","is_error":true,"result":"authorization failed"}`, "", agentport.EventError},
		{"error subtype", `{"type":"result","subtype":"error_max_turns","errors":["too many turns"]}`, "", agentport.EventError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			binary := writeFakeClaude(t, `
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"Checking"}]}}'
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"Final answer"}]}}'
printf '%s\n' '`+tc.result+`'`)
			run, err := New(Options{Binary: binary}).Run(context.Background(), agentport.AgentRunOptions{
				RunID: "result", Prompt: "test", CWD: t.TempDir(), PermissionMode: permissions.ClaudePermissionAcceptEdits,
			})
			if err != nil {
				t.Fatal(err)
			}
			events := collectEvents(t, run)
			var final string
			for _, event := range events {
				if event.Phase == agentport.TextFinalAnswer {
					final += *event.Delta
				}
			}
			if final != tc.wantFinal || events[len(events)-1].Type != tc.wantType {
				t.Fatalf("final=%q events=%#v", final, events)
			}
			if tc.name == "success" && len(events) != 3 {
				t.Fatalf("result duplicated assistant answer: %#v", events)
			}
		})
	}
}

func TestStreamTranslatorAuthorizationAndSubagentIsolation(t *testing.T) {
	tx := NewStreamTranslator()
	for _, line := range []string{
		`{"type":"assistant","parent_tool_use_id":null,"message":{"content":[{"type":"tool_use","id":"login","name":"Bash","input":{"command":"lark-cli auth login --no-wait --json"}}]}}`,
		`{"type":"assistant","parent_tool_use_id":"subagent","message":{"content":[{"type":"text","text":"subagent result"}]}}`,
	} {
		got, err := tx.TranslateLine([]byte(line))
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range got {
			if event.Type == agentport.EventText && event.Phase != agentport.TextCommentary {
				t.Fatal("subagent text is eligible for the main final")
			}
		}
	}
	got, err := tx.TranslateLine([]byte(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"login","content":"{\"verification_url\":\"https://example.com/auth\"}"}]}}`))
	if err != nil || len(got) != 2 || got[1].Type != agentport.EventUserAction {
		t.Fatalf("authorization events=%#v err=%v", got, err)
	}
}
