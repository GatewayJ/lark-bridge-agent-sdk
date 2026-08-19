package lark

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	larksdk "github.com/larksuite/oapi-sdk-go/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkevent "github.com/larksuite/oapi-sdk-go/v3/event"
	larkdispatcher "github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkcallback "github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
)

var (
	ErrIngressTransport          = errors.New("lark oapi ingress transport is unavailable")
	ErrIngressHandler            = errors.New("lark oapi ingress handler is required")
	ErrIngressHandlerUnavailable = errors.New("lark oapi ingress handler is unavailable")
	ErrIngressAlreadyStarted     = errors.New("lark oapi ingress transport is already started")
	ErrIngressDispatcher         = errors.New("lark oapi ingress event dispatcher is required")
	ErrIngressHandlerRegistered  = errors.New("lark oapi ingress event handler is already registered")
	ingressMarkdownImageKeyRE    = regexp.MustCompile(`!\[[^]]*\]\(([^)]+)\)`)
)

type IngressEventKind string

const (
	IngressEventMessage    IngressEventKind = "message"
	IngressEventComment    IngressEventKind = "comment"
	IngressEventCardAction IngressEventKind = "card_action"
)

type IngressHandler func(context.Context, IngressEvent) error

type IngressTransport interface {
	Connect(context.Context, IngressHandler) error
	Disconnect(context.Context) error
}

type IdentitySource interface {
	Identity(context.Context) (IngressIdentity, error)
}

type IngressIdentity struct {
	AppID          string
	OpenID         string
	UserID         string
	UnionID        string
	Name           string
	ActivateStatus int
	Raw            json.RawMessage
}

type IngressEvent struct {
	Kind       IngressEventKind
	EventID    string
	EventType  string
	AppID      string
	TenantKey  string
	CreateTime time.Time
	Message    *IngressMessage
	Comment    *IngressComment
	CardAction *IngressCardAction
	Raw        json.RawMessage
}

// IngressEnvelope is retained as an alias for message-only integrations.
type IngressEnvelope = IngressEvent

type IngressMessage struct {
	MessageID  string
	RootID     string
	ParentID   string
	ThreadID   string
	ChatID     string
	ChatType   string
	CreateTime time.Time
	Sender     IngressSender
	Mentions   []IngressMention
	Content    IngressMessageContent
}

type IngressComment struct {
	CommentID    string
	ReplyID      string
	FileToken    string
	FileType     string
	NoticeType   string
	Operator     IngressSender
	MentionedBot bool
	CreateTime   time.Time
}

type IngressCardAction struct {
	Token        string
	Host         string
	DeliveryType string
	MessageID    string
	ChatID       string
	Operator     IngressSender
	Action       IngressCardActionPayload
	Context      IngressCardActionContext
}

type IngressCardActionPayload struct {
	Value      map[string]any
	Tag        string
	Option     string
	Timezone   string
	Name       string
	FormValue  map[string]any
	InputValue string
	Options    []string
	Checked    bool
}

type IngressCardActionContext struct {
	URL           string
	PreviewToken  string
	OpenMessageID string
	OpenChatID    string
}

type IngressSender struct {
	OpenID    string
	UserID    string
	UnionID   string
	Type      string
	TenantKey string
}

type IngressMention struct {
	Key       string
	OpenID    string
	UserID    string
	UnionID   string
	Name      string
	Type      string
	TenantKey string
}

type IngressMessageContent struct {
	Type      string
	PlainText string
	Raw       json.RawMessage
	Post      *IngressRichPost
	Resources []IngressResource
}

type IngressRichPost struct {
	Locales map[string]IngressRichDocument
}

type IngressRichDocument struct {
	Title     string
	Content   [][]IngressRichElement
	ContentV2 [][]IngressRichElement
	Raw       json.RawMessage
}

type IngressRichElement struct {
	Tag       string
	Text      string
	Href      string
	UserID    string
	UserName  string
	ImageKey  string
	FileKey   string
	FileName  string
	Language  string
	EmojiType string
	Raw       json.RawMessage
}

type IngressResource struct {
	Type          string
	FileKey       string
	FileName      string
	DurationMs    *int
	CoverImageKey string
}

type ingressSocket interface {
	Start(context.Context) error
	Close()
	EventHandler() *larkdispatcher.EventDispatcher
	SetOnReady(func())
}

