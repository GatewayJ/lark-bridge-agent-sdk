package impresenter

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"

	"github.com/GatewayJ/lark-bridge-agent-sdk/internal/app/cardrender"
)

const (
	continuedCardNote    = "_↪️ 内容将在下一张卡片继续_"
	continuationCardNote = "_↩️ 接上张卡片_"
)

type cardStreamState struct {
	messageID         string
	state             cardrender.RunState
	published         cardrender.RunState
	successfulUpdates int
}

func newCardStreamState(state cardrender.RunState) cardStreamState {
	return cardStreamState{state: state, published: state}
}

func (s *cardStreamState) reduce(event cardrender.Event) {
	s.state = cardrender.Reduce(s.state, event)
}

func (s *cardStreamState) markSent(messageID string) {
	s.messageID = messageID
	s.published = s.state
	s.successfulUpdates = 0
}

func (s *cardStreamState) flush(ctx context.Context, input Input) error {
	if s == nil || s.messageID == "" {
		return nil
	}
	rolledOver := false
	for s.shouldRollover(input) {
		if err := s.rollover(ctx, input); err != nil {
			return err
		}
		rolledOver = true
	}
	if rolledOver {
		return nil
	}
	if err := updateRunCard(ctx, input, s.state, s.messageID); err != nil {
		return err
	}
	s.published = s.state
	s.successfulUpdates++
	return nil
}

func (s *cardStreamState) shouldRollover(input Input) bool {
	policy := input.CardRollover
	if policy.MaxBytes <= 0 && policy.MaxUpdates <= 0 {
		return false
	}
	pending := cardStateDelta(s.published, s.state)
	if !hasCardContent(renderRunState(input, pending)) {
		return false
	}
	if policy.MaxUpdates > 0 && s.successfulUpdates >= policy.MaxUpdates {
		return true
	}
	if policy.MaxBytes <= 0 {
		return false
	}
	encoded, err := json.Marshal(renderContinuationCard(input, s.state))
	return err == nil && len(encoded) > policy.MaxBytes
}

func (s *cardStreamState) rollover(ctx context.Context, input Input) error {
	pending := cardStateDelta(s.published, s.state)
	nextState := cardStatePrefix(input, pending, input.CardRollover.MaxBytes)
	card := renderContinuationCard(input, nextState)
	result, err := sendCardWithCard(ctx, input, card)
	if err != nil {
		return err
	}

	previousMessageID := s.messageID
	previousState := s.published
	s.messageID = result.MessageID
	s.state = pending
	s.published = nextState
	s.successfulUpdates = 0

	// The channel has accepted the continuation before the old card is closed.
	// A close failure therefore cannot hide the newly produced content.
	if updater, ok := input.Channel.(CardUpdater); ok && previousMessageID != "" {
		_ = updater.UpdateCard(ctx, UpdateCardRequest{
			MessageID: previousMessageID,
			Card:      renderContinuedCard(input, previousState),
		})
	}
	return nil
}

func (s *cardStreamState) prepareFinal(ctx context.Context, input Input, final cardrender.RunState) (cardrender.RunState, error) {
	state := s.applyFinalState(final)
	for s.shouldRollover(input) {
		if err := s.rollover(ctx, input); err != nil {
			return state, err
		}
		state = s.applyFinalState(final)
	}
	return state, nil
}

func shouldSplitFinalCard(input Input, state cardrender.RunState) bool {
	if input.CardRollover.MaxBytes <= 0 {
		return false
	}
	encoded, err := json.Marshal(renderRunCard(input, state))
	return err == nil && len(encoded) > input.CardRollover.MaxBytes
}

func sendSplitFinalCards(ctx context.Context, input Input, state cardrender.RunState) error {
	remaining := state
	segment := 0
	for {
		prefix := cardStatePrefix(input, remaining, input.CardRollover.MaxBytes)
		card := renderRunCard(input, prefix)
		if segment > 0 {
			prependCardNote(card, continuationCardNote)
		}
		if _, err := sendCardWithCard(ctx, input, card); err != nil {
			return err
		}
		segment++

		remaining = cardStateDelta(prefix, remaining)
		if !hasCardContent(renderRunState(input, remaining)) {
			return nil
		}
	}
}

func (s *cardStreamState) applyFinalState(final cardrender.RunState) cardrender.RunState {
	if s == nil || s.messageID == "" {
		return final
	}
	state := s.state
	state.RunID = final.RunID
	state.Scope = final.Scope
	state.CWD = final.CWD
	state.SessionID = final.SessionID
	state.ThreadID = final.ThreadID
	state.Model = final.Model
	state.Status = final.Status
	state.Footer = final.Footer
	state.Error = final.Error
	state.Usage = final.Usage
	state.LastEvent = final.LastEvent
	state.StartedAt = final.StartedAt
	state.UpdatedAt = final.UpdatedAt
	state.Elapsed = final.Elapsed
	state.TimeoutMinutes = final.TimeoutMinutes
	state.Reasoning.Active = false
	for index := range state.Blocks {
		if state.Blocks[index].Kind == cardrender.BlockText {
			state.Blocks[index].Streaming = false
		}
	}
	s.state = state
	return state
}

