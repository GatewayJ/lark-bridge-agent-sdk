package impresenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/GatewayJ/lark-bridge-agent-sdk/internal/app/cardrender"
	agentport "github.com/GatewayJ/lark-bridge-agent-sdk/internal/ports/agent"
)

func TestPresentTextModeSendsFinalMarkdownOnly(t *testing.T) {
	ch := &fakeChannel{}
	run := fakeRun([]agentport.AgentEvent{
		textEvent("hello"),
		{Type: agentport.EventDone},
	})

	state, err := Present(context.Background(), Input{
		Run:       run,
		Channel:   ch,
		ChatID:    "oc_chat",
		ReplyMode: ReplyText,
		Options:   SendOptions{ReplyTo: "om_parent"},
	})
	if err != nil {
		t.Fatalf("Present returned error: %v", err)
	}
	if state.Status != "succeeded" {
		t.Fatalf("state = %#v", state)
	}
	if len(ch.messages) != 1 || ch.messages[0].Content.Markdown != "hello" || ch.messages[0].Content.Text != "" {
		t.Fatalf("messages = %#v", ch.messages)
	}
	if len(ch.cards) != 0 {
		t.Fatalf("cards = %#v", ch.cards)
	}
}

func TestPresentCardModeSendsRunCard(t *testing.T) {
	ch := &fakeChannel{}
	run := fakeRun([]agentport.AgentEvent{
		textEvent("hello card"),
		{Type: agentport.EventDone},
	})

	_, err := Present(context.Background(), Input{
		Run:       run,
		Channel:   ch,
		ChatID:    "oc_chat",
		ReplyMode: ReplyCard,
		Options:   SendOptions{ReplyTo: "om_parent"},
	})
	if err != nil {
		t.Fatalf("Present returned error: %v", err)
	}
	if len(ch.cards) != 1 || ch.cards[0].Card["schema"] != "2.0" {
		t.Fatalf("cards = %#v", ch.cards)
	}
	if len(ch.updates) != 1 || ch.updates[0].MessageID != "card-message-1" || ch.updates[0].Card["schema"] != "2.0" {
		t.Fatalf("updates = %#v", ch.updates)
	}
	if !strings.Contains(mustCardBody(ch.updates[0].Card), "hello card") {
		t.Fatalf("card body = %#v", ch.updates[0].Card)
	}
	if len(ch.messages) != 0 {
		t.Fatalf("messages = %#v", ch.messages)
	}
}

