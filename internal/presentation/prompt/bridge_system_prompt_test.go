package prompt

import (
	"strings"
	"testing"
)

func TestBridgeSystemPromptContainsBridgeRules(t *testing.T) {
	needles := []string{
		"lark-cli",
		"LARKSUITE_CLI_CONFIG_DIR",
		"飞书 OAuth 授权",
		"lark-cli auth login --device-code",
		"只有被真实 @",
		"默认不要 @ 其他 bot",
		"botOpenId",
	}

	for _, needle := range needles {
		if !strings.Contains(BRIDGE_SYSTEM_PROMPT, needle) {
			t.Fatalf("BRIDGE_SYSTEM_PROMPT missing %q", needle)
		}
	}
}

func TestBuildBridgeSystemPromptAppendsIdentity(t *testing.T) {
	if got := BuildBridgeSystemPrompt(nil); got != BRIDGE_SYSTEM_PROMPT {
		t.Fatal("BuildBridgeSystemPrompt(nil) did not return base prompt")
	}

	got := BuildBridgeSystemPrompt(&AgentBotIdentity{
		OpenID: "ou_bot_self",
		Name:   "尼莫",
	})
	if !strings.HasPrefix(got, BRIDGE_SYSTEM_PROMPT) {
		t.Fatal("identity prompt does not start with base prompt")
	}
	for _, needle := range []string{"ou_bot_self", "尼莫", "你的 open_id"} {
		if !strings.Contains(got, needle) {
			t.Fatalf("identity prompt missing %q", needle)
		}
	}
}

func TestPrefixBridgeSystemPrompt(t *testing.T) {
	got := PrefixBridgeSystemPrompt("hello world", &AgentBotIdentity{OpenID: "ou_bot_self"})
	if !strings.Contains(got, "ou_bot_self") {
		t.Fatal("prefixed prompt missing identity")
	}
	if strings.Index(got, "ou_bot_self") > strings.Index(got, "## user_message") {
		t.Fatal("identity appears after user message heading")
	}
	if !strings.HasSuffix(got, "hello world") {
		t.Fatal("prefixed prompt does not end with user prompt")
	}
}

func TestBridgeRulesCoverActionAndEnvironmentRequirements(t *testing.T) {
	for _, rule := range []string{
		"chatId", "chatType", "<quoted_messages>", "<interactive_cards>", "JSON 数组", "rejectionReason", "skipped",
		"结构化 @", "没有新信息", "schema: \"2.0\"", "SIGNED_TOKEN_FROM_LARK_CLI", "禁止猜测、伪造、复用或手写", "文字回复选择",
		"LARK_CHANNEL_CONFIG", "以命令返回结果为准", "doctor/preflight",
		"authorization", "confirmation", "question", "相同 `id`", "发送后结束本轮", "文档评论",
		"群聊（含 topic 群）", "--no-wait --json", "verification_url", "device_code", "前台等待", "strict-mode off", "default-as auto", "/stop",
	} {
		if !strings.Contains(BRIDGE_SYSTEM_PROMPT, rule) {
			t.Errorf("missing rule %q", rule)
		}
	}
}