type OAPIIngressTransport struct {
	mu sync.RWMutex

	client           *larksdk.Client
	appID            string
	socket           ingressSocket
	languagePriority []string
	startTimeout     time.Duration
	startCancel      context.CancelFunc
	handler          IngressHandler
	ready            chan struct{}
	started          bool
}

var _ IngressTransport = (*OAPIIngressTransport)(nil)
var _ IdentitySource = (*OAPIIngressTransport)(nil)
var _ ingressSocket = (*larkws.Client)(nil)

func NewOAPIIngressTransport(options OAPITransportOptions) (*OAPIIngressTransport, error) {
	client := options.Client
	if client == nil && hasOAPICredentials(options) {
		client = newOAPIClient(options)
	}
	socket := options.ingressSocket
	if socket == nil {
		wsClient := options.WSClient
		if wsClient == nil {
			if !hasOAPICredentials(options) {
				return nil, ErrOAPIAppCredentials
			}
			wsClient = newOAPIWSClient(options)
		}
		socket = wsClient
	}
	if socket == nil {
		return nil, ErrIngressTransport
	}
	dispatcher := socket.EventHandler()
	if dispatcher == nil {
		return nil, ErrIngressDispatcher
	}

	startTimeout := options.StartTimeout
	if startTimeout == 0 {
		startTimeout = DefaultOAPIStartTimeout
	}
	transport := &OAPIIngressTransport{
		client:           client,
		appID:            options.AppID,
		socket:           socket,
		languagePriority: append([]string(nil), options.LanguagePriority...),
		startTimeout:     startTimeout,
	}
	if err := registerIngressHandlers(dispatcher, transport); err != nil {
		return nil, err
	}
	return transport, nil
}

func registerIngressHandlers(dispatcher *larkdispatcher.EventDispatcher, transport *OAPIIngressTransport) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("%w: %v", ErrIngressHandlerRegistered, recovered)
		}
	}()
	dispatcher.OnP2MessageReceiveV1(transport.handleMessage)
	dispatcher.OnCustomizedEvent("drive.notice.comment_add_v1", transport.handleComment)
	dispatcher.OnP2CardActionTrigger(transport.handleCardAction)
	return nil
}

func (t *OAPIIngressTransport) Connect(ctx context.Context, handler IngressHandler) error {
	if t == nil || t.socket == nil {
		return ErrIngressTransport
	}
	if handler == nil {
		return ErrIngressHandler
	}

	ready := make(chan struct{}, 1)
	done := make(chan error, 1)
	startCtx, cancel := context.WithCancel(ctx)

	t.mu.Lock()
	if t.started {
		t.mu.Unlock()
		cancel()
		return ErrIngressAlreadyStarted
	}
	t.started = true
	t.handler = handler
	t.ready = ready
	t.startCancel = cancel
	t.socket.SetOnReady(t.signalReady)
	t.mu.Unlock()

	go func() {
		done <- t.socket.Start(startCtx)
	}()

	var timer *time.Timer
	var timeout <-chan time.Time
	if t.startTimeout >= 0 {
		timer = time.NewTimer(t.startTimeout)
		timeout = timer.C
		defer timer.Stop()
	}
	select {
	case err := <-done:
		if err == nil {
			err = ErrIngressTransport
		}
		t.resetFailedStart()
		return err
	case <-ready:
		return nil
	case <-timeout:
		cancel()
		t.socket.Close()
		t.resetFailedStart()
		return ErrOAPIStartTimeout
	case <-ctx.Done():
		cancel()
		t.socket.Close()
		t.resetFailedStart()
		return ctx.Err()
	}
}

func (t *OAPIIngressTransport) Disconnect(context.Context) error {
	if t == nil || t.socket == nil {
		return nil
	}
	t.mu.Lock()
	if t.startCancel != nil {
		t.startCancel()
	}
	t.startCancel = nil
	t.handler = nil
	t.ready = nil
	t.started = false
	t.mu.Unlock()
	t.socket.Close()
	return nil
}

func (t *OAPIIngressTransport) Identity(ctx context.Context) (IngressIdentity, error) {
	if t == nil {
		return IngressIdentity{}, ErrIngressTransport
	}
	if t.client == nil {
		return IngressIdentity{}, ErrOAPIClient
	}
	return fetchIngressIdentity(ctx, t.client, t.appID)
}

func (t *OAPIIngressTransport) handleMessage(
	ctx context.Context,
	event *larkim.P2MessageReceiveV1,
) error {
	return t.dispatch(ctx, mapIngressEnvelope(event, t.languagePriority))
}

