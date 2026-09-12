// Package agentoutput preserves message boundaries from CLI event streams.
package agentoutput

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/GatewayJ/lark-bridge-agent-sdk/internal/ports/agent"
)

const actionStart = "<bridge_user_action>"
const actionEnd = "</bridge_user_action>"

// UserAction decodes only a complete, explicitly marked assistant message.
// Ordinary prose, quoted examples, and tool output are not classified by words.
func UserAction(text string) (agent.AgentEvent, bool) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, actionStart) || !strings.HasSuffix(text, actionEnd) {
		return agent.AgentEvent{}, false
	}
	var request struct {
		ID      string `json:"id"`
		Kind    string `json:"kind"`
		Message string `json:"message"`
	}
	body := strings.TrimSuffix(strings.TrimPrefix(text, actionStart), actionEnd)
	if json.Unmarshal([]byte(body), &request) != nil || strings.TrimSpace(request.Message) == "" {
		return agent.AgentEvent{}, false
	}
	switch request.Kind {
	case "authorization", "confirmation", "question":
	default:
		return agent.AgentEvent{}, false
	}
	if request.ID == "" {
		request.ID = actionID(request.Message)
	}
	return agent.AgentEvent{Type: agent.EventUserAction, ID: &request.ID, Name: &request.Kind, Delta: &request.Message}, true
}

// LarkAuthorization recognizes the structured result of the two-stage login
// command. This makes the URL visible even if the agent omits the action marker.
func LarkAuthorization(command, output string) (agent.AgentEvent, bool) {
	fields := loginCommandFields(command)
	if len(fields) < 5 || filepath.Base(fields[0]) != "lark-cli" || fields[1] != "auth" || fields[2] != "login" {
		return agent.AgentEvent{}, false
	}
	flags := map[string]bool{}
	for _, field := range fields[3:] {
		flags[field] = true
	}
	if !flags["--no-wait"] || !flags["--json"] {
		return agent.AgentEvent{}, false
	}
	var result struct {
		URL  string `json:"verification_url"`
		Data struct {
			URL string `json:"verification_url"`
		} `json:"data"`
	}
	if json.Unmarshal([]byte(output), &result) != nil {
		return agent.AgentEvent{}, false
	}
	if result.URL == "" {
		result.URL = result.Data.URL
	}
	u, err := url.Parse(result.URL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return agent.AgentEvent{}, false
	}
	kind, id := "authorization", actionID(result.URL)
	message := "需要飞书授权，请打开以下链接完成授权，完成后我会继续处理。\n\n" + result.URL
	return agent.AgentEvent{Type: agent.EventUserAction, ID: &id, Name: &kind, Delta: &message, Input: map[string]any{"url": result.URL}}, true
}

// Codex may report a shell-wrapped command. Accept only a literal single
// command; shell expansion and compound scripts are left to the explicit marker.
func loginCommandFields(command string) []string {
	if strings.ContainsAny(command, "\n\r;&|><$`()") {
		return nil
	}
	fields := strings.Fields(command)
	if len(fields) < 3 {
		return nil
	}
	switch filepath.Base(fields[0]) {
	case "sh", "bash", "zsh", "dash":
		if fields[1] != "-c" && fields[1] != "-lc" {
			return nil
		}
		argument := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(command), fields[0]))
		argument = strings.TrimSpace(strings.TrimPrefix(argument, fields[1]))
		if len(argument) < 2 {
			return nil
		}
		if argument[0] == '\'' && argument[len(argument)-1] == '\'' && !strings.Contains(argument[1:len(argument)-1], "'") {
			return strings.Fields(argument[1 : len(argument)-1])
		}
		if argument[0] == '"' {
			literal, err := strconv.Unquote(argument)
			if err == nil {
				return strings.Fields(literal)
			}
		}
		return nil
	default:
		return fields
	}
}

func actionID(text string) string {
	sum := sha256.Sum256([]byte(text))
	return fmt.Sprintf("action-%x", sum[:12])
}

// Stream holds at most one complete candidate message, not a growing transcript.
// A following message or tool proves an unclassified candidate was commentary.
// An authoritative CLI result wins over that candidate on normal completion.
type Stream struct {
	pending           *agent.AgentEvent
	usage             []agent.AgentEvent
	seenActions       map[string]bool
	authorizationURLs []string
	messageIndex      int
}

func (s *Stream) Push(event agent.AgentEvent) []agent.AgentEvent {
	switch event.Type {
	case agent.EventText:
		if event.Delta == nil {
			return nil
		}
		if action, ok := UserAction(*event.Delta); ok {
			return s.Push(action)
		}
		if *event.Delta == "" && event.Phase != agent.TextFinalAnswer {
			return nil
		}
		if value(event.ID) == "" {
			s.messageIndex++
			id := fmt.Sprintf("message-%d", s.messageIndex)
			event.ID = &id
		}
		if event.Phase == agent.TextFinalAnswer && s.pending != nil && strings.TrimSpace(value(s.pending.Delta)) == strings.TrimSpace(*event.Delta) {
			s.pending = &event
			return nil
		}
		out := s.flushProgress()
		if event.Phase != "" && event.Phase != agent.TextFinalAnswer {
			event.Phase = agent.TextCommentary
			return append(out, event)
		}
		s.pending = &event
		return out
	case agent.EventUserAction:
		out := s.flushProgress()
		if s.seenActions == nil {
			s.seenActions = map[string]bool{}
		}
		key := value(event.ID)
		if key == "" {
			key = actionID(value(event.Delta))
		}
		if value(event.Name) == "authorization" {
			if input, ok := event.Input.(map[string]any); ok {
				if link, ok := input["url"].(string); ok && link != "" {
					s.authorizationURLs = append(s.authorizationURLs, link)
				}
			}
			for _, link := range s.authorizationURLs {
				if strings.Contains(value(event.Delta), link) {
					key = actionID(link)
					break
				}
			}
		}
		if !s.seenActions[key] {
			s.seenActions[key] = true
			out = append(out, event)
		}
		return out
	case agent.EventToolUse, agent.EventThinking:
		return append(s.flushProgress(), event)
	case agent.EventUsage:
		s.usage = append(s.usage, event)
		return nil
	default:
		return []agent.AgentEvent{event}
	}
}

func (s *Stream) Finish(finalText *string, success bool) []agent.AgentEvent {
	var out []agent.AgentEvent
	if !success {
		out = s.flushProgress()
	} else if finalText != nil {
		if s.pending != nil && strings.TrimSpace(value(s.pending.Delta)) != strings.TrimSpace(*finalText) {
			out = s.flushProgress()
		}
		s.pending = nil
		if action, ok := UserAction(*finalText); ok {
			out = append(out, s.Push(action)...)
		} else if strings.TrimSpace(*finalText) != "" {
			out = append(out, agent.AgentEvent{Type: agent.EventText, Phase: agent.TextFinalAnswer, Delta: finalText})
		}
	} else if s.pending != nil {
		final := *s.pending
		final.Phase = agent.TextFinalAnswer
		if strings.TrimSpace(value(final.Delta)) != "" {
			out = append(out, final)
		}
		s.pending = nil
	}
	out = append(out, s.usage...)
	s.usage = nil
	return out
}

func (s *Stream) flushProgress() []agent.AgentEvent {
	if s.pending == nil {
		return nil
	}
	progress := *s.pending
	progress.Phase = agent.TextCommentary
	s.pending = nil
	return []agent.AgentEvent{progress}
}

func value(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