func cardStateDelta(published cardrender.RunState, current cardrender.RunState) cardrender.RunState {
	delta := current
	delta.Blocks = nil
	delta.Reasoning.Content = stringDelta(published.Reasoning.Content, current.Reasoning.Content)

	for index, block := range current.Blocks {
		if index >= len(published.Blocks) {
			delta.Blocks = append(delta.Blocks, cloneCardBlock(block))
			continue
		}
		previous := published.Blocks[index]
		if block.Kind == cardrender.BlockText && previous.Kind == cardrender.BlockText {
			content := stringDelta(previous.Content, block.Content)
			if content != "" {
				block.Content = content
				delta.Blocks = append(delta.Blocks, cloneCardBlock(block))
			}
			continue
		}
		if !reflect.DeepEqual(previous, block) {
			delta.Blocks = append(delta.Blocks, cloneCardBlock(block))
		}
	}
	return delta
}

func stringDelta(previous string, current string) string {
	if strings.HasPrefix(current, previous) {
		return strings.TrimPrefix(current, previous)
	}
	if current == previous {
		return ""
	}
	return current
}

func cloneCardBlock(block cardrender.Block) cardrender.Block {
	if block.Tool != nil {
		tool := *block.Tool
		block.Tool = &tool
	}
	return block
}

func hasCardContent(state cardrender.RunState) bool {
	if strings.TrimSpace(state.Reasoning.Content) != "" || strings.TrimSpace(state.Error) != "" {
		return true
	}
	for _, block := range state.Blocks {
		if block.Kind == cardrender.BlockTool && block.Tool != nil {
			return true
		}
		if block.Kind == cardrender.BlockText && strings.TrimSpace(block.Content) != "" {
			return true
		}
	}
	return false
}

func cardStatePrefix(input Input, state cardrender.RunState, maxBytes int) cardrender.RunState {
	if maxBytes <= 0 || serializedCardFits(input, state, maxBytes) {
		return state
	}
	prefix := state
	prefix.Blocks = nil
	prefix.Reasoning.Content = ""

	if state.Reasoning.Content != "" {
		candidate := prefix
		candidate.Reasoning = state.Reasoning
		if serializedCardFits(input, candidate, maxBytes) {
			prefix = candidate
		} else {
			prefix.Reasoning = state.Reasoning
			prefix.Reasoning.Content = fittingStringPrefix(input, prefix, state.Reasoning.Content, maxBytes, true)
			return prefix
		}
	}

	for _, block := range state.Blocks {
		candidate := prefix
		candidate.Blocks = append(cloneCardBlocks(prefix.Blocks), cloneCardBlock(block))
		if serializedCardFits(input, candidate, maxBytes) {
			prefix = candidate
			continue
		}
		if block.Kind == cardrender.BlockText {
			candidate = prefix
			candidate.Blocks = append(cloneCardBlocks(prefix.Blocks), cloneCardBlock(block))
			last := len(candidate.Blocks) - 1
			candidate.Blocks[last].Content = fittingStringPrefix(input, candidate, block.Content, maxBytes, false)
			if candidate.Blocks[last].Content != "" {
				return candidate
			}
		}
		if hasCardContent(prefix) {
			return prefix
		}
		// Tool panels are already bounded by the renderer. Keep an unsplittable
		// block intact even when a caller configures an impractically small cap.
		return candidate
	}
	return prefix
}

func fittingStringPrefix(input Input, template cardrender.RunState, content string, maxBytes int, reasoning bool) string {
	runes := []rune(content)
	low, high, best := 1, len(runes), 0
	for low <= high {
		middle := low + (high-low)/2
		candidate := template
		if reasoning {
			candidate.Reasoning.Content = string(runes[:middle])
		} else {
			candidate.Blocks = cloneCardBlocks(template.Blocks)
			candidate.Blocks[len(candidate.Blocks)-1].Content = string(runes[:middle])
		}
		if serializedCardFits(input, candidate, maxBytes) {
			best = middle
			low = middle + 1
		} else {
			high = middle - 1
		}
	}
	if best == 0 && len(runes) > 0 {
		best = 1
	}
	return string(runes[:best])
}

func serializedCardFits(input Input, state cardrender.RunState, maxBytes int) bool {
	encoded, err := json.Marshal(renderContinuationCard(input, state))
	return err == nil && len(encoded) <= maxBytes
}

func cloneCardBlocks(blocks []cardrender.Block) []cardrender.Block {
	cloned := make([]cardrender.Block, 0, len(blocks))
	for _, block := range blocks {
		cloned = append(cloned, cloneCardBlock(block))
	}
	return cloned
}

func renderContinuedCard(input Input, state cardrender.RunState) map[string]any {
	state.Status = cardrender.StatusSucceeded
	state.Footer = ""
	state.Reasoning.Active = false
	for index := range state.Blocks {
		if state.Blocks[index].Kind == cardrender.BlockText {
			state.Blocks[index].Streaming = false
		}
	}
	card := renderRunCard(input, state)
	appendCardNote(card, continuedCardNote)
	return card
}

func renderContinuationCard(input Input, state cardrender.RunState) map[string]any {
	card := renderRunCard(input, state)
	prependCardNote(card, continuationCardNote)
	return card
}

func prependCardNote(card map[string]any, note string) {
	mutateCardElements(card, func(elements []any) []any {
		return append([]any{cardNote(note)}, elements...)
	})
}

func appendCardNote(card map[string]any, note string) {
	mutateCardElements(card, func(elements []any) []any {
		return append(elements, cardNote(note))
	})
}

func mutateCardElements(card map[string]any, mutate func([]any) []any) {
	body, ok := card["body"].(map[string]any)
	if !ok {
		return
	}
	elements, ok := body["elements"].([]any)
	if !ok {
		return
	}
	body["elements"] = mutate(elements)
}

func cardNote(content string) map[string]any {
	return map[string]any{
		"tag":       "markdown",
		"content":   content,
		"text_size": "notation",
	}
}