func TestPresentCardModeStreamsThrottledUpdates(t *testing.T) {
	ch := &fakeChannel{}
	run := delayedRun{
		{event: textEvent("hello")},
		{after: 15 * time.Millisecond, event: textEvent(" live")},
		{event: agentport.AgentEvent{Type: agentport.EventDone}},
	}

	_, err := Present(context.Background(), Input{
		Run:            run,
		Channel:        ch,
		ChatID:         "oc_chat",
		ReplyMode:      ReplyCard,
		StreamThrottle: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Present returned error: %v", err)
	}
	if len(ch.cards) != 1 {
		t.Fatalf("cards = %#v", ch.cards)
	}
	if len(ch.updates) < 2 {
		t.Fatalf("updates = %#v, want streaming update plus final update", ch.updates)
	}
	if !strings.Contains(mustCardBody(ch.updates[0].Card), "hello live") {
		t.Fatalf("first streaming update body = %#v", ch.updates[0].Card)
	}
	if !strings.Contains(mustCardBody(ch.updates[len(ch.updates)-1].Card), "hello live") {
		t.Fatalf("final update body = %#v", ch.updates[len(ch.updates)-1].Card)
	}
}

func TestPresentCardModeRollsOverAfterUpdateLimit(t *testing.T) {
	ch := &fakeChannel{}
	run := delayedRun{
		{after: 5 * time.Millisecond, event: textEvent("first")},
		{after: 5 * time.Millisecond, event: textEvent(" second")},
		{event: agentport.AgentEvent{Type: agentport.EventDone}},
	}

	_, err := Present(context.Background(), Input{
		Run:            run,
		Channel:        ch,
		ChatID:         "oc_chat",
		ReplyMode:      ReplyCard,
		StreamThrottle: time.Millisecond,
		CardRollover: CardRolloverPolicy{
			MaxUpdates: 1,
		},
	})
	if err != nil {
		t.Fatalf("Present returned error: %v", err)
	}
	if len(ch.cards) != 2 {
		t.Fatalf("cards = %#v, want initial card plus continuation", ch.cards)
	}
	continuation := mustCardBody(ch.cards[1].Card)
	if !strings.Contains(continuation, "second") || strings.Contains(continuation, "first") {
		t.Fatalf("continuation body = %q, want only unpublished output", continuation)
	}
	if !strings.Contains(continuation, continuationCardNote) {
		t.Fatalf("continuation body = %q, want continuation marker", continuation)
	}
	if !hasCardUpdate(ch.updates, "card-message-1", continuedCardNote) {
		t.Fatalf("updates = %#v, want old card frozen with continuation marker", ch.updates)
	}
	if !hasCardUpdate(ch.updates, "card-message-2", "second") {
		t.Fatalf("updates = %#v, want final update on the continuation card", ch.updates)
	}
}

func TestPresentCardModeRollsOverBeforeSerializedSizeLimit(t *testing.T) {
	input := Input{ReplyMode: ReplyCard}
	first := strings.Repeat("甲", 80)
	second := strings.Repeat("乙", 80)
	state := cardrender.NewRunState(cardrender.RunStateInput{})
	state = cardrender.Reduce(state, toCardEvent(textEvent(first)))
	firstSize := serializedCardSize(t, renderRunCard(input, state))
	state = cardrender.Reduce(state, toCardEvent(textEvent(second)))
	secondSize := serializedCardSize(t, renderRunCard(input, state))
	limit := firstSize + (secondSize-firstSize)/2

	ch := &fakeChannel{}
	run := delayedRun{
		{after: 5 * time.Millisecond, event: textEvent(first)},
		{after: 5 * time.Millisecond, event: textEvent(second)},
		{event: agentport.AgentEvent{Type: agentport.EventDone}},
	}
	input.Run = run
	input.Channel = ch
	input.ChatID = "oc_chat"
	input.StreamThrottle = time.Millisecond
	input.CardRollover.MaxBytes = limit

	_, err := Present(context.Background(), input)
	if err != nil {
		t.Fatalf("Present returned error: %v", err)
	}
	if len(ch.cards) != 2 {
		t.Fatalf("cards = %#v, want size-triggered continuation", ch.cards)
	}
	continuation := mustCardBody(ch.cards[1].Card)
	if !strings.Contains(continuation, second) || strings.Contains(continuation, first) {
		t.Fatalf("continuation body = %q, want only the size-overflow delta", continuation)
	}
}

func TestPresentCardModeSplitsOneOversizedDeltaAcrossContinuationCards(t *testing.T) {
	input := Input{ReplyMode: ReplyCard}
	chunk := strings.Repeat("x", 120)
	state := cardrender.NewRunState(cardrender.RunStateInput{})
	state = cardrender.Reduce(state, toCardEvent(textEvent(chunk)))
	limit := serializedCardSize(t, renderContinuationCard(input, state))
	output := strings.Repeat("x", 1200)

	ch := &fakeChannel{}
	input.Run = delayedRun{
		{after: 5 * time.Millisecond, event: textEvent(output)},
		{event: agentport.AgentEvent{Type: agentport.EventDone}},
	}
	input.Channel = ch
	input.ChatID = "oc_chat"
	input.StreamThrottle = time.Millisecond
	input.CardRollover.MaxBytes = limit

	_, err := Present(context.Background(), input)
	if err != nil {
		t.Fatalf("Present returned error: %v", err)
	}
	if len(ch.cards) < 3 {
		t.Fatalf("cards = %d, want multiple continuation cards", len(ch.cards))
	}
	var delivered strings.Builder
	for _, sent := range ch.cards[1:] {
		if size := serializedCardSize(t, sent.Card); size > limit {
			t.Fatalf("continuation card bytes = %d, limit = %d", size, limit)
		}
		delivered.WriteString(primaryCardMarkdown(sent.Card))
	}
	if delivered.String() != output {
		t.Fatalf("delivered output length = %d, want %d", delivered.Len(), len(output))
	}
}

func TestPresentCardModeSplitsOversizedDeferredFinalAnswer(t *testing.T) {
	input := Input{ReplyMode: ReplyCard, DeferUntilDone: true, FinalAnswerOnly: true}
	chunk := strings.Repeat("z", 120)
	state := cardrender.NewRunState(cardrender.RunStateInput{})
	state = cardrender.Reduce(state, toCardEvent(textEvent(chunk)))
	state = cardrender.Reduce(state, toCardEvent(agentport.AgentEvent{Type: agentport.EventDone}))
	limit := serializedCardSize(t, renderContinuationCard(input, state))
	output := strings.Repeat("z", 1200)

	ch := &fakeChannel{}
	input.Run = fakeRun([]agentport.AgentEvent{
		textEvent(output),
		{Type: agentport.EventDone},
	})
	input.Channel = ch
	input.ChatID = "oc_chat"
	input.CardRollover.MaxBytes = limit

	_, err := Present(context.Background(), input)
	if err != nil {
		t.Fatalf("Present returned error: %v", err)
	}
	if len(ch.cards) < 2 || len(ch.updates) != 0 {
		t.Fatalf("cards = %d updates = %d, want split final cards without updates", len(ch.cards), len(ch.updates))
	}
	var delivered strings.Builder
	for _, sent := range ch.cards {
		if size := serializedCardSize(t, sent.Card); size > limit {
			t.Fatalf("final card bytes = %d, limit = %d", size, limit)
		}
		delivered.WriteString(primaryCardMarkdown(sent.Card))
	}
	if delivered.String() != output {
		t.Fatalf("delivered output length = %d, want %d", delivered.Len(), len(output))
	}
}

func TestPresentCardModeCanHideToolCalls(t *testing.T) {
	ch := &fakeChannel{}
	run := fakeRun([]agentport.AgentEvent{
		toolUseEvent("tool-1", "Bash"),
		toolResultEvent("tool-1", "secret output"),
		textEvent("final answer"),
		{Type: agentport.EventDone},
	})

	state, err := Present(context.Background(), Input{
		Run:           run,
		Channel:       ch,
		ChatID:        "oc_chat",
		ReplyMode:     ReplyCard,
		HideToolCalls: true,
	})
	if err != nil {
		t.Fatalf("Present returned error: %v", err)
	}
	if len(state.Blocks) != 2 {
		t.Fatalf("state blocks = %#v, want original tool and text blocks", state.Blocks)
	}
	if len(ch.updates) != 1 {
		t.Fatalf("updates = %#v", ch.updates)
	}
	body := mustCardBody(ch.updates[0].Card)
	if strings.Contains(body, "Bash") || strings.Contains(body, "secret output") || !strings.Contains(body, "final answer") {
		t.Fatalf("card body = %q", body)
	}
}

func TestPresentTextModeCanHideToolCalls(t *testing.T) {
	ch := &fakeChannel{}
	run := fakeRun([]agentport.AgentEvent{
		toolUseEvent("tool-1", "Bash"),
		toolResultEvent("tool-1", "secret output"),
		textEvent("final answer"),
		{Type: agentport.EventDone},
	})

	_, err := Present(context.Background(), Input{
		Run:           run,
		Channel:       ch,
		ChatID:        "oc_chat",
		ReplyMode:     ReplyText,
		HideToolCalls: true,
	})
	if err != nil {
		t.Fatalf("Present returned error: %v", err)
	}
	if len(ch.messages) != 1 {
		t.Fatalf("messages = %#v", ch.messages)
	}
	body := ch.messages[0].Content.Markdown
	if strings.Contains(body, "Bash") || strings.Contains(body, "secret output") || !strings.Contains(body, "final answer") {
		t.Fatalf("message body = %q", body)
	}
}

func TestPresentCardModeCanDeferAndSendFinalAnswerOnly(t *testing.T) {
	ch := &fakeChannel{}
	run := fakeRun([]agentport.AgentEvent{
		toolUseEvent("tool-1", "Bash"),
		toolResultEvent("tool-1", "secret output"),
		textEvent("final answer"),
		{Type: agentport.EventDone},
	})
	hookCalled := false

	_, err := Present(context.Background(), Input{
		Run:             run,
		Channel:         ch,
		ChatID:          "oc_chat",
		ReplyMode:       ReplyCard,
		DeferUntilDone:  true,
		FinalAnswerOnly: true,
		BeforeFinal: func(context.Context, cardrender.RunState) error {
			hookCalled = true
			if len(ch.cards) != 0 || len(ch.updates) != 0 {
				t.Fatalf("BeforeFinal saw outbound cards=%d updates=%d", len(ch.cards), len(ch.updates))
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Present returned error: %v", err)
	}
	if !hookCalled {
		t.Fatalf("BeforeFinal was not called")
	}
	if len(ch.cards) != 1 || len(ch.updates) != 0 {
		t.Fatalf("cards=%#v updates=%#v", ch.cards, ch.updates)
	}
	body := mustCardBody(ch.cards[0].Card)
	if strings.Contains(body, "Bash") || strings.Contains(body, "secret output") || !strings.Contains(body, "final answer") {
		t.Fatalf("final card body = %q", body)
	}
}

func TestPresentMarkdownModeStreamsThrottledUpdates(t *testing.T) {
	ch := &fakeChannel{}
	run := delayedRun{
		{event: textEvent("hello")},
		{after: 15 * time.Millisecond, event: textEvent(" live")},
		{event: agentport.AgentEvent{Type: agentport.EventDone}},
	}

	_, err := Present(context.Background(), Input{
		Run:            run,
		Channel:        ch,
		ChatID:         "oc_chat",
		ReplyMode:      ReplyMarkdown,
		StreamThrottle: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Present returned error: %v", err)
	}
	if len(ch.messages) != 1 || !strings.Contains(ch.messages[0].Content.Markdown, "正在思考") {
		t.Fatalf("messages = %#v", ch.messages)
	}
	if len(ch.messageUpdates) < 2 {
		t.Fatalf("message updates = %#v, want streaming update plus final update", ch.messageUpdates)
	}
	if ch.messageUpdates[0].MessageID != "message-1" || !strings.Contains(ch.messageUpdates[0].Content.Markdown, "hello live") {
		t.Fatalf("first message update = %#v", ch.messageUpdates[0])
	}
	if !strings.Contains(ch.messageUpdates[len(ch.messageUpdates)-1].Content.Markdown, "hello live") {
		t.Fatalf("final message update = %#v", ch.messageUpdates[len(ch.messageUpdates)-1])
	}
}

func TestPresentMarkdownModeClearsFooterWhenRunHasNoContent(t *testing.T) {
	ch := &fakeChannel{}
	run := fakeRun([]agentport.AgentEvent{{Type: agentport.EventDone}})

	_, err := Present(context.Background(), Input{
		Run:       run,
		Channel:   ch,
		ChatID:    "oc_chat",
		ReplyMode: ReplyMarkdown,
	})
	if err != nil {
		t.Fatalf("Present returned error: %v", err)
	}
	if len(ch.messages) != 1 || !strings.Contains(ch.messages[0].Content.Markdown, "正在思考") {
		t.Fatalf("initial placeholder = %#v", ch.messages)
	}
	if len(ch.messageUpdates) != 1 {
		t.Fatalf("message updates = %#v, want terminal settlement", ch.messageUpdates)
	}
	final := ch.messageUpdates[0].Content.Markdown
	if !strings.Contains(final, "未返回内容") || strings.Contains(final, "正在思考") || strings.Contains(final, "正在输出") {
		t.Fatalf("terminal settlement = %q", final)
	}
}

func TestPresentMarkdownModeRollsOverWithoutRepeatingVisibleContent(t *testing.T) {
	ch := &fakeChannel{}
	run := delayedRun{
		{event: textEvent("alpha")},
		{after: 5 * time.Millisecond, event: textEvent(" beta")},
		{after: 5 * time.Millisecond, event: textEvent(" gamma")},
		{event: agentport.AgentEvent{Type: agentport.EventDone}},
	}

	_, err := Present(context.Background(), Input{
		Run:               run,
		Channel:           ch,
		ChatID:            "oc_chat",
		ReplyMode:         ReplyMarkdown,
		StreamThrottle:    time.Millisecond,
		MaxMessageUpdates: 1,
	})
	if err != nil {
		t.Fatalf("Present returned error: %v", err)
	}
	if len(ch.messages) != 2 {
		t.Fatalf("messages = %#v, want initial message plus continuation", ch.messages)
	}
	continuation := ch.messages[1].Content.Markdown
	if !strings.Contains(continuation, "gamma") || strings.Contains(continuation, "alpha") || strings.Contains(continuation, "beta") {
		t.Fatalf("continuation repeated prior content: %q", continuation)
	}
	if len(ch.messageUpdates) != 3 {
		t.Fatalf("message updates = %#v, want stream, settlement, and final updates", ch.messageUpdates)
	}
	settled := ch.messageUpdates[1]
	if settled.MessageID != "message-1" || !strings.Contains(settled.Content.Markdown, "alpha") || !strings.Contains(settled.Content.Markdown, "beta") {
		t.Fatalf("settled prior message = %#v", settled)
	}
	if strings.Contains(settled.Content.Markdown, "正在输出") {
		t.Fatalf("settled prior message retained streaming footer: %#v", settled)
	}
	final := ch.messageUpdates[2]
	if final.MessageID != "message-2" || !strings.Contains(final.Content.Markdown, "gamma") {
		t.Fatalf("final continuation update = %#v", final)
	}
	if strings.Contains(final.Content.Markdown, "alpha") || strings.Contains(final.Content.Markdown, "beta") || strings.Contains(final.Content.Markdown, "正在输出") {
		t.Fatalf("final continuation repeated content or retained footer: %#v", final)
	}
}

func TestPresentMarkdownModeRolloverSettlementFailureDoesNotBlockContinuation(t *testing.T) {
	settleErr := errors.New("temporary settlement failure")
	ch := &fakeChannel{messageUpdateErrs: []error{nil, settleErr, settleErr, nil}}
	run := delayedRun{
		{event: textEvent("alpha")},
		{after: 5 * time.Millisecond, event: textEvent(" beta")},
		{after: 5 * time.Millisecond, event: textEvent(" gamma")},
		{event: agentport.AgentEvent{Type: agentport.EventDone}},
	}

	_, err := Present(context.Background(), Input{
		Run:               run,
		Channel:           ch,
		ChatID:            "oc_chat",
		ReplyMode:         ReplyMarkdown,
		StreamThrottle:    time.Millisecond,
		MaxMessageUpdates: 1,
	})
	if err != nil {
		t.Fatalf("Present returned error: %v", err)
	}
	if len(ch.messages) != 2 || !strings.Contains(ch.messages[1].Content.Markdown, "gamma") {
		t.Fatalf("continuation was blocked by settlement failure: %#v", ch.messages)
	}
	if len(ch.messageUpdates) != 4 || ch.messageUpdates[len(ch.messageUpdates)-1].MessageID != "message-2" {
		t.Fatalf("message updates = %#v, want two settlement attempts and final continuation", ch.messageUpdates)
	}
}

func TestPresentMarkdownModeFallbackSendsOnlyUnpublishedContent(t *testing.T) {
	ch := &fakeChannel{messageUpdateErrs: []error{nil, errors.New("patch denied")}}
	run := delayedRun{
		{event: textEvent("alpha")},
		{after: 5 * time.Millisecond, event: textEvent(" beta")},
		{after: 5 * time.Millisecond, event: textEvent(" gamma")},
		{event: agentport.AgentEvent{Type: agentport.EventDone}},
	}

	_, err := Present(context.Background(), Input{
		Run:            run,
		Channel:        ch,
		ChatID:         "oc_chat",
		ReplyMode:      ReplyMarkdown,
		StreamThrottle: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Present returned error: %v", err)
	}
	if len(ch.messages) != 2 {
		t.Fatalf("messages = %#v, want initial stream plus fallback", ch.messages)
	}
	fallback := ch.messages[1].Content.Markdown
	if !strings.Contains(fallback, "gamma") || strings.Contains(fallback, "alpha") || strings.Contains(fallback, "beta") {
		t.Fatalf("fallback repeated already published content: %q", fallback)
	}
	if strings.Contains(fallback, "正在输出") {
		t.Fatalf("fallback retained streaming footer: %q", fallback)
	}
}

func TestPresentMarkdownModeFallsBackToNewMessageWhenFinalUpdateFails(t *testing.T) {
	ch := &fakeChannel{messageUpdateErr: errors.New("patch denied")}
	run := fakeRun([]agentport.AgentEvent{
		textEvent("final answer"),
		{Type: agentport.EventDone},
	})

	_, err := Present(context.Background(), Input{
		Run:       run,
		Channel:   ch,
		ChatID:    "oc_chat",
		ReplyMode: ReplyMarkdown,
	})
	if err != nil {
		t.Fatalf("Present returned error: %v", err)
	}
	if len(ch.messageUpdates) == 0 {
		t.Fatalf("message updates = %#v, want attempted update", ch.messageUpdates)
	}
	if len(ch.messages) != 2 {
		t.Fatalf("messages = %#v, want initial stream plus fallback final", ch.messages)
	}
	if !strings.Contains(ch.messages[1].Content.Markdown, "final answer") {
		t.Fatalf("fallback final message = %#v", ch.messages[1])
	}
}

func TestPresentMarkdownModeStopsStreamingAfterUpdateFails(t *testing.T) {
	ch := &fakeChannel{messageUpdateErr: errors.New("message cannot be updated")}
	run := delayedRun{
		{event: textEvent("first")},
		{after: 15 * time.Millisecond, event: textEvent(" second")},
		{after: 15 * time.Millisecond, event: textEvent(" third")},
		{event: agentport.AgentEvent{Type: agentport.EventDone}},
	}

	_, err := Present(context.Background(), Input{
		Run:            run,
		Channel:        ch,
		ChatID:         "oc_chat",
		ReplyMode:      ReplyMarkdown,
		StreamThrottle: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Present returned error: %v", err)
	}
	if len(ch.messageUpdates) != 1 {
		t.Fatalf("message updates = %#v, want one failed streaming update", ch.messageUpdates)
	}
	if len(ch.messages) != 2 {
		t.Fatalf("messages = %#v, want initial stream plus fallback final", ch.messages)
	}
	if !strings.Contains(ch.messages[1].Content.Markdown, "first second third") {
		t.Fatalf("fallback final message = %#v", ch.messages[1])
	}
}

func TestPresentStopsRunOnIdleTimeout(t *testing.T) {
	ch := &fakeChannel{}
	run := newIdleBlockingRun(textEvent("partial"))

	state, err := Present(context.Background(), Input{
		Run:         run,
		Channel:     ch,
		ChatID:      "oc_chat",
		ReplyMode:   ReplyText,
		IdleTimeout: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Present returned error: %v", err)
	}
	if state.Status != cardrender.StatusTimeout || state.TimeoutMinutes != 1 {
		t.Fatalf("state = %#v, want timeout", state)
	}
	select {
	case <-run.stopped:
	default:
		t.Fatalf("run was not stopped after idle timeout")
	}
	if len(ch.messages) != 1 || !strings.Contains(ch.messages[0].Content.Markdown, "无响应") {
		t.Fatalf("messages = %#v", ch.messages)
	}
}

func TestPresentIdleTimeoutPausesWhileToolIsInFlight(t *testing.T) {
	ch := &fakeChannel{}
	run := delayedRun{
		{event: toolUseEvent("tool-1", "lark-cli")},
		{after: 30 * time.Millisecond, event: toolResultEvent("tool-1", "ok")},
		{event: textEvent("done")},
		{event: agentport.AgentEvent{Type: agentport.EventDone}},
	}

	state, err := Present(context.Background(), Input{
		Run:         run,
		Channel:     ch,
		ChatID:      "oc_chat",
		ReplyMode:   ReplyText,
		IdleTimeout: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Present returned error: %v", err)
	}
	if state.Status != cardrender.StatusSucceeded {
		t.Fatalf("state = %#v, want succeeded", state)
	}
	if len(ch.messages) != 1 || !strings.Contains(ch.messages[0].Content.Markdown, "done") {
		t.Fatalf("messages = %#v", ch.messages)
	}
}

func TestPresentCardModeSendsInitialStopToken(t *testing.T) {
	ch := &fakeChannel{}

	_, err := Present(context.Background(), Input{
		Run:       fakeRun([]agentport.AgentEvent{{Type: agentport.EventDone}}),
		Channel:   ch,
		ChatID:    "oc_chat",
		ReplyMode: ReplyCard,
		RenderOptions: cardRenderOptions(func(action string) string {
			if action != "stop" {
				t.Fatalf("action = %q, want stop", action)
			}
			return "signed-token"
		}),
	})
	if err != nil {
		t.Fatalf("Present returned error: %v", err)
	}
	if len(ch.cards) != 1 {
		t.Fatalf("cards = %#v", ch.cards)
	}
	if !strings.Contains(flattenCard(ch.cards[0].Card), "signed-token") {
		t.Fatalf("initial card missing token: %#v", ch.cards[0].Card)
	}
}

type fakeRun []agentport.AgentEvent

func (r fakeRun) Events(context.Context) <-chan agentport.AgentEvent {
	out := make(chan agentport.AgentEvent, len(r))
	for _, event := range r {
		out <- event
	}
	close(out)
	return out
}

type delayedRun []delayedEvent

type delayedEvent struct {
	after time.Duration
	event agentport.AgentEvent
}

func (r delayedRun) Events(ctx context.Context) <-chan agentport.AgentEvent {
	out := make(chan agentport.AgentEvent)
	go func() {
		defer close(out)
		for _, item := range r {
			if item.after > 0 {
				timer := time.NewTimer(item.after)
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
			}
			select {
			case <-ctx.Done():
				return
			case out <- item.event:
			}
		}
	}()
	return out
}

type idleBlockingRun struct {
	events  chan agentport.AgentEvent
	stopped chan struct{}
	once    sync.Once
}

func newIdleBlockingRun(events ...agentport.AgentEvent) *idleBlockingRun {
	run := &idleBlockingRun{
		events:  make(chan agentport.AgentEvent, len(events)),
		stopped: make(chan struct{}),
	}
	for _, event := range events {
		run.events <- event
	}
	return run
}

func (r *idleBlockingRun) Events(context.Context) <-chan agentport.AgentEvent {
	return r.events
}

func (r *idleBlockingRun) Stop(context.Context) error {
	r.once.Do(func() { close(r.stopped) })
	return nil
}

type fakeChannel struct {
	messages          []SendMessageRequest
	cards             []SendCardRequest
	updates           []UpdateCardRequest
	messageUpdates    []UpdateMessageRequest
	messageUpdateErr  error
	messageUpdateErrs []error
}

func (c *fakeChannel) SendMessage(_ context.Context, req SendMessageRequest) (SendMessageResult, error) {
	c.messages = append(c.messages, req)
	return SendMessageResult{MessageID: fmt.Sprintf("message-%d", len(c.messages))}, nil
}

func (c *fakeChannel) SendCard(_ context.Context, req SendCardRequest) (SendCardResult, error) {
	c.cards = append(c.cards, req)
	return SendCardResult{MessageID: fmt.Sprintf("card-message-%d", len(c.cards))}, nil
}

func (c *fakeChannel) UpdateCard(_ context.Context, req UpdateCardRequest) error {
	c.updates = append(c.updates, req)
	return nil
}

func (c *fakeChannel) UpdateMessage(_ context.Context, req UpdateMessageRequest) error {
	c.messageUpdates = append(c.messageUpdates, req)
	if len(c.messageUpdateErrs) > 0 {
		err := c.messageUpdateErrs[0]
		c.messageUpdateErrs = c.messageUpdateErrs[1:]
		return err
	}
	return c.messageUpdateErr
}

func textEvent(delta string) agentport.AgentEvent {
	return agentport.AgentEvent{Type: agentport.EventText, Delta: &delta}
}

func toolUseEvent(id string, name string) agentport.AgentEvent {
	return agentport.AgentEvent{Type: agentport.EventToolUse, ID: &id, Name: &name}
}

func toolResultEvent(id string, output string) agentport.AgentEvent {
	return agentport.AgentEvent{Type: agentport.EventToolResult, ID: &id, Output: &output}
}

func cardRenderOptions(sign func(string) string) cardrender.RenderOptions {
	return cardrender.RenderOptions{SignCallback: sign}
}

func mustCardBody(card map[string]any) string {
	body, _ := card["body"].(map[string]any)
	elements, _ := body["elements"].([]any)
	return strings.TrimSpace(strings.Join(flattenStrings(elements), "\n"))
}

func flattenCard(card map[string]any) string {
	return strings.Join(flattenStrings(card), "\n")
}

func hasCardUpdate(updates []UpdateCardRequest, messageID string, content string) bool {
	for _, update := range updates {
		if update.MessageID == messageID && strings.Contains(flattenCard(update.Card), content) {
			return true
		}
	}
	return false
}

func serializedCardSize(t *testing.T, card map[string]any) int {
	t.Helper()
	encoded, err := json.Marshal(card)
	if err != nil {
		t.Fatalf("marshal card: %v", err)
	}
	return len(encoded)
}

func primaryCardMarkdown(card map[string]any) string {
	body, _ := card["body"].(map[string]any)
	elements, _ := body["elements"].([]any)
	var content strings.Builder
	for _, raw := range elements {
		element, _ := raw.(map[string]any)
		if element["tag"] != "markdown" || element["text_size"] != nil {
			continue
		}
		text, _ := element["content"].(string)
		content.WriteString(text)
	}
	return content.String()
}

func flattenStrings(value any) []string {
	switch typed := value.(type) {
	case []any:
		var out []string
		for _, item := range typed {
			out = append(out, flattenStrings(item)...)
		}
		return out
	case map[string]any:
		var out []string
		for _, item := range typed {
			out = append(out, flattenStrings(item)...)
		}
		return out
	case string:
		return []string{typed}
	default:
		return nil
	}
}
