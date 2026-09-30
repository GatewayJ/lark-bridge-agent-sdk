package commands

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GatewayJ/lark-bridge-agent-sdk/internal/domain/permissions"
	"github.com/GatewayJ/lark-bridge-agent-sdk/internal/domain/profile"
)

func TestConfigWorkspaceValidationAndPersistence(t *testing.T) {
	cfg, cap, cwd := commandTestConfig(t, profile.AgentCodex, permissions.AccessFull)
	path := filepath.Join(t.TempDir(), "config.json")
	writeCommandRoot(t, path, "codex-dev", cfg, nil)
	service := New(Options{ProfileName: "codex-dev", ProfileConfig: cfg, Capability: cap, RuntimeControls: ownerControls(), ConfigPath: path})
	before, _ := os.ReadFile(path)
	for _, invalid := range []string{"", "relative", "/", filepath.Join(t.TempDir(), "missing")} {
		req := commandRequest("/config submit", "scope-1", cwd, ChatModeP2P)
		req.FormValue = map[string]any{"default_workspace": invalid}
		response, err := service.Handle(context.Background(), req)
		if err != nil || response.Config.Failure == "" || response.Config.Saved {
			t.Fatalf("invalid directory accepted: %#v %v", response, err)
		}
		after, _ := os.ReadFile(path)
		if string(before) != string(after) {
			t.Fatal("invalid directory changed configuration")
		}
	}
	next := t.TempDir()
	req := commandRequest("/config submit", "scope-1", cwd, ChatModeP2P)
	req.FormValue = map[string]any{"default_workspace": next}
	response, err := service.Handle(context.Background(), req)
	if err != nil || !response.Config.Saved || response.Config.Snapshot.DefaultWorkspace != next {
		t.Fatalf("save: %#v %v", response, err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), next) {
		t.Fatal("directory was not persisted")
	}
}

func TestStatusCodexUsageAndFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		cfg, cap, cwd := commandTestConfig(t, profile.AgentCodex, permissions.AccessFull)
		service := New(Options{ProfileConfig: cfg, Capability: cap, RuntimeControls: ownerControls(), CodexUsage: func(context.Context) (string, error) {
			if fail {
				return "", errors.New("unavailable")
			}
			return "已使用 25%", nil
		}})
		response, err := service.Handle(context.Background(), commandRequest("/status", "scope-1", cwd, ChatModeP2P))
		if err != nil {
			t.Fatal(err)
		}
		expected := "已使用 25%"
		if fail {
			expected = "暂时无法获取"
		}
		if !strings.Contains(response.Markdown, expected) || !strings.Contains(response.Markdown, cwd) {
			t.Fatalf("status=%s", response.Markdown)
		}
	}
}
