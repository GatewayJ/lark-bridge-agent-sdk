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
type IngressHandler func(context.Context, Envelope) error

type IngressTransport interface {
	Connect(context.Context, IngressHandler) error
	Disconnect(context.Context) error
}

type Envelope struct {
	EventID    string
	EventType  string
	AppID      string
	TenantKey  string
	CreateTime time.Time
	Message    *Message
	Raw        json.RawMessage
}

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
	return t.inner.Connect(ctx, func(ctx context.Context, envelope internallark.IngressEnvelope) error {
		return handler(ctx, fromInternalIngressEnvelope(envelope))
	})
}

func (t *OAPIIngressTransport) Disconnect(ctx context.Context) error {
	if t == nil || t.inner == nil {
		return nil
	}
	return t.inner.Disconnect(ctx)
}

func fromInternalIngressEnvelope(in internallark.IngressEnvelope) Envelope {
	out := Envelope{
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
