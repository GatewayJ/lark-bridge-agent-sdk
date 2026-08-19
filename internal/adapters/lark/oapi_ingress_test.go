package lark

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	larkdispatcher "github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
)

func TestOAPIIngressWebSocketCallbackReturnsPersistenceErrorAndAllowsFeishuRetry(t *testing.T) {
	payload := ingressMessagePayload(t, "evt_retry", "om_retry", "group", "text", `{"text":"hello"}`)
	protocol := newIngressWebSocketProtocol(t, payload, 2)
	dispatcher := larkdispatcher.NewEventDispatcher("", "")
	wsClient := larkws.NewClient(
		"cli_test",
		"secret",
		larkws.WithDomain(protocol.server.URL),
		larkws.WithEventHandler(dispatcher),
	)
	transport, err := NewOAPIIngressTransport(OAPITransportOptions{
		WSClient:     wsClient,
		StartTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewOAPIIngressTransport error = %v", err)
	}

	wantErr := errors.New("persist ingress")
	var calls atomic.Int32
	if err := transport.Connect(context.Background(), func(context.Context, IngressEnvelope) error {
		if calls.Add(1) == 1 {
			return wantErr
		}
		return nil
	}); err != nil {
		t.Fatalf("Connect error = %v", err)
	}

	if response := protocol.awaitResponse(t); response.StatusCode != http.StatusInternalServerError {
		t.Fatalf("persistence failure response code = %d, want %d", response.StatusCode, http.StatusInternalServerError)
	}
	if response := protocol.awaitResponse(t); response.StatusCode != http.StatusOK {
		t.Fatalf("Feishu retry response code = %d, want %d", response.StatusCode, http.StatusOK)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("handler calls = %d, want 2; retry must not be SDK-deduplicated", got)
	}
}

func TestOAPIIngressDispatcherReturnsHandlerErrorUnchanged(t *testing.T) {
	dispatcher, transport := newIngressIntegrationTransport(t, nil)
	wantErr := errors.New("persist ingress")
	if err := transport.Connect(context.Background(), func(context.Context, IngressEnvelope) error {
		return wantErr
	}); err != nil {
		t.Fatalf("Connect error = %v", err)
	}
	t.Cleanup(func() { _ = transport.Disconnect(context.Background()) })

	payload := ingressMessagePayload(t, "evt_error", "om_error", "group", "text", `{"text":"hello"}`)
	if _, err := dispatcher.Do(context.Background(), payload); err != wantErr {
		t.Fatalf("dispatcher error = %v, want unchanged %v", err, wantErr)
	}
}

func TestOAPIIngressNegativeStartTimeoutWaitsForStartupError(t *testing.T) {
	wantErr := errors.New("websocket bootstrap failed")
	socket := &fakeIngressSocket{
		dispatcher: larkdispatcher.NewEventDispatcher("", ""),
		startErr:   wantErr,
	}
	transport, err := NewOAPIIngressTransport(OAPITransportOptions{
		StartTimeout:  -1,
		ingressSocket: socket,
	})
	if err != nil {
		t.Fatalf("NewOAPIIngressTransport error = %v", err)
	}

	for attempt := 1; attempt <= 2; attempt++ {
		err = transport.Connect(context.Background(), func(context.Context, IngressEnvelope) error { return nil })
		if !errors.Is(err, wantErr) {
			t.Fatalf("Connect attempt %d error = %v, want %v", attempt, err, wantErr)
		}
	}
}

func TestOAPIIngressCallbackWaitsForSynchronousHandler(t *testing.T) {
	dispatcher, transport := newIngressIntegrationTransport(t, nil)
	started := make(chan struct{})
	release := make(chan struct{})
	if err := transport.Connect(context.Background(), func(context.Context, IngressEnvelope) error {
		close(started)
		<-release
		return nil
	}); err != nil {
		t.Fatalf("Connect error = %v", err)
	}
	t.Cleanup(func() { _ = transport.Disconnect(context.Background()) })

	done := make(chan error, 1)
	go func() {
		_, err := dispatcher.Do(context.Background(), ingressMessagePayload(t, "evt_wait", "om_wait", "p2p", "text", `{"text":"hello"}`))
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("handler did not start")
	}
	select {
	case err := <-done:
		t.Fatalf("callback returned before durable handler completed: %v", err)
	default:
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("callback error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("callback did not return after handler completed")
	}
}

func TestOAPIIngressDoesNotApplyMentionPolicyBatchingOrProcessingLock(t *testing.T) {
	dispatcher, transport := newIngressIntegrationTransport(t, nil)
	entered := make(chan string, 2)
	release := make(chan struct{})
	if err := transport.Connect(context.Background(), func(_ context.Context, envelope IngressEnvelope) error {
		entered <- envelope.Message.MessageID
		<-release
		return nil
	}); err != nil {
		t.Fatalf("Connect error = %v", err)
	}
	t.Cleanup(func() { _ = transport.Disconnect(context.Background()) })

	payload := ingressMessagePayload(t, "evt_parallel", "om_same", "group", "text", `{"text":"no bot mention"}`)
	done := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := dispatcher.Do(context.Background(), payload)
			done <- err
		}()
	}
	for range 2 {
		select {
		case messageID := <-entered:
			if messageID != "om_same" {
				t.Fatalf("message ID = %q", messageID)
			}
		case <-time.After(time.Second):
			t.Fatal("callbacks were gated, batched, deduplicated, or process-locked")
		}
	}
	close(release)
	for range 2 {
		if err := <-done; err != nil {
			t.Fatalf("callback error = %v", err)
		}
	}
}

