package impresenter

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/GatewayJ/lark-bridge-agent-sdk/internal/app/cardkit"
	"github.com/GatewayJ/lark-bridge-agent-sdk/internal/app/cardrender"
	agentport "github.com/GatewayJ/lark-bridge-agent-sdk/internal/ports/agent"
)

type actionDelivery struct {
	delivered map[string]bool
	failures  map[string]error
}

func (d *actionDelivery) send(ctx context.Context, input Input, event agentport.AgentEvent) {
	if input.Channel == nil || event.Delta == nil || strings.TrimSpace(*event.Delta) == "" {
		return
	}
	body := *event.Delta
	key := body
	if event.ID != nil && *event.ID != "" {
		key = *event.ID
	}
	if d.delivered == nil {
		d.delivered = map[string]bool{}
		d.failures = map[string]error{}
	}
	if d.delivered[key] {
		return
	}
	if event.Name != nil && *event.Name == "authorization" && !input.PrivateChat {
		body = "用户身份授权需要在私聊中完成，请私信我后继续。"
	}
	_, err := input.Channel.SendMessage(ctx, SendMessageRequest{ChatID: input.ChatID, Options: input.Options, Content: MessageContent{Markdown: body}})
	if err != nil {
		// A prompt must remain visible outside the CoT drawer. Try an ordinary
		// card if message delivery fails; neither path finishes the agent run.
		card := cardkit.RenderCardView(cardrender.CardView{Summary: "需要用户操作", Elements: []cardrender.CardElement{{Kind: cardrender.ElementMarkdown, Text: body}}})
		_, err = input.Channel.SendCard(ctx, SendCardRequest{ChatID: input.ChatID, Options: input.Options, Card: card})
	}
	if err == nil {
		d.delivered[key] = true
		delete(d.failures, key)
		return
	}
	d.failures[key] = fmt.Errorf("deliver user action: %w", err)
	if input.OnUserActionError != nil {
		input.OnUserActionError(ctx, d.failures[key])
	}
}

func (d *actionDelivery) err() error {
	var failures []error
	for _, err := range d.failures {
		failures = append(failures, err)
	}
	return errors.Join(failures...)
}
