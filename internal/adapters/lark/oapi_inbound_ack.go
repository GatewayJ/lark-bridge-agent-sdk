package lark

import (
	"context"
	"sync"
	"time"

	larknormalize "github.com/larksuite/oapi-sdk-go/v3/channel/normalize"
	"github.com/larksuite/oapi-sdk-go/v3/channel/pipeline"
	"github.com/larksuite/oapi-sdk-go/v3/channel/safety"
	channeltypes "github.com/larksuite/oapi-sdk-go/v3/channel/types"
	larkevent "github.com/larksuite/oapi-sdk-go/v3/event"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
)

// oapiAckAfterIntakeChannel bypasses the facade's asynchronous inbound
// message pipeline. The embedded channel remains responsible for outbound
// operations and all other event types.
type oapiAckAfterIntakeChannel struct {
	oapiChannel

	wsClient        *larkws.Client
	pipelineManager *pipeline.ChatPipelineManager
	policyGate      *safety.PolicyGate
	staleWindow     time.Duration

	mu                sync.RWMutex
	messageHandlers   []func(context.Context, *channeltypes.NormalizedMessage) error
	commentHandlers   []func(context.Context, *channeltypes.CommentEvent) error
	messageRegistered bool
	commentRegistered bool
}

func newOAPIAckAfterIntakeChannel(
	base oapiChannel,
	wsClient *larkws.Client,
	config channeltypes.ChannelConfig,
) oapiChannel {
	return &oapiAckAfterIntakeChannel{
		oapiChannel:     base,
		wsClient:        wsClient,
		pipelineManager: pipeline.NewChatPipelineManager(config.Safety.Batch),
		policyGate:      safety.NewPolicyGate(&config.Policy, nil),
		staleWindow:     config.Safety.StaleMessageWindowMs,
	}
}

func (c *oapiAckAfterIntakeChannel) OnMessage(
	handler func(context.Context, *channeltypes.NormalizedMessage) error,
) {
	c.mu.Lock()
	c.messageHandlers = append(c.messageHandlers, handler)
	if c.messageRegistered || c.wsClient == nil || c.wsClient.EventHandler() == nil {
		c.mu.Unlock()
		return
	}
	c.messageRegistered = true
	dispatcher := c.wsClient.EventHandler()
	c.mu.Unlock()

	dispatcher.OnP2MessageReceiveV1(c.handleMessage)
}

func (c *oapiAckAfterIntakeChannel) OnComment(
	handler func(context.Context, *channeltypes.CommentEvent) error,
) {
	c.mu.Lock()
	c.commentHandlers = append(c.commentHandlers, handler)
	if c.commentRegistered || c.wsClient == nil || c.wsClient.EventHandler() == nil {
		c.mu.Unlock()
		return
	}
	c.commentRegistered = true
	dispatcher := c.wsClient.EventHandler()
	c.mu.Unlock()

	dispatcher.OnCustomizedEvent("drive.notice.comment_add_v1", c.handleComment)
}

func (c *oapiAckAfterIntakeChannel) handleMessage(
	ctx context.Context,
	event *larkim.P2MessageReceiveV1,
) error {
	message := larknormalize.ParseMessage(event)
	if message == nil {
		return nil
	}

	if bot := c.GetBotIdentity(ctx); bot != nil {
		if message.UserID == bot.OpenID {
			return nil
		}
		for i := range message.Mentions {
			mention := &message.Mentions[i]
			if mention.OpenID == bot.OpenID || mention.UserID == bot.OpenID ||
				(bot.UserID != "" && mention.UserID == bot.UserID) {
				message.MentionedBot = true
				mention.IsBot = true
			}
		}
	}

	if safety.IsStale(message.CreateTimeMs, c.staleWindow) {
		return nil
	}
	if decision := c.policyGate.Evaluate(message); !decision.Allowed {
		return nil
	}

	// Do not use the facade's eager in-memory deduplication here. Durable
	// consumers must make retries idempotent, and failed intake attempts must
	// remain eligible for protocol redelivery.
	return c.pipelineManager.Run(ctx, "message:"+message.ChatID, func() error {
		for _, handler := range c.messageHandlerSnapshot() {
			if err := handler(ctx, message); err != nil {
				return err
			}
		}
		return nil
	})
}

func (c *oapiAckAfterIntakeChannel) handleComment(
	ctx context.Context,
	event *larkevent.EventReq,
) error {
	comment := larknormalize.ParseComment(event)
	if comment == nil || comment.CommentID == "" {
		return nil
	}

	return c.pipelineManager.Run(ctx, "comment:"+comment.FileToken, func() error {
		for _, handler := range c.commentHandlerSnapshot() {
			if err := handler(ctx, comment); err != nil {
				return err
			}
		}
		return nil
	})
}

func (c *oapiAckAfterIntakeChannel) messageHandlerSnapshot() []func(
	context.Context,
	*channeltypes.NormalizedMessage,
) error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]func(context.Context, *channeltypes.NormalizedMessage) error(nil), c.messageHandlers...)
}

func (c *oapiAckAfterIntakeChannel) commentHandlerSnapshot() []func(
	context.Context,
	*channeltypes.CommentEvent,
) error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]func(context.Context, *channeltypes.CommentEvent) error(nil), c.commentHandlers...)
}