func TestOAPIIngressRichPostUsesDeterministicLanguagePriority(t *testing.T) {
	dispatcher, transport := newIngressIntegrationTransport(t, []string{"zh-CN", "en_us"})
	received := make(chan IngressEnvelope, 1)
	if err := transport.Connect(context.Background(), func(_ context.Context, envelope IngressEnvelope) error {
		received <- envelope
		return nil
	}); err != nil {
		t.Fatalf("Connect error = %v", err)
	}
	t.Cleanup(func() { _ = transport.Disconnect(context.Background()) })

	post := map[string]any{
		"en_us": map[string]any{
			"title": "English title",
			"content": [][]any{{
				map[string]any{"tag": "text", "text": "English body"},
				map[string]any{"tag": "img", "image_key": "img_en"},
			}},
		},
		"zh_cn": map[string]any{
			"title":   "中文标题",
			"content": [][]any{{map[string]any{"tag": "text", "text": "旧正文"}}},
			"content_v2": [][]any{{
				map[string]any{"tag": "md", "text": "你好 ![图](img_zh)"},
				map[string]any{"tag": "media", "file_key": "file_zh", "file_name": "设计.pdf", "image_key": "cover_zh"},
			}},
		},
	}
	contentJSON, err := json.Marshal(post)
	if err != nil {
		t.Fatalf("marshal post: %v", err)
	}
	payload := ingressMessagePayload(t, "evt_post", "om_post", "p2p", "post", string(contentJSON))
	if _, err := dispatcher.Do(context.Background(), payload); err != nil {
		t.Fatalf("dispatch post: %v", err)
	}

	envelope := <-received
	if envelope.Message == nil {
		t.Fatal("message is nil")
	}
	content := envelope.Message.Content
	if content.Type != "post" || content.PlainText != "中文标题\n你好 ![图](img_zh)" {
		t.Fatalf("content type/plain text = %q / %q", content.Type, content.PlainText)
	}
	if string(content.Raw) != string(contentJSON) {
		t.Fatalf("raw content = %s, want %s", content.Raw, contentJSON)
	}
	if content.Post == nil || len(content.Post.Locales) != 2 {
		t.Fatalf("post locales = %#v", content.Post)
	}
	zh := content.Post.Locales["zh_cn"]
	if zh.Title != "中文标题" || len(zh.Content) != 1 || len(zh.ContentV2) != 1 {
		t.Fatalf("zh document = %#v", zh)
	}
	if len(content.Resources) != 2 ||
		content.Resources[0].Type != "image" || content.Resources[0].FileKey != "img_zh" ||
		content.Resources[1].Type != "media" || content.Resources[1].FileKey != "file_zh" ||
		content.Resources[1].CoverImageKey != "cover_zh" {
		t.Fatalf("resources = %#v", content.Resources)
	}
}

