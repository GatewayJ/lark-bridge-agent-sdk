//go:build !windows

package claudecli

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	agentport "github.com/GatewayJ/lark-bridge-agent-sdk/internal/ports/agent"
)

func TestAdapterStopTerminatesChildProcessGroup(t *testing.T) {
	cwd := t.TempDir()
	binary := writeFakeClaude(t, `
sleep 30 >/dev/null 2>&1 &
printf '%s' "$!" > child_pid.txt
printf '%s\n' '{"type":"system","subtype":"init","session_id":"sess-stop-group"}'
wait
	`)

	adapter := New(Options{
		Binary:    binary,
		StopGrace: 50 * time.Millisecond,
	})
	run, err := adapter.Run(context.Background(), agentRunOptions("run-stop-group", "stop process group", cwd))
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	first := <-run.Events()
	if first.Type == "" {
		t.Fatalf("unexpected empty first event: %#v", first)
	}
	childPID := readPID(t, filepath.Join(cwd, "child_pid.txt"))
	if !processExists(childPID) {
		t.Fatalf("child process %d was not running before Stop", childPID)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := run.Stop(ctx); err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}
	for range run.Events() {
	}

	deadline := time.Now().Add(5 * time.Second)
	for processExists(childPID) && time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
	}
	if processExists(childPID) {
		_ = syscall.Kill(childPID, syscall.SIGKILL)
		t.Fatalf("child process %d survived Stop; process group was not terminated", childPID)
	}
}

func agentRunOptions(runID string, prompt string, cwd string) agentport.AgentRunOptions {
	return agentport.AgentRunOptions{RunID: runID, Prompt: prompt, CWD: cwd}
}

func readPID(t *testing.T, path string) int {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read pid file: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("parse pid %q: %v", raw, err)
	}
	return pid
}

func processExists(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
