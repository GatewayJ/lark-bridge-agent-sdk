package impresenter

import (
	"context"
	agentport "github.com/GatewayJ/lark-bridge-agent-sdk/internal/ports/agent"
	"strings"
	"testing"
	"time"
)

type fallbackFinalRun struct{ events chan agentport.AgentEvent }

func (r fallbackFinalRun) Events(context.Context) <-chan agentport.AgentEvent { return r.events }

func TestFallbackPublishesFinalAsNewMessage(t *testing.T) {
	for _, mode := range []ReplyMode{ReplyMarkdown, ReplyCard, ReplyText} {
		t.Run(string(mode), func(t *testing.T) {
			ch := &fakeChannel{}
			events := make(chan agentport.AgentEvent, 5)
			resume := make(chan struct{})
			close(resume)
			commentary := textEvent("过程说明")
			commentary.Phase = agentport.TextCommentary
			final := textEvent("最终结果 https://example.com/result")
			final.Phase = agentport.TextFinalAnswer
			_, err := Present(context.Background(), Input{
				Run: fallbackFinalRun{events}, Channel: ch, ChatID: "chat", ReplyMode: mode,
				DeferUntilDone: true, FinalAnswerOnly: true, ResumeProgress: resume, StreamThrottle: time.Nanosecond,
				OnResumeProgress: func(context.Context) {
					events <- commentary
					events <- final
					events <- agentport.AgentEvent{Type: agentport.EventUsage}
					events <- agentport.AgentEvent{Type: agentport.EventDone}
					close(events)
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			var last string
			if mode == ReplyCard {
				if len(ch.cards) < 2 {
					t.Fatal("缺少独立的最终卡片")
				}
				last = mustCardBody(ch.cards[len(ch.cards)-1].Card)
				for _, update := range ch.updates {
					if strings.Contains(mustCardBody(update.Card), "最终结果") {
						t.Fatal("最终结果混入过程卡片")
					}
				}
			} else {
				if mode == ReplyMarkdown && len(ch.messages) < 2 {
					t.Fatal("缺少独立的最终消息")
				}
				last = ch.messages[len(ch.messages)-1].Content.Markdown
				for _, update := range ch.messageUpdates {
					if strings.Contains(update.Content.Markdown, "最终结果") {
						t.Fatal("最终结果混入过程消息")
					}
				}
			}
			if !strings.Contains(last, "最终结果 https://example.com/result") || strings.Contains(last, "过程说明") {
				t.Fatalf("最终回复内容错误：%s", last)
			}
		})
	}
}
