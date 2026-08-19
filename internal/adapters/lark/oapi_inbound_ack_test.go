package lark

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	appintake "github.com/GatewayJ/lark-bridge-agent-sdk/internal/app/intake"
	channeltypes "github.com/larksuite/oapi-sdk-go/v3/channel/types"
	larkdispatcher "github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
)

func TestOAPIAckAfterIntakeMessageWaitsForHandler(t *testing.T) {
	dispatcher, channel := newOAPIAckAfterIntakeTestChannel()
	started := make(chan struct{})
	release := make(chan struct{})
	channel.OnMessage(func(context.Context, *channeltypes.NormalizedMessage) error {
		close(started)
		<-release
		return nil
	})

	done := make(chan error, 1)
	go func() {
		_, err := dispatcher.Do(context.Background(), oapiMessageEventPayload("evt_wait", "om_wait"))
		done <- err
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("message handler did not start")
	}
	select {
	case err := <-done:
		t.Fatalf("dispatcher returned before intake completed: %v", err)
	default:
	}

	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("dispatcher returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("dispatcher did not return after intake completed")
	}
}

func TestNewOAPITransportSelectsAckAfterIntakeChannel(t *testing.T) {
	durable, err := NewOAPITransport(OAPITransportOptions{
		AppID:          "cli_test",
		AppSecret:      "secret",
		InboundAckMode: InboundAckAfterIntake,
	})
	if err != nil {
		t.Fatalf("NewOAPITransport durable error = %v", err)
	}
	if _, ok := durable.channel.(*oapiAckAfterIntakeChannel); !ok {
		t.Fatalf("durable channel = %T, want *oapiAckAfterIntakeChannel", durable.channel)
	}

	defaultTransport, err := NewOAPITransport(OAPITransportOptions{
		AppID:     "cli_test",
		AppSecret: "secret",
	})
	if err != nil {
		t.Fatalf("NewOAPITransport default error = %v", err)
	}
	if _, ok := defaultTransport.channel.(*oapiAckAfterIntakeChannel); ok {
		t.Fatalf("default channel unexpectedly uses ack-after-intake path")
	}
}

func TestOAPIAckAfterIntakeMessagePropagatesErrorAndAllowsRetry(t *testing.T) {
	dispatcher, channel := newOAPIAckAfterIntakeTestChannel()
	wantErr := errors.New("persist message")
	var calls atomic.Int32
	channel.OnMessage(func(context.Context, *channeltypes.NormalizedMessage) error {
		if calls.Add(1) == 1 {
			return wantErr
		}
		return nil
	})
	payload := oapiMessageEventPayload("evt_retry", "om_retry")

	if _, err := dispatcher.Do(context.Background(), payload); !errors.Is(err, wantErr) {
		t.Fatalf("first dispatch error = %v, want %v", err, wantErr)
	}
	if _, err := dispatcher.Do(context.Background(), payload); err != nil {
		t.Fatalf("retry dispatch error = %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("handler calls = %d, want 2", got)
	}
}

func TestOAPIAckAfterIntakeCommentPropagatesErrorAndAllowsRetry(t *testing.T) {
	dispatcher, channel := newOAPIAckAfterIntakeTestChannel()
	wantErr := errors.New("persist comment")
	var calls atomic.Int32
	channel.OnComment(func(context.Context, *channeltypes.CommentEvent) error {
		if calls.Add(1) == 1 {
			return wantErr
		}
		return nil
	})
	payload := oapiCommentEventPayload("evt_comment_retry", "comment_retry", "file_retry")

	if _, err := dispatcher.Do(context.Background(), payload); !errors.Is(err, wantErr) {
		t.Fatalf("first dispatch error = %v, want %v", err, wantErr)
	}
	if _, err := dispatcher.Do(context.Background(), payload); err != nil {
		t.Fatalf("retry dispatch error = %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("handler calls = %d, want 2", got)
	}
}

func TestOAPITransportRejectsInvalidInboundAckMode(t *testing.T) {
	_, err := NewOAPITransport(OAPITransportOptions{
		channel:        &fakeOAPIChannel{},
		InboundAckMode: InboundAckMode(255),
	})
	if !errors.Is(err, ErrOAPIInboundAckMode) {
		t.Fatalf("NewOAPITransport error = %v, want %v", err, ErrOAPIInboundAckMode)
	}
}

func TestOAPITransportAfterIntakeFailsClosedWithoutHandler(t *testing.T) {
	transport, err := NewOAPITransport(OAPITransportOptions{
		channel:        &fakeOAPIChannel{},
		InboundAckMode: InboundAckAfterIntake,
	})
	if err != nil {
		t.Fatalf("NewOAPITransport error = %v", err)
	}

	err = transport.emit(context.Background(), IncomingEvent{
		Kind:    appintake.EventMessage,
		Message: &appintake.MessageInput{},
	})
	if !errors.Is(err, ErrOAPIIntakeMissing) {
		t.Fatalf("emit error = %v, want %v", err, ErrOAPIIntakeMissing)
	}
}

func newOAPIAckAfterIntakeTestChannel() (*larkdispatcher.EventDispatcher, oapiChannel) {
	dispatcher := larkdispatcher.NewEventDispatcher("", "")
	wsClient := larkws.NewClient(
		"cli_test",
		"secret",
		larkws.WithEventHandler(dispatcher),
	)
	base := &fakeOAPIChannel{}
	config := defaultOAPIChannelConfig(OAPITransportOptions{})
	return dispatcher, newOAPIAckAfterIntakeChannel(base, wsClient, config)
}

func oapiMessageEventPayload(eventID, messageID string) []byte {
	return []byte(fmt.Sprintf(`{
		"schema":"2.0",
		"header":{
			"event_id":%q,
			"event_type":"im.message.receive_v1",
			"create_time":%q
		},
		"event":{
			"sender":{"sender_id":{"open_id":"ou_sender"}},
			"message":{
				"message_id":%q,
				"chat_id":"oc_chat",
				"chat_type":"p2p",
				"message_type":"text",
				"content":"{\"text\":\"hello\"}"
			}
		}
	}`, eventID, fmt.Sprint(time.Now().UnixMilli()), messageID))
}

func oapiCommentEventPayload(eventID, commentID, fileToken string) []byte {
	createdAt := fmt.Sprint(time.Now().UnixMilli())
	return []byte(fmt.Sprintf(`{
		"schema":"2.0",
		"header":{
			"event_id":%q,
			"event_type":"drive.notice.comment_add_v1",
			"create_time":%q
		},
		"event":{
			"comment_id":%q,
			"file_token":%q,
			"file_type":"docx",
			"create_time":%q,
			"user_id":{"open_id":"ou_commenter"}
		}
	}`, eventID, createdAt, commentID, fileToken, createdAt))
}
