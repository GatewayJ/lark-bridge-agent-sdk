package agentoutput

import (
	"strings"
	"testing"

	"github.com/GatewayJ/lark-bridge-agent-sdk/internal/ports/agent"
)

func TestStreamSeparatesProgressFromAuthoritativeFinal(t *testing.T) {
	s := &Stream{}
	var events []agent.AgentEvent
	for _, event := range []agent.AgentEvent{
		text("Checking the calendar.", ""),
		{Type: agent.EventToolUse},
		{Type: agent.EventToolResult},
		text("Creating the meeting.", ""),
		text("Created.\n\n18:00–18:30\nJoin: https://example.com/meeting", ""),
	} {
		events = append(events, s.Push(event)...)
	}
	final := "Created.\n\n18:00–18:30\nJoin: https://example.com/meeting\n"
	events = append(events, s.Finish(&final, true)...)
	var progress, answers []string
	var ids []string
	for _, event := range events {
		if event.Type != agent.EventText {
			continue
		}
		if event.Phase == agent.TextFinalAnswer {
			answers = append(answers, value(event.Delta))
		} else {
			progress = append(progress, value(event.Delta))
			ids = append(ids, value(event.ID))
		}
	}
	if strings.Join(progress, "|") != "Checking the calendar.|Creating the meeting." || len(answers) != 1 || answers[0] != final {
		t.Fatalf("progress=%q final=%q", progress, answers)
	}
	if ids[0] == "" || ids[0] == ids[1] {
		t.Fatalf("lost complete message boundaries: %q", ids)
	}
}

func TestStreamFinalSourcesAndFailure(t *testing.T) {
	for _, tc := range []struct {
		name         string
		phase        agent.TextPhase
		file         *string
		success      bool
		wantFinal    string
		wantProgress string
	}{
		{"legacy candidate", "", nil, true, "candidate", ""},
		{"explicit final", agent.TextFinalAnswer, nil, true, "candidate", ""},
		{"explicit commentary", agent.TextCommentary, nil, true, "", "candidate"},
		{"unknown phase", "future", nil, true, "", "candidate"},
		{"file wins", "", ptr("canonical answer"), true, "canonical answer", "candidate"},
		{"empty file", "", ptr(""), true, "", "candidate"},
		{"failure", agent.TextFinalAnswer, ptr("must not publish"), false, "", "candidate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &Stream{}
			events := append(s.Push(text("candidate", tc.phase)), s.Finish(tc.file, tc.success)...)
			var final, progress string
			for _, event := range events {
				if event.Phase == agent.TextFinalAnswer {
					final += value(event.Delta)
				} else {
					progress += value(event.Delta)
				}
			}
			if final != tc.wantFinal || progress != tc.wantProgress {
				t.Fatalf("final=%q progress=%q", final, progress)
			}
		})
	}
}

func TestStreamEmptyResultDoesNotPromoteProgress(t *testing.T) {
	s := &Stream{}
	s.Push(text("Still working", ""))
	events := append(s.Push(text("", agent.TextFinalAnswer)), s.Finish(nil, true)...)
	for _, event := range events {
		if event.Phase == agent.TextFinalAnswer && value(event.Delta) != "" {
			t.Fatalf("invented final: %#v", event)
		}
	}
}

func TestStreamAuthorizationIsImmediateAndDeduplicated(t *testing.T) {
	action, ok := LarkAuthorization("lark-cli auth login --no-wait --json", `{"verification_url":"https://example.com/auth?x=1&y=2","device_code":"private"}`)
	if !ok {
		t.Fatal("structured authorization was not recognized")
	}
	s := &Stream{}
	got := s.Push(action)
	if len(got) != 1 || got[0].Type != agent.EventUserAction || strings.Contains(value(got[0].Delta), "private") {
		t.Fatalf("authorization not immediately visible: %#v", got)
	}
	marker := `<bridge_user_action>{"id":"model-id","kind":"authorization","message":"请打开 https://example.com/auth?x=1&y=2"}</bridge_user_action>`
	if got := s.Push(text(marker, "")); len(got) != 0 {
		t.Fatalf("duplicate action: %#v", got)
	}
	if got := s.Push(agent.AgentEvent{Type: agent.EventToolUse}); len(got) != 1 || got[0].Type != agent.EventToolUse {
		t.Fatal("action ended stream")
	}
	if got := s.Finish(&marker, true); len(got) != 0 {
		t.Fatalf("action leaked into final: %#v", got)
	}
}

func TestUserActionRequiresExplicitCompleteProtocol(t *testing.T) {
	valid := `<bridge_user_action>{"kind":"question","message":"会议多长时间？"}</bridge_user_action>`
	if event, ok := UserAction(valid); !ok || value(event.ID) == "" || value(event.Name) != "question" {
		t.Fatalf("action=%#v ok=%v", event, ok)
	}
	for _, input := range []string{
		"请授权 https://example.com", "```" + valid + "```", "example: " + valid,
		`<bridge_user_action>{"kind":"progress","message":"working"}</bridge_user_action>`,
		`<bridge_user_action>{"kind":"question","message":""}</bridge_user_action>`,
		`<bridge_user_action>broken</bridge_user_action>`,
	} {
		if _, ok := UserAction(input); ok {
			t.Fatalf("classified ordinary or invalid text: %s", input)
		}
	}
}

func TestLarkAuthorizationRequiresMatchingCommandAndStructuredURL(t *testing.T) {
	for _, tc := range []struct {
		command, output string
		want            bool
	}{
		{"lark-cli auth login --json --no-wait --scope calendar", `{"data":{"verification_url":"https://example.com/auth"}}`, true},
		{"/bin/zsh -lc 'lark-cli auth login --no-wait --json'", `{"verification_url":"https://example.com/auth"}`, true},
		{`bash -c "lark-cli auth login --no-wait --json"`, `{"verification_url":"https://example.com/auth"}`, true},
		{"lark-cli auth login --no-wait --json; cat example.json", `{"verification_url":"https://example.com/auth"}`, false},
		{"cat example.json", `{"verification_url":"https://example.com/auth"}`, false},
		{"lark-cli auth login --json", `{"verification_url":"https://example.com/auth"}`, false},
		{"lark-cli auth login --json --no-wait", "click https://example.com/auth", false},
		{"lark-cli auth login --json --no-wait", `{"verification_url":"file:///secret"}`, false},
		{"lark-cli auth login --json --no-wait", `{"verification_url":"https://user:pass@example.com/auth"}`, false},
	} {
		if _, ok := LarkAuthorization(tc.command, tc.output); ok != tc.want {
			t.Fatalf("command=%s output=%s recognized=%v", tc.command, tc.output, ok)
		}
	}
}

func text(body string, phase agent.TextPhase) agent.AgentEvent {
	return agent.AgentEvent{Type: agent.EventText, Delta: &body, Phase: phase}
}
func ptr(s string) *string { return &s }