func (t *OAPIIngressTransport) handleComment(ctx context.Context, event *larkevent.EventReq) error {
	return t.dispatch(ctx, mapIngressCommentEvent(event))
}

func (t *OAPIIngressTransport) handleCardAction(
	ctx context.Context,
	event *larkcallback.CardActionTriggerEvent,
) (*larkcallback.CardActionTriggerResponse, error) {
	return nil, t.dispatch(ctx, mapIngressCardActionEvent(event))
}

func (t *OAPIIngressTransport) dispatch(ctx context.Context, event IngressEvent) error {
	t.mu.RLock()
	handler := t.handler
	t.mu.RUnlock()
	if handler == nil {
		return ErrIngressHandlerUnavailable
	}
	return handler(ctx, event)
}

func (t *OAPIIngressTransport) signalReady() {
	t.mu.RLock()
	ready := t.ready
	t.mu.RUnlock()
	if ready == nil {
		return
	}
	select {
	case ready <- struct{}{}:
	default:
	}
}

func (t *OAPIIngressTransport) resetFailedStart() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.started = false
	t.handler = nil
	t.ready = nil
	t.startCancel = nil
}

func mapIngressEnvelope(event *larkim.P2MessageReceiveV1, languagePriority []string) IngressEnvelope {
	envelope := IngressEvent{Kind: IngressEventMessage}
	if event == nil {
		return envelope
	}
	fillIngressEventBase(&envelope, event.EventReq, event.EventV2Base, event)
	if event.Event == nil || event.Event.Message == nil {
		return envelope
	}

	message := event.Event.Message
	envelope.Message = &IngressMessage{
		MessageID:  ingressStringValue(message.MessageId),
		RootID:     ingressStringValue(message.RootId),
		ParentID:   ingressStringValue(message.ParentId),
		ThreadID:   ingressStringValue(message.ThreadId),
		ChatID:     ingressStringValue(message.ChatId),
		ChatType:   ingressStringValue(message.ChatType),
		CreateTime: parseIngressMillis(ingressStringValue(message.CreateTime)),
		Content:    parseIngressMessageContent(ingressStringValue(message.MessageType), ingressStringValue(message.Content), languagePriority),
	}
	if sender := event.Event.Sender; sender != nil {
		envelope.Message.Sender.Type = ingressStringValue(sender.SenderType)
		envelope.Message.Sender.TenantKey = ingressStringValue(sender.TenantKey)
		if sender.SenderId != nil {
			envelope.Message.Sender.OpenID = ingressStringValue(sender.SenderId.OpenId)
			envelope.Message.Sender.UserID = ingressStringValue(sender.SenderId.UserId)
			envelope.Message.Sender.UnionID = ingressStringValue(sender.SenderId.UnionId)
		}
	}
	for _, mention := range message.Mentions {
		if mention == nil {
			continue
		}
		mapped := IngressMention{
			Key:       ingressStringValue(mention.Key),
			Name:      ingressStringValue(mention.Name),
			Type:      ingressStringValue(mention.MentionedType),
			TenantKey: ingressStringValue(mention.TenantKey),
		}
		if mention.Id != nil {
			mapped.OpenID = ingressStringValue(mention.Id.OpenId)
			mapped.UserID = ingressStringValue(mention.Id.UserId)
			mapped.UnionID = ingressStringValue(mention.Id.UnionId)
		}
		envelope.Message.Mentions = append(envelope.Message.Mentions, mapped)
	}
	return envelope
}

