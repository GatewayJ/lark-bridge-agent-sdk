package codexcli

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/GatewayJ/lark-bridge-agent-sdk/internal/domain/permissions"
	agentport "github.com/GatewayJ/lark-bridge-agent-sdk/internal/ports/agent"
)

func TestAdapterUsesFinalFileAfterProcessExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake uses POSIX sh")
	}
	for _, tc := range []struct{ name, thread, exit string }{
		{"fresh", "", "0"}, {"resume", "thread-existing", "0"}, {"failed exit", "", "7"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cwd := t.TempDir()
			binary := writeFakeCodex(t, `
while [ "$#" -gt 0 ]; do
  if [ "$1" = "--output-last-message" ]; then shift; answer_path="$1"; fi
  shift
done
cat > /dev/null
printf '%s' "$answer_path" > answer_path.txt
printf '%s\n' '{"type":"agent_message","message":"Checking the calendar"}'
printf '%s\n' '{"type":"agent_message","message":"Candidate answer"}'
printf '%s\n' '{"type":"turn.completed"}'
printf 'Canonical final\n\nMeeting created' > "$answer_path"
exit `+tc.exit)
			run, err := New(Options{Binary: binary, ProfileStateDir: t.TempDir()}).Run(context.Background(), agentport.AgentRunOptions{
				RunID: "final-file", Prompt: "test", CWD: cwd, ThreadID: tc.thread, Sandbox: permissions.CodexSandboxDangerFullAccess,
			})
			if err != nil {
				t.Fatal(err)
			}
			events := collectEvents(t, run)
			var final string
			for _, event := range events {
				if event.Type == agentport.EventText && event.Phase == agentport.TextFinalAnswer {
					final += *event.Delta
				}
			}
			if tc.exit == "0" {
				if final != "Canonical final\n\nMeeting created" || events[len(events)-1].Type != agentport.EventDone {
					t.Fatalf("final=%q events=%#v", final, events)
				}
			} else if final != "" || events[len(events)-1].Type != agentport.EventError {
				t.Fatalf("published successful final on failure: %#v", events)
			}
			path := readFile(t, filepath.Join(cwd, "answer_path.txt"))
			if path == "" {
				t.Fatal("output flag missing")
			}
			if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
				t.Fatalf("answer directory not cleaned: %v", err)
			}
		})
	}
}

func TestAdapterEmitsAuthorizationBeforeWaitingToolFinishes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake uses POSIX sh")
	}
	cwd := t.TempDir()
	binary := writeFakeCodex(t, `
cat > /dev/null
printf '%s\n' '{"type":"item.started","item":{"type":"command_execution","id":"login","command":"lark-cli auth login --no-wait --json"}}'
printf '%s\n' '{"type":"item.completed","item":{"type":"command_execution","id":"login","exit_code":0,"aggregated_output":"{\"verification_url\":\"https://example.com/auth\",\"device_code\":\"private\"}"}}'
while [ ! -f release ]; do sleep 0.01; done
printf '%s\n' '{"type":"agent_message","message":"Authorized and completed"}'
printf '%s\n' '{"type":"turn.completed"}'
`)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	run, err := New(Options{Binary: binary, ProfileStateDir: t.TempDir()}).Run(ctx, agentport.AgentRunOptions{
		RunID: "auth-live", Prompt: "test", CWD: cwd, Sandbox: permissions.CodexSandboxDangerFullAccess,
	})
	if err != nil {
		t.Fatal(err)
	}
	events := run.Events()
	for {
		select {
		case event, ok := <-events:
			if !ok || event.Type == agentport.EventDone || event.Type == agentport.EventError {
				t.Fatalf("run ended before prompt: %#v", event)
			}
			if event.Type != agentport.EventUserAction {
				continue
			}
			if event.Delta == nil || !strings.Contains(*event.Delta, "https://example.com/auth") || strings.Contains(*event.Delta, "private") {
				t.Fatalf("action=%#v", event)
			}
			if err := os.WriteFile(filepath.Join(cwd, "release"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			remaining := collectEvents(t, run)
			if len(remaining) != 2 || remaining[0].Phase != agentport.TextFinalAnswer || remaining[1].Type != agentport.EventDone {
				t.Fatalf("did not resume same run: %#v", remaining)
			}
			return
		case <-ctx.Done():
			t.Fatal("authorization prompt was deferred")
		}
	}
}
