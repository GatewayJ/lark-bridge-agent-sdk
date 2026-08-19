package bridge

import (
	"context"
	"errors"
	"testing"

	larkdispatcher "github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
)

func TestOAPIIngressTransportAcceptsInjectedWSClient(t *testing.T) {
	wsClient := larkws.NewClient(
		"cli_injected",
		"secret",
		larkws.WithEventHandler(larkdispatcher.NewEventDispatcher("", "")),
	)
	transport, err := NewOAPIIngressTransport(OAPILarkTransportOptions{
		WSClient:         wsClient,
		LanguagePriority: []string{"zh_cn", "zh-CN", "en_us"},
	})
	if err != nil {
		t.Fatalf("NewOAPIIngressTransport error = %v", err)
	}
	if transport == nil {
		t.Fatal("NewOAPIIngressTransport returned nil")
	}
}

func TestOAPIIngressTransportZeroValue(t *testing.T) {
	var transport *OAPIIngressTransport
	if err := transport.Connect(context.Background(), func(context.Context, Envelope) error { return nil }); !errors.Is(err, ErrIngressTransport) {
		t.Fatalf("Connect error = %v, want %v", err, ErrIngressTransport)
	}
	if err := transport.Disconnect(context.Background()); err != nil {
		t.Fatalf("Disconnect error = %v", err)
	}
}