func TestIngressLocaleFallbackIsSorted(t *testing.T) {
	content := parseIngressMessageContent("post", `{
		"zh_cn":{"title":"中文","content":[[{"tag":"text","text":"正文"}]]},
		"en_us":{"title":"English","content":[[{"tag":"text","text":"Body"}]]}
	}`, nil)
	if content.PlainText != "English\nBody" {
		t.Fatalf("fallback plain text = %q, want sorted en_us locale", content.PlainText)
	}
}

func TestIngressRawContentRemainsValidJSONForPlainMergeForward(t *testing.T) {
	content := parseIngressMessageContent("merge_forward", "Merged and Forwarded Message", nil)
	if content.PlainText != "Merged and Forwarded Message" {
		t.Fatalf("plain text = %q", content.PlainText)
	}
	if !json.Valid(content.Raw) || string(content.Raw) != `"Merged and Forwarded Message"` {
		t.Fatalf("raw content = %s, want valid JSON string", content.Raw)
	}
}

func newIngressIntegrationTransport(t *testing.T, languagePriority []string) (*larkdispatcher.EventDispatcher, *OAPIIngressTransport) {
	t.Helper()
	dispatcher := larkdispatcher.NewEventDispatcher("", "")
	socket := &fakeIngressSocket{dispatcher: dispatcher}
	transport, err := NewOAPIIngressTransport(OAPITransportOptions{
		LanguagePriority: languagePriority,
		ingressSocket:    socket,
	})
	if err != nil {
		t.Fatalf("NewOAPIIngressTransport error = %v", err)
	}
	return dispatcher, transport
}

