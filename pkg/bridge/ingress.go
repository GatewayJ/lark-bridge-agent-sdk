package bridge

import (
	"context"
	"encoding/json"
	"time"

	internallark "github.com/GatewayJ/lark-bridge-agent-sdk/internal/adapters/lark"
)

var (
	ErrIngressTransport          = internallark.ErrIngressTransport
	ErrIngressHandler            = internallark.ErrIngressHandler
	ErrIngressHandlerUnavailable = internallark.ErrIngressHandlerUnavailable
	ErrIngressAlreadyStarted     = internallark.ErrIngressAlreadyStarted
	ErrIngressDispatcher         = internallark.ErrIngressDispatcher
	ErrIngressHandlerRegistered  = internallark.ErrIngressHandlerRegistered
)

// IngressHandler must return only after the host has durably claimed or
// persisted the event. A nil result acknowledges intake, not agent execution.
type IngressHandler func(context.Context, IngressEvent) error

type IngressTransport interface {
	Connect(context.Context, IngressHandler) error
	Disconnect(context.Context) error
}

type IdentitySource interface {
	Identity(context.Context) (Identity, error)
}

type Identity struct {
	AppID          string
	OpenID         string
	UserID         string
	UnionID        string
	Name           string
	ActivateStatus int
	Raw            json.RawMessage
}

type IngressEventKind string

const (
	IngressEventMessage    IngressEventKind = "message"
	IngressEventComment    IngressEventKind = "comment"
	IngressEventCardAction IngressEventKind = "card_action"
)

type IngressEvent struct {
	Kind       IngressEventKind
	EventID    string
	EventType  string
	AppID      string
	TenantKey  string
	CreateTime time.Time
	Message    *Message
	Comment    *IngressComment
	CardAction *IngressCardAction
	Raw        json.RawMessage
}

// Envelope is retained as an alias for message-only integrations.
type Envelope = IngressEvent

type Message struct {
	MessageID  string
	RootID     string
	ParentID   string
	ThreadID   string
	ChatID     string
	ChatType   string
	CreateTime time.Time
	Sender     Sender
	Mentions   []Mention
	Content    MessageContent
}

type IngressComment struct {
	CommentID    string
	ReplyID      string
	FileToken    string
	FileType     string
	NoticeType   string
	Operator     Sender
	MentionedBot bool
	CreateTime   time.Time
}

