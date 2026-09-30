package impresenter

import (
	"context"
	"errors"
	"strings"
	"testing"

	agentport "github.com/GatewayJ/lark-bridge-agent-sdk/internal/ports/agent"
)

type progressFailureChannel struct {
	fakeChannel
	attempts int
}

func (c *progressFailureChannel) SendMessage(ctx context.Context, req SendMessageRequest) (SendMessageResult, error) {
	c.attempts++
	if c.attempts == 1 {
		return SendMessageResult{}, errors.New("progress unavailable")
	}
	return c.fakeChannel.SendMessage(ctx, req)
}
func (c *progressFailureChannel) SendCard(ctx context.Context, req SendCardRequest) (SendCardResult, error) {
	c.attempts++
	if c.attempts == 1 {
		return SendCardResult{}, errors.New("progress unavailable")
	}
	return c.fakeChannel.SendCard(ctx, req)
}
func TestProgressFailureStillDeliversFinal(t *testing.T) {
	for _, mode := range []ReplyMode{ReplyMarkdown, ReplyCard} {
		for _, fallback := range []bool{false, true} {
			t.Run(string(mode)+map[bool]string{false: "/initial", true: "/fallback"}[fallback], func(t *testing.T) {
				channel := &progressFailureChannel{}
				events := make(chan agentport.AgentEvent, 2)
				final := textEvent("最终回复内容")
				final.Phase = agentport.TextFinalAnswer
				emit := func() { events <- final; events <- agentport.AgentEvent{Type: agentport.EventDone}; close(events) }
				var resume chan struct{}
				if fallback {
					resume = make(chan struct{})
					close(resume)
				} else {
					emit()
				}
				failures := 0
				_, err := Present(context.Background(), Input{Run: fallbackFinalRun{events}, Channel: channel, ChatID: "chat", ReplyMode: mode, DeferUntilDone: fallback, FinalAnswerOnly: fallback, ResumeProgress: resume, OnResumeProgress: func(context.Context) { emit() }, OnProgressError: func(context.Context, error) { failures++ }})
				if err != nil {
					t.Fatal(err)
				}
				if failures != 1 {
					t.Fatalf("progress errors=%d", failures)
				}
				body := ""
				if mode == ReplyCard {
					body = mustCardBody(channel.cards[len(channel.cards)-1].Card)
				} else {
					body = channel.messages[len(channel.messages)-1].Content.Markdown
				}
				if !strings.Contains(body, "最终回复内容") {
					t.Fatalf("final=%s", body)
				}
			})
		}
	}
}