func ingressMessagePayload(t *testing.T, eventID, messageID, chatType, messageType, content string) []byte {
	t.Helper()
	payload := map[string]any{
		"schema": "2.0",
		"header": map[string]any{
			"event_id":    eventID,
			"event_type":  "im.message.receive_v1",
			"app_id":      "cli_test",
			"tenant_key":  "tenant_test",
			"create_time": "1000",
		},
		"event": map[string]any{
			"sender": map[string]any{
				"sender_id":   map[string]any{"open_id": "ou_sender", "user_id": "user_sender", "union_id": "on_sender"},
				"sender_type": "user",
				"tenant_key":  "tenant_test",
			},
			"message": map[string]any{
				"message_id":   messageID,
				"chat_id":      "oc_chat",
				"chat_type":    chatType,
				"message_type": messageType,
				"content":      content,
				"create_time":  "1000",
			},
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return raw
}

type fakeIngressSocket struct {
	dispatcher *larkdispatcher.EventDispatcher
	startErr   error

	mu      sync.Mutex
	onReady func()
	closed  atomic.Bool
}

func (s *fakeIngressSocket) Start(ctx context.Context) error {
	s.mu.Lock()
	onReady := s.onReady
	startErr := s.startErr
	s.mu.Unlock()
	if startErr != nil {
		return startErr
	}
	if onReady != nil {
		onReady()
	}
	<-ctx.Done()
	return ctx.Err()
}

func (s *fakeIngressSocket) Close() {
	s.closed.Store(true)
}

func (s *fakeIngressSocket) EventHandler() *larkdispatcher.EventDispatcher {
	return s.dispatcher
}

func (s *fakeIngressSocket) SetOnReady(handler func()) {
	s.mu.Lock()
	s.onReady = handler
	s.mu.Unlock()
}

type ingressWebSocketProtocol struct {
	server    *httptest.Server
	responses chan larkws.Response
	errs      chan error
}

func newIngressWebSocketProtocol(t *testing.T, payload []byte, deliveries int) *ingressWebSocketProtocol {
	t.Helper()
	protocol := &ingressWebSocketProtocol{
		responses: make(chan larkws.Response, deliveries),
		errs:      make(chan error, 1),
	}
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case larkws.GenEndpointUri:
			endpoint := strings.Replace(server.URL, "http://", "ws://", 1) + "/ws?device_id=test&service_id=1"
			if err := json.NewEncoder(w).Encode(&larkws.EndpointResp{
				Code: larkws.OK,
				Data: &larkws.Endpoint{
					Url: endpoint,
					ClientConfig: &larkws.ClientConfig{
						ReconnectCount: 0,
						PingInterval:   3600,
					},
				},
			}); err != nil {
				protocol.reportError(fmt.Errorf("encode websocket endpoint: %w", err))
			}
		case "/ws":
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				protocol.reportError(fmt.Errorf("upgrade websocket: %w", err))
				return
			}
			defer conn.Close()
			for delivery := 0; delivery < deliveries; delivery++ {
				headers := larkws.Headers{}
				headers.Add(larkws.HeaderType, string(larkws.MessageTypeEvent))
				headers.Add(larkws.HeaderMessageID, "ws_retry_message")
				headers.Add(larkws.HeaderTraceID, "ws_retry_trace")
				headers.Add(larkws.HeaderSum, "1")
				headers.Add(larkws.HeaderSeq, "0")
				frame := &larkws.Frame{
					Method:  int32(larkws.FrameTypeData),
					Service: 1,
					Headers: headers,
					Payload: payload,
				}
				raw, err := frame.Marshal()
				if err != nil {
					protocol.reportError(fmt.Errorf("marshal delivery frame: %w", err))
					return
				}
				if err := conn.WriteMessage(websocket.BinaryMessage, raw); err != nil {
					protocol.reportError(fmt.Errorf("write delivery frame: %w", err))
					return
				}
				responseFrame, err := readIngressCallbackFrame(conn)
				if err != nil {
					protocol.reportError(err)
					return
				}
				var response larkws.Response
				if err := json.Unmarshal(responseFrame.Payload, &response); err != nil {
					protocol.reportError(fmt.Errorf("unmarshal callback response: %w", err))
					return
				}
				protocol.responses <- response
			}
		default:
			http.NotFound(w, r)
		}
	}))
	protocol.server = server
	t.Cleanup(server.Close)
	return protocol
}

func readIngressCallbackFrame(conn *websocket.Conn) (larkws.Frame, error) {
	for {
		messageType, raw, err := conn.ReadMessage()
		if err != nil {
			return larkws.Frame{}, fmt.Errorf("read callback response: %w", err)
		}
		if messageType != websocket.BinaryMessage {
			return larkws.Frame{}, fmt.Errorf("callback response message type = %d", messageType)
		}
		var frame larkws.Frame
		if err := frame.Unmarshal(raw); err != nil {
			return larkws.Frame{}, fmt.Errorf("unmarshal callback frame: %w", err)
		}
		if larkws.FrameType(frame.Method) == larkws.FrameTypeData {
			return frame, nil
		}
	}
}

func (p *ingressWebSocketProtocol) reportError(err error) {
	select {
	case p.errs <- err:
	default:
	}
}

func (p *ingressWebSocketProtocol) awaitResponse(t *testing.T) larkws.Response {
	t.Helper()
	select {
	case response := <-p.responses:
		return response
	case err := <-p.errs:
		t.Fatalf("WebSocket protocol error: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for WebSocket callback response")
	}
	return larkws.Response{}
}