func mapIngressCommentEvent(event *larkevent.EventReq) IngressEvent {
	mapped := IngressEvent{Kind: IngressEventComment}
	if event == nil {
		return mapped
	}
	fillIngressEventBase(&mapped, event, nil, event)
	var wire struct {
		Event struct {
			CommentID   string `json:"comment_id"`
			ReplyID     string `json:"reply_id"`
			FileToken   string `json:"file_token"`
			FileType    string `json:"file_type"`
			CreateTime  string `json:"create_time"`
			ActionTime  string `json:"action_time"`
			IsMentioned *bool  `json:"is_mentioned"`
			IsMention   *bool  `json:"is_mention"`
			UserID      *struct {
				OpenID  string `json:"open_id"`
				UserID  string `json:"user_id"`
				UnionID string `json:"union_id"`
			} `json:"user_id"`
			NoticeMeta *struct {
				FileToken   string `json:"file_token"`
				FileType    string `json:"file_type"`
				NoticeType  string `json:"notice_type"`
				Timestamp   string `json:"timestamp"`
				IsMentioned *bool  `json:"is_mentioned"`
				FromUserID  *struct {
					OpenID  string `json:"open_id"`
					UserID  string `json:"user_id"`
					UnionID string `json:"union_id"`
				} `json:"from_user_id"`
			} `json:"notice_meta"`
		} `json:"event"`
	}
	if json.Unmarshal(mapped.Raw, &wire) != nil {
		return mapped
	}
	comment := &IngressComment{
		CommentID:  wire.Event.CommentID,
		ReplyID:    wire.Event.ReplyID,
		FileToken:  wire.Event.FileToken,
		FileType:   wire.Event.FileType,
		CreateTime: parseIngressMillis(ingressFirstNonEmpty(wire.Event.CreateTime, wire.Event.ActionTime)),
	}
	if wire.Event.UserID != nil {
		comment.Operator = IngressSender{
			OpenID:  wire.Event.UserID.OpenID,
			UserID:  wire.Event.UserID.UserID,
			UnionID: wire.Event.UserID.UnionID,
			Type:    "user",
		}
	}
	if notice := wire.Event.NoticeMeta; notice != nil {
		comment.FileToken = ingressFirstNonEmpty(comment.FileToken, notice.FileToken)
		comment.FileType = ingressFirstNonEmpty(comment.FileType, notice.FileType)
		comment.NoticeType = notice.NoticeType
		if comment.CreateTime.IsZero() {
			comment.CreateTime = parseIngressMillis(notice.Timestamp)
		}
		if notice.FromUserID != nil {
			comment.Operator = IngressSender{
				OpenID:  notice.FromUserID.OpenID,
				UserID:  notice.FromUserID.UserID,
				UnionID: notice.FromUserID.UnionID,
				Type:    "user",
			}
		}
		if notice.IsMentioned != nil {
			comment.MentionedBot = *notice.IsMentioned
		}
	}
	if wire.Event.IsMentioned != nil {
		comment.MentionedBot = *wire.Event.IsMentioned
	} else if wire.Event.IsMention != nil {
		comment.MentionedBot = *wire.Event.IsMention
	}
	if comment.CreateTime.IsZero() {
		comment.CreateTime = mapped.CreateTime
	}
	mapped.Comment = comment
	return mapped
}

func mapIngressCardActionEvent(event *larkcallback.CardActionTriggerEvent) IngressEvent {
	mapped := IngressEvent{Kind: IngressEventCardAction}
	if event == nil {
		return mapped
	}
	fillIngressEventBase(&mapped, event.EventReq, event.EventV2Base, event)
	if event.Event == nil {
		return mapped
	}
	request := event.Event
	action := &IngressCardAction{
		Token:        request.Token,
		Host:         request.Host,
		DeliveryType: request.DeliveryType,
	}
	if request.Operator != nil {
		action.Operator = IngressSender{
			OpenID:    request.Operator.OpenID,
			UserID:    ingressStringValue(request.Operator.UserID),
			Type:      "user",
			TenantKey: ingressStringValue(request.Operator.TenantKey),
		}
	}
	if request.Action != nil {
		action.Action = IngressCardActionPayload{
			Value:      cloneIngressMap(request.Action.Value),
			Tag:        request.Action.Tag,
			Option:     request.Action.Option,
			Timezone:   request.Action.Timezone,
			Name:       request.Action.Name,
			FormValue:  cloneIngressMap(request.Action.FormValue),
			InputValue: request.Action.InputValue,
			Options:    append([]string(nil), request.Action.Options...),
			Checked:    request.Action.Checked,
		}
	}
	if request.Context != nil {
		action.MessageID = request.Context.OpenMessageID
		action.ChatID = request.Context.OpenChatID
		action.Context = IngressCardActionContext{
			URL:           request.Context.URL,
			PreviewToken:  request.Context.PreviewToken,
			OpenMessageID: request.Context.OpenMessageID,
			OpenChatID:    request.Context.OpenChatID,
		}
	}
	mapped.CardAction = action
	return mapped
}

