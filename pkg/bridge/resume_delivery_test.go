package bridge

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	appintake "github.com/GatewayJ/lark-bridge-agent-sdk/internal/app/intake"
)

func TestResumeCommandDeliversSelectionAndAppliedReply(t *testing.T) {
	for _, tc := range []struct {
		name     string
		view     *CommandResumeView
		markdown string
		wantCard bool
	}{
		{name: "history", view: &CommandResumeView{CWD: "/repo", Entries: []CommandResumeEntry{{Token: "opaque-token", Preview: "recent session", Detail: "Codex", UpdatedAt: 1700000000000}}}, wantCard: true},
		{name: "empty", view: &CommandResumeView{CWD: "/repo"}, wantCard: true},
		{name: "applied", view: &CommandResumeView{Applied: true}, markdown: "会话已切换"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			transport := NewFakeLarkTransport(LarkBotIdentity{})
			intake := &managedLarkIntake{transport: transport}
			err := intake.sendCommandResponse(context.Background(), appintake.MessageInput{ChatID: "oc_chat", MessageID: "om_request"}, appintake.Scope{Key: "oc_chat"}, CommandResponse{Handled: true, Kind: CommandResponseResume, Resume: tc.view, Markdown: tc.markdown})
			if err != nil {
				t.Fatal(err)
			}
			cards := transport.SentCardSnapshot()
			if tc.wantCard {
				if len(cards) != 1 {
					t.Fatalf("cards=%#v", cards)
				}
				if cards[0].Card["schema"] != "2.0" {
					t.Fatalf("schema=%v", cards[0].Card["schema"])
				}
				data, _ := json.Marshal(cards[0].Card)
				if tc.name == "history" && (!strings.Contains(string(data), "resume.use") || !strings.Contains(string(data), "opaque-token") || !strings.Contains(string(data), "recent session")) {
					t.Fatalf("card=%s", data)
				}
				if tc.name == "empty" && !strings.Contains(string(data), "没有历史会话") {
					t.Fatalf("card=%s", data)
				}
			} else {
				messages := transport.SentMessageSnapshot()
				if len(cards) != 0 || len(messages) != 1 || messages[0].Content.Markdown != tc.markdown {
					t.Fatalf("messages=%#v cards=%#v", messages, cards)
				}
			}
		})
	}
}