type IngressCardAction struct {
	Token        string
	Host         string
	DeliveryType string
	MessageID    string
	ChatID       string
	Operator     Sender
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

type Sender struct {
	OpenID    string
	UserID    string
	UnionID   string
	Type      string
	TenantKey string
}

type Mention struct {
	Key       string
	OpenID    string
	UserID    string
	UnionID   string
	Name      string
	Type      string
	TenantKey string
}

type MessageContent struct {
	Type      string
	PlainText string
	Raw       json.RawMessage
	Post      *RichPost
	Resources []Resource
}

type RichPost struct {
	Locales map[string]RichDocument
}

type RichDocument struct {
	Title     string
	Content   [][]RichElement
	ContentV2 [][]RichElement
	Raw       json.RawMessage
}

type RichElement struct {
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

type Resource struct {
	Type          string
	FileKey       string
	FileName      string
	DurationMs    *int
	CoverImageKey string
}

type OAPIIngressTransport struct {
	inner *internallark.OAPIIngressTransport
}

var _ IngressTransport = (*OAPIIngressTransport)(nil)
var _ IdentitySource = (*OAPIIngressTransport)(nil)

func NewOAPIIngressTransport(options OAPILarkTransportOptions) (*OAPIIngressTransport, error) {
	transport, err := internallark.NewOAPIIngressTransport(toInternalOAPITransportOptions(options))
	if err != nil {
		return nil, err
	}
	return &OAPIIngressTransport{inner: transport}, nil
}

func (t *OAPIIngressTransport) Connect(ctx context.Context, handler IngressHandler) error {
	if t == nil || t.inner == nil {
		return ErrIngressTransport
	}
	if handler == nil {
		return ErrIngressHandler
	}
	return t.inner.Connect(ctx, func(ctx context.Context, event internallark.IngressEvent) error {
		return handler(ctx, fromInternalIngressEvent(event))
	})
}

func (t *OAPIIngressTransport) Disconnect(ctx context.Context) error {
	if t == nil || t.inner == nil {
		return nil
	}
	return t.inner.Disconnect(ctx)
}

func (t *OAPIIngressTransport) Identity(ctx context.Context) (Identity, error) {
	if t == nil || t.inner == nil {
		return Identity{}, ErrIngressTransport
	}
	identity, err := t.inner.Identity(ctx)
	if err != nil {
		return Identity{}, fromInternalLarkError(err)
	}
	return Identity{
		AppID:          identity.AppID,
		OpenID:         identity.OpenID,
		UserID:         identity.UserID,
		UnionID:        identity.UnionID,
		Name:           identity.Name,
		ActivateStatus: identity.ActivateStatus,
		Raw:            append(json.RawMessage(nil), identity.Raw...),
	}, nil
}

func fromInternalIngressEvent(in internallark.IngressEvent) IngressEvent {
	out := IngressEvent{
		Kind:       IngressEventKind(in.Kind),
		EventID:    in.EventID,
		EventType:  in.EventType,
		AppID:      in.AppID,
		TenantKey:  in.TenantKey,
		CreateTime: in.CreateTime,
		Raw:        append(json.RawMessage(nil), in.Raw...),
	}
	if in.Message != nil {
		message := fromInternalIngressMessage(*in.Message)
		out.Message = &message
	}
	if in.Comment != nil {
		out.Comment = &IngressComment{
			CommentID:    in.Comment.CommentID,
			ReplyID:      in.Comment.ReplyID,
			FileToken:    in.Comment.FileToken,
			FileType:     in.Comment.FileType,
			NoticeType:   in.Comment.NoticeType,
			Operator:     Sender(in.Comment.Operator),
			MentionedBot: in.Comment.MentionedBot,
			CreateTime:   in.Comment.CreateTime,
		}
	}
	if in.CardAction != nil {
		out.CardAction = &IngressCardAction{
			Token:        in.CardAction.Token,
			Host:         in.CardAction.Host,
			DeliveryType: in.CardAction.DeliveryType,
			MessageID:    in.CardAction.MessageID,
			ChatID:       in.CardAction.ChatID,
			Operator:     Sender(in.CardAction.Operator),
			Action: IngressCardActionPayload{
				Value:      cloneIngressAnyMap(in.CardAction.Action.Value),
				Tag:        in.CardAction.Action.Tag,
				Option:     in.CardAction.Action.Option,
				Timezone:   in.CardAction.Action.Timezone,
				Name:       in.CardAction.Action.Name,
				FormValue:  cloneIngressAnyMap(in.CardAction.Action.FormValue),
				InputValue: in.CardAction.Action.InputValue,
				Options:    append([]string(nil), in.CardAction.Action.Options...),
				Checked:    in.CardAction.Action.Checked,
			},
			Context: IngressCardActionContext(in.CardAction.Context),
		}
	}
	return out
}

func fromInternalIngressEnvelope(in internallark.IngressEnvelope) Envelope {
	return fromInternalIngressEvent(in)
}

func cloneIngressAnyMap(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func fromInternalIngressMessage(in internallark.IngressMessage) Message {
	mentions := make([]Mention, 0, len(in.Mentions))
	for _, mention := range in.Mentions {
		mentions = append(mentions, Mention(mention))
	}
	return Message{
		MessageID:  in.MessageID,
		RootID:     in.RootID,
		ParentID:   in.ParentID,
		ThreadID:   in.ThreadID,
		ChatID:     in.ChatID,
		ChatType:   in.ChatType,
		CreateTime: in.CreateTime,
		Sender:     Sender(in.Sender),
		Mentions:   mentions,
		Content:    fromInternalIngressMessageContent(in.Content),
	}
}

func fromInternalIngressMessageContent(in internallark.IngressMessageContent) MessageContent {
	resources := make([]Resource, 0, len(in.Resources))
	for _, resource := range in.Resources {
		resources = append(resources, Resource(resource))
	}
	out := MessageContent{
		Type:      in.Type,
		PlainText: in.PlainText,
		Raw:       append(json.RawMessage(nil), in.Raw...),
		Resources: resources,
	}
	if in.Post != nil {
		out.Post = fromInternalIngressRichPost(*in.Post)
	}
	return out
}

func fromInternalIngressRichPost(in internallark.IngressRichPost) *RichPost {
	post := &RichPost{Locales: make(map[string]RichDocument, len(in.Locales))}
	for locale, document := range in.Locales {
		post.Locales[locale] = fromInternalIngressRichDocument(document)
	}
	return post
}

func fromInternalIngressRichDocument(in internallark.IngressRichDocument) RichDocument {
	return RichDocument{
		Title:     in.Title,
		Content:   fromInternalIngressRichBlocks(in.Content),
		ContentV2: fromInternalIngressRichBlocks(in.ContentV2),
		Raw:       append(json.RawMessage(nil), in.Raw...),
	}
}

func fromInternalIngressRichBlocks(in [][]internallark.IngressRichElement) [][]RichElement {
	if len(in) == 0 {
		return nil
	}
	out := make([][]RichElement, 0, len(in))
	for _, block := range in {
		mapped := make([]RichElement, 0, len(block))
		for _, element := range block {
			mapped = append(mapped, RichElement{
				Tag:       element.Tag,
				Text:      element.Text,
				Href:      element.Href,
				UserID:    element.UserID,
				UserName:  element.UserName,
				ImageKey:  element.ImageKey,
				FileKey:   element.FileKey,
				FileName:  element.FileName,
				Language:  element.Language,
				EmojiType: element.EmojiType,
				Raw:       append(json.RawMessage(nil), element.Raw...),
			})
		}
		out = append(out, mapped)
	}
	return out
}