func fillIngressEventBase(event *IngressEvent, request *larkevent.EventReq, base *larkevent.EventV2Base, fallback any) {
	if event == nil {
		return
	}
	if request != nil {
		event.Raw = cloneRawMessage(request.Body)
	}
	if len(event.Raw) == 0 && fallback != nil {
		if raw, err := json.Marshal(fallback); err == nil {
			event.Raw = raw
		}
	}
	if base != nil && base.Header != nil {
		event.EventID = base.Header.EventID
		event.EventType = base.Header.EventType
		event.AppID = base.Header.AppID
		event.TenantKey = base.Header.TenantKey
		event.CreateTime = parseIngressMillis(base.Header.CreateTime)
	}
	var wire struct {
		Header struct {
			EventID    string `json:"event_id"`
			EventType  string `json:"event_type"`
			AppID      string `json:"app_id"`
			TenantKey  string `json:"tenant_key"`
			CreateTime string `json:"create_time"`
		} `json:"header"`
	}
	if json.Unmarshal(event.Raw, &wire) != nil {
		return
	}
	event.EventID = ingressFirstNonEmpty(event.EventID, wire.Header.EventID)
	event.EventType = ingressFirstNonEmpty(event.EventType, wire.Header.EventType)
	event.AppID = ingressFirstNonEmpty(event.AppID, wire.Header.AppID)
	event.TenantKey = ingressFirstNonEmpty(event.TenantKey, wire.Header.TenantKey)
	if event.CreateTime.IsZero() {
		event.CreateTime = parseIngressMillis(wire.Header.CreateTime)
	}
}

func fetchIngressIdentity(ctx context.Context, client *larksdk.Client, appID string) (IngressIdentity, error) {
	response, err := client.Get(ctx, "/open-apis/bot/v3/info", nil, larkcore.AccessTokenTypeTenant)
	if err != nil {
		return IngressIdentity{}, err
	}
	if response == nil {
		return IngressIdentity{}, errors.New("lark bot identity response is empty")
	}
	if response.StatusCode != http.StatusOK {
		return IngressIdentity{}, fmt.Errorf("lark bot identity returned HTTP status %d", response.StatusCode)
	}
	var wire struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Bot  struct {
			OpenID         string `json:"open_id"`
			AppName        string `json:"app_name"`
			ActivateStatus int    `json:"activate_status"`
		} `json:"bot"`
	}
	if err := json.Unmarshal(response.RawBody, &wire); err != nil {
		return IngressIdentity{}, fmt.Errorf("decode lark bot identity: %w", err)
	}
	if wire.Code != 0 {
		return IngressIdentity{}, &OAPIError{Operation: "get bot identity", Code: wire.Code, Message: wire.Msg}
	}
	if strings.TrimSpace(wire.Bot.OpenID) == "" {
		return IngressIdentity{}, errors.New("lark bot identity open id is missing")
	}
	return IngressIdentity{
		AppID:          appID,
		OpenID:         wire.Bot.OpenID,
		Name:           wire.Bot.AppName,
		ActivateStatus: wire.Bot.ActivateStatus,
		Raw:            cloneRawMessage(response.RawBody),
	}, nil
}

func ingressFirstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func cloneIngressMap(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func parseIngressMessageContent(messageType, rawContent string, languagePriority []string) IngressMessageContent {
	content := IngressMessageContent{
		Type: messageType,
		Raw:  ingressRawContent(rawContent),
	}
	if messageType == "post" {
		post := parseIngressRichPost(content.Raw)
		content.Post = post
		if locale, ok := selectIngressLocale(post, languagePriority); ok {
			document := post.Locales[locale]
			content.PlainText = ingressDocumentPlainText(document)
			content.Resources = ingressDocumentResources(document)
		}
		return content
	}

	plainText, resources := parseIngressFlatContent(messageType, content.Raw)
	content.PlainText = plainText
	content.Resources = resources
	return content
}

func parseIngressFlatContent(messageType string, raw json.RawMessage) (string, []IngressResource) {
	if messageType == "merge_forward" {
		var text string
		if json.Unmarshal(raw, &text) == nil {
			return text, nil
		}
		return string(raw), nil
	}
	fields := map[string]json.RawMessage{}
	if json.Unmarshal(raw, &fields) != nil {
		return "[unsupported message]", nil
	}
	stringField := func(key string) string {
		var value string
		_ = json.Unmarshal(fields[key], &value)
		return value
	}
	switch messageType {
	case "text":
		return stringField("text"), nil
	case "image":
		key := stringField("image_key")
		if key == "" {
			return "[image]", nil
		}
		return fmt.Sprintf("![image](%s)", key), []IngressResource{{Type: "image", FileKey: key}}
	case "file":
		key, name := stringField("file_key"), stringField("file_name")
		if key == "" {
			return "[file]", nil
		}
		return fmt.Sprintf(`<file key="%s" name="%s"/>`, html.EscapeString(key), html.EscapeString(name)), []IngressResource{{Type: "file", FileKey: key, FileName: name}}
	case "audio":
		key := stringField("file_key")
		if key == "" {
			return "[audio]", nil
		}
		duration := ingressJSONInt(fields["duration"])
		return fmt.Sprintf(`<audio key="%s"/>`, html.EscapeString(key)), []IngressResource{{Type: "audio", FileKey: key, DurationMs: duration}}
	case "media", "video":
		key, name := stringField("file_key"), stringField("file_name")
		if key == "" {
			return "[video]", nil
		}
		return fmt.Sprintf(`<video key="%s" name="%s"/>`, html.EscapeString(key), html.EscapeString(name)), []IngressResource{{
			Type:          "video",
			FileKey:       key,
			FileName:      name,
			DurationMs:    ingressJSONInt(fields["duration"]),
			CoverImageKey: stringField("image_key"),
		}}
	case "sticker":
		key := stringField("file_key")
		if key == "" {
			return "[sticker]", nil
		}
		return fmt.Sprintf(`<sticker key="%s"/>`, html.EscapeString(key)), []IngressResource{{Type: "sticker", FileKey: key}}
	case "folder":
		return fmt.Sprintf(`<folder key="%s" name="%s"/>`, html.EscapeString(stringField("file_key")), html.EscapeString(stringField("file_name"))), nil
	case "share_chat":
		return fmt.Sprintf(`<group_card id="%s"/>`, html.EscapeString(stringField("chat_id"))), nil
	case "share_user":
		return fmt.Sprintf(`<contact_card id="%s"/>`, html.EscapeString(stringField("user_id"))), nil
	case "interactive":
		return "[interactive card]", nil
	default:
		if text := stringField("text"); text != "" {
			return text, nil
		}
		return "[unsupported message]", nil
	}
}

func ingressJSONInt(raw json.RawMessage) *int {
	if len(raw) == 0 {
		return nil
	}
	var value int
	if json.Unmarshal(raw, &value) == nil {
		return &value
	}
	var decimal float64
	if json.Unmarshal(raw, &decimal) == nil {
		value = int(decimal)
		return &value
	}
	return nil
}

func parseIngressRichPost(raw json.RawMessage) *IngressRichPost {
	root := map[string]json.RawMessage{}
	if len(raw) == 0 || json.Unmarshal(raw, &root) != nil {
		return nil
	}
	if nested, ok := root["post"]; ok {
		var postRoot map[string]json.RawMessage
		if json.Unmarshal(nested, &postRoot) == nil {
			root = postRoot
			raw = nested
		}
	}

	post := &IngressRichPost{Locales: make(map[string]IngressRichDocument)}
	if ingressDocumentObject(root) {
		if document, ok := decodeIngressRichDocument(raw); ok {
			post.Locales[""] = document
		}
		return post
	}
	for locale, documentRaw := range root {
		if document, ok := decodeIngressRichDocument(documentRaw); ok {
			post.Locales[locale] = document
		}
	}
	return post
}

func ingressDocumentObject(value map[string]json.RawMessage) bool {
	for _, key := range []string{"title", "content", "content_v2"} {
		if _, ok := value[key]; ok {
			return true
		}
	}
	return false
}

func decodeIngressRichDocument(raw json.RawMessage) (IngressRichDocument, bool) {
	var wire struct {
		Title     string              `json:"title"`
		Content   [][]json.RawMessage `json:"content"`
		ContentV2 [][]json.RawMessage `json:"content_v2"`
	}
	if json.Unmarshal(raw, &wire) != nil {
		return IngressRichDocument{}, false
	}
	return IngressRichDocument{
		Title:     wire.Title,
		Content:   decodeIngressRichBlocks(wire.Content),
		ContentV2: decodeIngressRichBlocks(wire.ContentV2),
		Raw:       cloneRawMessage(raw),
	}, true
}

func decodeIngressRichBlocks(blocks [][]json.RawMessage) [][]IngressRichElement {
	if len(blocks) == 0 {
		return nil
	}
	out := make([][]IngressRichElement, 0, len(blocks))
	for _, block := range blocks {
		line := make([]IngressRichElement, 0, len(block))
		for _, raw := range block {
			var wire struct {
				Tag       string `json:"tag"`
				Text      string `json:"text"`
				Href      string `json:"href"`
				UserID    string `json:"user_id"`
				UserName  string `json:"user_name"`
				ImageKey  string `json:"image_key"`
				FileKey   string `json:"file_key"`
				FileName  string `json:"file_name"`
				Language  string `json:"language"`
				EmojiType string `json:"emoji_type"`
			}
			if json.Unmarshal(raw, &wire) != nil {
				continue
			}
			line = append(line, IngressRichElement{
				Tag:       wire.Tag,
				Text:      wire.Text,
				Href:      wire.Href,
				UserID:    wire.UserID,
				UserName:  wire.UserName,
				ImageKey:  wire.ImageKey,
				FileKey:   wire.FileKey,
				FileName:  wire.FileName,
				Language:  wire.Language,
				EmojiType: wire.EmojiType,
				Raw:       cloneRawMessage(raw),
			})
		}
		out = append(out, line)
	}
	return out
}

func selectIngressLocale(post *IngressRichPost, languagePriority []string) (string, bool) {
	if post == nil || len(post.Locales) == 0 {
		return "", false
	}
	keys := make([]string, 0, len(post.Locales))
	for key := range post.Locales {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, preferred := range languagePriority {
		if _, ok := post.Locales[preferred]; ok {
			return preferred, true
		}
		for _, key := range keys {
			if strings.EqualFold(key, preferred) {
				return key, true
			}
		}
		canonical := canonicalIngressLocale(preferred)
		for _, key := range keys {
			if canonicalIngressLocale(key) == canonical {
				return key, true
			}
		}
	}
	return keys[0], true
}

func canonicalIngressLocale(locale string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(locale), "_", "-"))
}

