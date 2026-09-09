package lark

import (
	"context"
	"errors"
	"testing"

	appintake "github.com/GatewayJ/lark-bridge-agent-sdk/internal/app/intake"
)

func TestAdapterReconnectPreservesPendingMessagesAndIntakeLifetime(t *testing.T) {
	var delivered []appintake.NormalizedEvent
	intake := &reconnectIntake{Queue: appintake.NewQueue(appintake.QueueOptions{
		Handler: func(_ context.Context, batch appintake.Batch) error {
			delivered = append(delivered, batch.Events...)
			return nil
		},
	})}
	transport := NewFakeTransport(BotIdentity{OpenID: "ou_bot", Name: "Before"})
	adapter, err := NewAdapter(AdapterOptions{Transport: transport, Intake: intake})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := adapter.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adapter.Disconnect(ctx) })
	emit := func(id string) {
		t.Helper()
		if err := transport.Emit(ctx, IncomingEvent{
			Kind: appintake.EventMessage,
			Message: &appintake.MessageInput{
				MessageID: id, ChatID: "oc_dm", ChatType: appintake.ChatTypeP2P,
				Sender: appintake.Actor{OpenID: "ou_user"}, Content: "hello",
			},
		}); err != nil {
			t.Fatal(err)
		}
	}
	emit("om_before")
	transport.Identity.Name = "After"
	if err := adapter.Reconnect(ctx); err != nil {
		t.Fatal(err)
	}
	emit("om_after")
	if err := intake.FlushAll(ctx); err != nil {
		t.Fatal(err)
	}
	if len(delivered) != 2 || delivered[0].Message.MessageID != "om_before" || delivered[1].Message.MessageID != "om_after" {
		t.Fatalf("delivered messages = %#v", delivered)
	}
	if intake.starts != 1 || !adapter.Started() || adapter.BotIdentity().Name != "After" {
		t.Fatalf("starts=%d connected=%v identity=%#v", intake.starts, adapter.Started(), adapter.BotIdentity())
	}
	if err := adapter.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}
	if err := adapter.Reconnect(ctx); !errors.Is(err, ErrAdapterNotStarted) {
		t.Fatalf("reconnect after shutdown = %v, want ErrAdapterNotStarted", err)
	}
	if _, err := intake.Push(delivered[0]); !errors.Is(err, appintake.ErrQueueClosed) {
		t.Fatalf("intake after shutdown = %v, want ErrQueueClosed", err)
	}
}

func TestAdapterReconnectFailureCanRetry(t *testing.T) {
	for _, phase := range []string{"disconnect", "connect", "identity", "projection"} {
		t.Run(phase, func(t *testing.T) {
			transport := NewFakeTransport(BotIdentity{OpenID: "ou_bot"})
			var projectionErr error
			adapter, err := NewAdapter(AdapterOptions{
				Transport: transport,
				ProfileProjection: ProfileProjectionHookFunc(func(context.Context, ProfileProjectionRequest) (ProfileProjectionResult, error) {
					return ProfileProjectionResult{}, projectionErr
				}),
			})
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			if err := adapter.Start(ctx); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = adapter.Disconnect(ctx) })
			want := errors.New(phase + " failed")
			switch phase {
			case "disconnect":
				transport.DisconnectErr = want
			case "connect":
				transport.ConnectErr = want
			case "identity":
				transport.IdentityErr = want
			case "projection":
				projectionErr = want
			}
			if err := adapter.Reconnect(ctx); !errors.Is(err, want) {
				t.Fatalf("reconnect error = %v, want %v", err, want)
			}
			if adapter.Started() {
				t.Fatal("failed reconnect reported a started adapter")
			}
			if phase != "disconnect" {
				if err := transport.Emit(ctx, IncomingEvent{}); !errors.Is(err, ErrFakeTransportNotConnected) {
					t.Fatalf("failed reconnect left transport connected: %v", err)
				}
			}
			transport.SetErrors(FakeTransportErrors{})
			projectionErr = nil
			if err := adapter.Reconnect(ctx); err != nil {
				t.Fatalf("retry reconnect: %v", err)
			}
			if !adapter.Started() {
				t.Fatal("successful retry did not mark the adapter started")
			}
		})
	}
}

type reconnectIntake struct {
	*appintake.Queue
	starts int
}

func (i *reconnectIntake) Start(context.Context) error {
	i.starts++
	return nil
}

func (i *reconnectIntake) HandleLarkEvent(_ context.Context, event appintake.NormalizedEvent) error {
	_, err := i.Push(event)
	return err
}
