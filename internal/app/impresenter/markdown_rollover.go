package impresenter

import (
	"context"
	"strings"

	"github.com/GatewayJ/lark-bridge-agent-sdk/internal/app/cardrender"
)

const markdownSettleAttempts = 2

// markdownStreamState keeps each Feishu message as a non-overlapping segment
// of the run. Feishu caps edits per message, so a long run must continue in a
// fresh message without replaying content that is already visible.
type markdownStreamState struct {
	messageID         string
	segmentBase       cardrender.RunState
	published         cardrender.RunState
	successfulUpdates int
}

func newMarkdownStreamState() *markdownStreamState {
	return &markdownStreamState{}
}

func (s *markdownStreamState) start(ctx context.Context, input Input, state cardrender.RunState) error {
	if s == nil {
		return nil
	}
	body := renderRunMarkdown(input, state)
	if strings.TrimSpace(body) == "" {
		return nil
	}
	result, err := sendMarkdownBody(ctx, input, body)
	if err != nil {
		return err
	}
	s.messageID = result.MessageID
	s.published = state
	s.successfulUpdates = 0
	return nil
}

func (s *markdownStreamState) flush(ctx context.Context, input Input, state cardrender.RunState) error {
	if s == nil || s.messageID == "" {
		return s.start(ctx, input, state)
	}
	if s.successfulUpdates >= maxMessageUpdates(input.MaxMessageUpdates) {
		if s.hasUnpublishedContent(input, state) {
			return s.rollover(ctx, input, state)
		}
		// Preserve the reserved close updates while only status/footer state is
		// changing. The terminal path will use that headroom to remove the footer.
		return nil
	}
	return s.update(ctx, input, state)
}

func (s *markdownStreamState) finish(ctx context.Context, input Input, state cardrender.RunState) error {
	if s == nil || s.messageID == "" {
		_, err := sendMarkdown(ctx, input, state)
		return err
	}
	if s.successfulUpdates >= maxMessageUpdates(input.MaxMessageUpdates) && s.hasUnpublishedContent(input, state) {
		return s.rollover(ctx, input, state)
	}
	return s.update(ctx, input, state)
}

func (s *markdownStreamState) update(ctx context.Context, input Input, state cardrender.RunState) error {
	updater, ok := input.Channel.(MessageUpdater)
	if !ok || s.messageID == "" {
		return nil
	}
	body := s.render(input, state)
	if strings.TrimSpace(body) == "" {
		if isActive(state) {
			return nil
		}
		body = "_（未返回内容）_"
	}
	if err := updater.UpdateMessage(ctx, UpdateMessageRequest{
		MessageID: s.messageID,
		Content:   MessageContent{Markdown: body},
	}); err != nil {
		return err
	}
	s.published = state
	s.successfulUpdates++
	return nil
}

func (s *markdownStreamState) rollover(ctx context.Context, input Input, state cardrender.RunState) error {
	base := s.published
	body := renderRunMarkdown(input, markdownStateDelta(base, state))
	if strings.TrimSpace(body) == "" {
		return s.update(ctx, input, state)
	}
	if isActive(state) && len(body) > defaultMaxLiveMarkdown {
		// The Lark OAPI channel auto-splits long markdown sends and returns only
		// the first message ID. Sending an active footer that way would leave the
		// final chunk stuck on "正在输出" because only the first chunk can be
		// patched. Wait for the terminal state, then all chunks are final at send.
		return nil
	}

	result, err := sendMarkdownBody(ctx, input, body)
	if err != nil {
		return err
	}

	previousMessageID := s.messageID
	previousSegment := markdownStateDelta(s.segmentBase, s.published)
	s.messageID = result.MessageID
	s.segmentBase = base
	s.published = state
	s.successfulUpdates = 0

	// The continuation is already visible. Closing the prior message is
	// best-effort so a transient patch failure cannot suppress new output.
	settleMarkdownMessage(ctx, input, previousMessageID, previousSegment)
	return nil
}

func (s *markdownStreamState) sendFallback(ctx context.Context, input Input, state cardrender.RunState) error {
	if s == nil {
		_, err := sendMarkdown(ctx, input, state)
		return err
	}
	missing := markdownStateDelta(s.published, state)
	body := renderRunMarkdown(input, missing)
	if strings.TrimSpace(body) == "" {
		// Nothing was lost; only the terminal patch failed. Retry settling the
		// visible segment before giving up, without posting duplicate content.
		settleMarkdownMessage(ctx, input, s.messageID, markdownStateDelta(s.segmentBase, state))
		return nil
	}
	settleMarkdownMessage(ctx, input, s.messageID, markdownStateDelta(s.segmentBase, s.published))
	_, err := sendMarkdownBody(ctx, input, body)
	return err
}

func (s *markdownStreamState) render(input Input, state cardrender.RunState) string {
	return renderRunMarkdown(input, markdownStateDelta(s.segmentBase, state))
}

func (s *markdownStreamState) hasUnpublishedContent(input Input, state cardrender.RunState) bool {
	delta := markdownStateDelta(s.published, state)
	return strings.TrimSpace(renderSettledRunMarkdown(input, delta)) != ""
}

func markdownStateDelta(base cardrender.RunState, current cardrender.RunState) cardrender.RunState {
	return cardStateDelta(base, current)
}

func settleMarkdownMessage(ctx context.Context, input Input, messageID string, state cardrender.RunState) {
	updater, ok := input.Channel.(MessageUpdater)
	if !ok || messageID == "" {
		return
	}
	body := renderSettledRunMarkdown(input, state)
	if strings.TrimSpace(body) == "" {
		return
	}
	for attempt := 0; attempt < markdownSettleAttempts; attempt++ {
		if updater.UpdateMessage(ctx, UpdateMessageRequest{
			MessageID: messageID,
			Content:   MessageContent{Markdown: body},
		}) == nil {
			return
		}
	}
}

func sendMarkdownBody(ctx context.Context, input Input, body string) (SendMessageResult, error) {
	return input.Channel.SendMessage(ctx, SendMessageRequest{
		ChatID:  input.ChatID,
		Content: MessageContent{Markdown: body},
		Options: input.Options,
	})
}