func ingressDocumentBlocks(document IngressRichDocument) [][]IngressRichElement {
	if len(document.ContentV2) > 0 {
		return document.ContentV2
	}
	return document.Content
}

func ingressDocumentPlainText(document IngressRichDocument) string {
	lines := make([]string, 0, len(ingressDocumentBlocks(document))+1)
	if title := strings.TrimSpace(document.Title); title != "" {
		lines = append(lines, title)
	}
	for _, block := range ingressDocumentBlocks(document) {
		var line strings.Builder
		for _, element := range block {
			switch element.Tag {
			case "text", "md", "code_block":
				line.WriteString(element.Text)
			case "a":
				if element.Text != "" {
					line.WriteString(element.Text)
				} else {
					line.WriteString(element.Href)
				}
			case "at":
				line.WriteByte('@')
				if element.UserName != "" {
					line.WriteString(element.UserName)
				} else {
					line.WriteString(element.UserID)
				}
			case "emotion":
				line.WriteString(element.EmojiType)
			case "hr":
				line.WriteString("---")
			default:
				line.WriteString(element.Text)
			}
		}
		lines = append(lines, line.String())
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func ingressDocumentResources(document IngressRichDocument) []IngressResource {
	var resources []IngressResource
	for _, block := range ingressDocumentBlocks(document) {
		for _, element := range block {
			switch element.Tag {
			case "img":
				if element.ImageKey != "" {
					resources = append(resources, IngressResource{Type: "image", FileKey: element.ImageKey})
				}
			case "media", "file":
				if element.FileKey != "" {
					resources = append(resources, IngressResource{
						Type:          element.Tag,
						FileKey:       element.FileKey,
						FileName:      element.FileName,
						CoverImageKey: element.ImageKey,
					})
				}
			case "md":
				for _, match := range ingressMarkdownImageKeyRE.FindAllStringSubmatch(element.Text, -1) {
					if len(match) > 1 && match[1] != "" {
						resources = append(resources, IngressResource{Type: "image", FileKey: match[1]})
					}
				}
			}
		}
	}
	return resources
}

func parseIngressMillis(value string) time.Time {
	millis, err := strconv.ParseInt(value, 10, 64)
	if err != nil || millis <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(millis)
}

func ingressStringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func cloneRawMessage(raw []byte) json.RawMessage {
	return append(json.RawMessage(nil), raw...)
}

func ingressRawContent(raw string) json.RawMessage {
	if json.Valid([]byte(raw)) {
		return cloneRawMessage([]byte(raw))
	}
	encoded, _ := json.Marshal(raw)
	return encoded
}
