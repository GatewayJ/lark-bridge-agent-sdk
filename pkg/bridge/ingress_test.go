package bridge

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	internallark "github.com/GatewayJ/lark-bridge-agent-sdk/internal/adapters/lark"
	lark "github.com/larksuite/oapi-sdk-go/v3"
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
	var _ IdentitySource = transport
}

func TestOAPIIngressTransportProvidesIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			writeBridgeOAPIJSON(t, w, map[string]any{
				"code": 0, "msg": "ok", "tenant_access_token": "tenant-token", "expire": 7200,
			})
		case "/open-apis/bot/v3/info":
			writeBridgeOAPIJSON(t, w, map[string]any{
				"code": 0,
				"msg":  "ok",
				"bot": map[string]any{
					"open_id":         "ou_public_bot",
					"app_name":        "Public Bot",
					"activate_status": 2,
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	client := lark.NewClient("cli_public_identity", "secret",
		lark.WithOpenBaseUrl(server.URL),
		lark.WithHttpClient(server.Client()),
		lark.WithReqTimeout(5*time.Second),
	)
	wsClient := larkws.NewClient(
		"cli_public_identity",
		"secret",
		larkws.WithEventHandler(larkdispatcher.NewEventDispatcher("", "")),
	)
	transport, err := NewOAPIIngressTransport(OAPILarkTransportOptions{
		AppID:    "cli_public_identity",
		Client:   client,
		WSClient: wsClient,
	})
	if err != nil {
		t.Fatalf("NewOAPIIngressTransport error = %v", err)
	}

	identity, err := transport.Identity(context.Background())
	if err != nil {
		t.Fatalf("Identity error = %v", err)
	}
	if identity.AppID != "cli_public_identity" || identity.OpenID != "ou_public_bot" ||
		identity.Name != "Public Bot" || identity.ActivateStatus != 2 {
		t.Fatalf("identity = %#v", identity)
	}
}

func TestFromInternalIngressEventMapsUnifiedPayloads(t *testing.T) {
	commentEvent := fromInternalIngressEvent(internallark.IngressEvent{
		Kind:      internallark.IngressEventComment,
		EventID:   "evt_comment",
		EventType: "drive.notice.comment_add_v1",
		Comment: &internallark.IngressComment{
			CommentID:    "comment_1",
			FileToken:    "doc_token",
			Operator:     internallark.IngressSender{OpenID: "ou_commenter"},
			MentionedBot: true,
		},
	})
	if commentEvent.Kind != IngressEventComment || commentEvent.Comment == nil ||
		commentEvent.Comment.CommentID != "comment_1" || commentEvent.Comment.Operator.OpenID != "ou_commenter" {
		t.Fatalf("comment event = %#v", commentEvent)
	}

	internalCardEvent := internallark.IngressEvent{
		Kind:    internallark.IngressEventCardAction,
		EventID: "evt_card",
		CardAction: &internallark.IngressCardAction{
			MessageID: "om_card",
			Action: internallark.IngressCardActionPayload{
				Value:     map[string]any{"command": "stop"},
				FormValue: map[string]any{"reason": "user"},
			},
		},
	}
	cardEvent := fromInternalIngressEvent(internalCardEvent)
	if cardEvent.Kind != IngressEventCardAction || cardEvent.CardAction == nil ||
		cardEvent.CardAction.MessageID != "om_card" || cardEvent.CardAction.Action.Value["command"] != "stop" {
		t.Fatalf("card action event = %#v", cardEvent)
	}
	cardEvent.CardAction.Action.Value["command"] = "changed"
	if got := internalCardEvent.CardAction.Action.Value["command"]; got != "stop" {
		t.Fatalf("public mapping mutated internal value = %v", got)
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
