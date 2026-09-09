package lark

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	appintake "github.com/GatewayJ/lark-bridge-agent-sdk/internal/app/intake"
	"github.com/gorilla/websocket"
	channeltypes "github.com/larksuite/oapi-sdk-go/v3/channel/types"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
)

func TestOAPITransportManualReconnectPreservesAutomaticRecovery(t *testing.T) {
	var sequence atomic.Int32
	sockets := make(chan *websocket.Conn, 8)
	upgrader := websocket.Upgrader{}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case larkws.GenEndpointUri:
			_ = json.NewEncoder(w).Encode(&larkws.EndpointResp{
				Code: larkws.OK,
				Data: &larkws.Endpoint{
					Url:          strings.Replace(server.URL, "http://", "ws://", 1) + "/ws?device_id=test&service_id=1",
					ClientConfig: &larkws.ClientConfig{ReconnectCount: 1, PingInterval: 3600},
				},
			})
		case "/open-apis/auth/v3/tenant_access_token/internal":
			_, _ = w.Write([]byte(`{"code":0,"tenant_access_token":"test_token","expire":7200}`))
		case "/open-apis/bot/v3/info":
			_, _ = w.Write([]byte(`{"code":0,"bot":{"open_id":"ou_bot","app_name":"Test Bot"}}`))
		case "/ws":
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				t.Errorf("upgrade websocket: %v", err)
				return
			}
			defer conn.Close()
			sockets <- conn
			id := fmt.Sprintf("%d", sequence.Add(1))
			payload := ingressMessagePayload(t, "evt_"+id, "om_"+id, "p2p", "text", `{"text":"hello"}`)
			// The SDK discards stale events before calling the bridge.
			payload = []byte(strings.ReplaceAll(string(payload), `"1000"`, fmt.Sprintf(`"%d"`, time.Now().UnixMilli())))
			frame := larkws.Frame{
				Method: int32(larkws.FrameTypeData), Service: 1, Payload: payload,
				Headers: larkws.Headers{
					{Key: larkws.HeaderType, Value: string(larkws.MessageTypeEvent)},
					{Key: larkws.HeaderMessageID, Value: "ws_" + id},
					{Key: larkws.HeaderSum, Value: "1"},
					{Key: larkws.HeaderSeq, Value: "0"},
				},
			}
			raw, err := frame.Marshal()
			if err != nil {
				t.Errorf("marshal event: %v", err)
				return
			}
			if err := conn.WriteMessage(websocket.BinaryMessage, raw); err != nil {
				t.Errorf("send event: %v", err)
				return
			}
			for {
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	transport, err := NewOAPITransport(OAPITransportOptions{
		AppID: "cli_reconnect", AppSecret: "secret", Domain: server.URL,
		StartTimeout: 2 * time.Second, LogLevel: larkcore.LogLevelError,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = transport.Disconnect(context.Background()) })
	messages := make(chan string, 8)
	handler := reconnectTransportHandler(func(_ context.Context, event IncomingEvent) error {
		if event.Kind == appintake.EventMessage {
			messages <- event.Message.MessageID
		}
		return nil
	})
	awaitMessage := func(want string) {
		t.Helper()
		select {
		case got := <-messages:
			if got != want {
				t.Fatalf("message = %q, want %q", got, want)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out waiting for %s", want)
		}
	}
	ctx := context.Background()
	if err := transport.Connect(ctx, handler); err != nil {
		t.Fatal(err)
	}
	awaitMessage("om_1")
	<-sockets
	if err := transport.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}
	if err := transport.Connect(ctx, handler); err != nil {
		t.Fatal(err)
	}
	awaitMessage("om_2")
	// Drop the replacement connection from the server. Automatic recovery
	// must reconnect and deliver a third message without another Connect call.
	_ = (<-sockets).Close()
	awaitMessage("om_3")
}

func TestOAPITransportIgnoresCallbacksFromReplacedChannel(t *testing.T) {
	oldChannel := &fakeOAPIChannel{}
	newChannel := &fakeOAPIChannel{}
	transport, err := NewOAPITransport(OAPITransportOptions{channel: oldChannel})
	if err != nil {
		t.Fatal(err)
	}
	transport.newChannel = func() (oapiChannel, *larkws.Client) { return newChannel, nil }
	handler := &recordingLarkHandler{}
	ctx := context.Background()
	if err := transport.Connect(ctx, handler); err != nil {
		t.Fatal(err)
	}
	if err := oldChannel.message(ctx, &channeltypes.NormalizedMessage{MessageID: "om_previous"}); err != nil {
		t.Fatal(err)
	}
	if err := transport.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}
	if err := transport.Connect(ctx, handler); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = transport.Disconnect(ctx) })
	oldChannel.disconnected()
	oldChannel.reconnected()
	if err := oldChannel.message(ctx, &channeltypes.NormalizedMessage{MessageID: "om_new"}); err != nil {
		t.Fatal(err)
	}
	if err := newChannel.message(ctx, &channeltypes.NormalizedMessage{MessageID: "om_previous"}); err != nil {
		t.Fatal(err)
	}
	if err := newChannel.message(ctx, &channeltypes.NormalizedMessage{MessageID: "om_new"}); err != nil {
		t.Fatal(err)
	}
	if len(handler.events) != 2 || handler.events[1].Message == nil || handler.events[1].Message.MessageID != "om_new" {
		t.Fatalf("events after replacement = %#v", handler.events)
	}
}

type reconnectTransportHandler func(context.Context, IncomingEvent) error

func (h reconnectTransportHandler) HandleLarkTransportEvent(ctx context.Context, event IncomingEvent) error {
	return h(ctx, event)
}
