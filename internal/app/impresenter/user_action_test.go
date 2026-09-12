package impresenter

import (
	"context"
	"errors"
	"strings"
	"testing"

	agentport "github.com/GatewayJ/lark-bridge-agent-sdk/internal/ports/agent"
)

func TestPresentSeparatesCommentaryActionsAndFinalInEveryMode(t *testing.T) {
	for _, mode := range []ReplyMode{ReplyText, ReplyMarkdown, ReplyCard} {
		t.Run(string(mode), func(t *testing.T) {
			ch := &fakeChannel{}
			progress, final := textEvent("Checking the calendar"), textEvent("Meeting created.\n\n18:00–18:30")
			progress.Phase, final.Phase = agentport.TextCommentary, agentport.TextFinalAnswer
			action := promptAction("confirmation", "Use 30 minutes?")
			_, err := Present(context.Background(), Input{
				Run:     fakeRun{progress, action, action, toolUseEvent("t", "calendar"), toolResultEvent("t", "ok"), final, {Type: agentport.EventDone}},
				Channel: ch, ChatID: "chat", ReplyMode: mode, DeferUntilDone: true, FinalAnswerOnly: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(ch.messages) < 1 || ch.messages[0].Content.Markdown != "Use 30 minutes?" {
				t.Fatalf("action=%#v", ch.messages)
			}
			var body string
			if mode == ReplyCard {
				if len(ch.cards) != 1 || len(ch.messages) != 1 {
					t.Fatalf("outbound=%#v", ch)
				}
				body = mustCardBody(ch.cards[0].Card)
			} else {
				if len(ch.messages) != 2 {
					t.Fatalf("messages=%#v", ch.messages)
				}
				body = ch.messages[1].Content.Markdown
			}
			if !strings.Contains(body, *final.Delta) || strings.Contains(body, *progress.Delta) || strings.Contains(body, "Use 30 minutes?") {
				t.Fatalf("wrong final body: %q", body)
			}
		})
	}
}

func TestPresentActionDeliveryFallbackAndFailure(t *testing.T) {
	for _, failCard := range []bool{false, true} {
		ch := &actionFailingChannel{fakeChannel: &fakeChannel{}, failCard: failCard}
		notified := false
		progress := textEvent("Still working")
		progress.Phase = agentport.TextCommentary
		state, err := Present(context.Background(), Input{
			Run:     fakeRun{promptAction("authorization", "https://example.com/auth"), progress, {Type: agentport.EventDone}},
			Channel: ch, ChatID: "chat", ReplyMode: ReplyText, DeferUntilDone: true, FinalAnswerOnly: true, PrivateChat: true,
			OnUserActionError: func(context.Context, error) { notified = true },
		})
		if (err != nil) != failCard || notified != failCard {
			t.Fatalf("failure=%v err=%v notified=%v", failCard, err, notified)
		}
		if state.Status != "succeeded" || len(state.Blocks) == 0 {
			t.Fatalf("action delivery stopped run: %#v", state)
		}
		if !failCard && (len(ch.cards) != 1 || !strings.Contains(mustCardBody(ch.cards[0].Card), "https://example.com/auth")) {
			t.Fatalf("fallback card=%#v", ch.cards)
		}
	}
}

func TestPresentAuthorizationRequiresPrivateChat(t *testing.T) {
	ch := &fakeChannel{}
	_, err := Present(context.Background(), Input{
		Run:     fakeRun{promptAction("authorization", "https://example.com/auth"), {Type: agentport.EventDone}},
		Channel: ch, ChatID: "group", ReplyMode: ReplyText, DeferUntilDone: true, FinalAnswerOnly: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ch.messages) != 1 || strings.Contains(ch.messages[0].Content.Markdown, "https://") || !strings.Contains(ch.messages[0].Content.Markdown, "私聊") {
		t.Fatalf("group authorization=%#v", ch.messages)
	}
}

func TestPresentDoesNotPromoteCommentaryWhenFinalIsMissing(t *testing.T) {
	ch := &fakeChannel{}
	progress := textEvent("Creating the meeting")
	progress.Phase = agentport.TextCommentary
	_, err := Present(context.Background(), Input{
		Run: fakeRun{progress, {Type: agentport.EventDone}}, Channel: ch, ChatID: "chat",
		ReplyMode: ReplyMarkdown, DeferUntilDone: true, FinalAnswerOnly: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ch.messages) != 1 || ch.messages[0].Content.Markdown != "本轮未返回最终回复。" {
		t.Fatalf("final=%#v", ch.messages)
	}
}

type actionFailingChannel struct {
	*fakeChannel
	failCard bool
}

func (c *actionFailingChannel) SendMessage(_ context.Context, req SendMessageRequest) (SendMessageResult, error) {
	if strings.Contains(req.Content.Markdown, "https://example.com/auth") {
		return SendMessageResult{}, errors.New("message failed")
	}
	return c.fakeChannel.SendMessage(context.Background(), req)
}

func (c *actionFailingChannel) SendCard(ctx context.Context, req SendCardRequest) (SendCardResult, error) {
	if c.failCard {
		return SendCardResult{}, errors.New("card failed")
	}
	return c.fakeChannel.SendCard(ctx, req)
}

func promptAction(kind, message string) agentport.AgentEvent {
	id := "request-1"
	return agentport.AgentEvent{Type: agentport.EventUserAction, ID: &id, Name: &kind, Delta: &message}
}
