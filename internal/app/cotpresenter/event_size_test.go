package cotpresenter

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	agentport "github.com/GatewayJ/lark-bridge-agent-sdk/internal/ports/agent"
)

func TestDetailedLargeArgsRemainCompleteAndOrdered(t *testing.T) {
	client := &fakeCOTClient{}
	publisher := NewPublisher(PublisherOptions{Client: client, ChatID: "chat", RunID: "run", UpdateThrottle: time.Hour})
	if !publisher.Start(context.Background()) {
		t.Fatal("启动失败")
	}
	command := strings.Repeat("中文🙂\n\t\"\\<>&", 3000)
	input := map[string]any{"command": command}
	err := ConsumeEvents(context.Background(), cotEventStream(
		toolUseEvent("tool-1", "command_execution", input),
		toolResultEvent("tool-1", "完成", false),
		agentport.AgentEvent{Type: agentport.EventDone, TerminationReason: agentport.TerminationNormal},
	), publisher, ModeDetailed)
	if err != nil {
		t.Fatal(err)
	}
	if publisher.Disabled() {
		t.Fatalf("意外回退：%s", publisher.DegradedReason())
	}
	var reconstructed strings.Builder
	started, ended, parts := false, false, 0
	for _, update := range client.updates {
		if len(update.Events) > updateEventLimit {
			t.Fatal("单次更新事件过多")
		}
		for _, event := range update.Events {
			wire, _ := json.Marshal(event)
			if len(wire) > eventWireBudget || !utf8.ValidString(event.Content) {
				t.Fatal("事件超过预算或包含无效 UTF-8")
			}
			var content map[string]any
			if err := json.Unmarshal([]byte(event.Content), &content); err != nil {
				t.Fatal(err)
			}
			switch event.EventType {
			case "TOOL_CALL_START":
				started = true
			case "TOOL_CALL_ARGS":
				if !started || ended || content["toolCallId"] != "tool-1" {
					t.Fatal("参数顺序或关联 ID 错误")
				}
				reconstructed.WriteString(content["delta"].(string))
				parts++
			case "TOOL_CALL_END":
				ended = true
			}
		}
	}
	expected, _ := json.Marshal(input)
	if reconstructed.String() != string(expected) || parts < 2 || !ended {
		t.Fatal("分段后参数不完整")
	}
	if len(client.completes) != 1 {
		t.Fatal("缺少完成事件")
	}
}

func TestBoundedTextPreservesUnicodeAndEscapes(t *testing.T) {
	for _, kind := range []string{"TEXT_MESSAGE_CONTENT", "REASONING_MESSAGE_CONTENT"} {
		t.Run(kind, func(t *testing.T) {
			original := strings.Repeat("文字🙂\n\"\\<>&", 1500)
			events, err := boundedEvents(kind, map[string]any{"messageId": "message", "delta": original}, 1)
			if err != nil {
				t.Fatal(err)
			}
			var got strings.Builder
			for _, event := range events {
				wire, _ := json.Marshal(event)
				if len(wire) > eventWireBudget {
					t.Fatal("编码后的事件超过预算")
				}
				var fields map[string]any
				if err := json.Unmarshal([]byte(event.Content), &fields); err != nil {
					t.Fatal(err)
				}
				if fields["messageId"] != "message" {
					t.Fatal("关联 ID 改变")
				}
				got.WriteString(fields["delta"].(string))
			}
			if got.String() != original {
				t.Fatal("文字内容改变")
			}
		})
	}
}

func TestBoundedDisplayFieldsAndInvalidMetadata(t *testing.T) {
	for _, tc := range []struct {
		kind   string
		fields map[string]any
	}{
		{"RUN_STARTED", map[string]any{"runId": "run", "input": map[string]any{"query": strings.Repeat("内容", 5000)}}},
		{"TOOL_CALL_START", map[string]any{"toolCallId": "tool", "title": strings.Repeat("路径", 5000)}},
		{"TOOL_CALL_RESULT", map[string]any{"toolCallId": "tool", "content": strings.Repeat("输出", 5000)}},
		{"RUN_ERROR", map[string]any{"message": strings.Repeat("错误", 5000)}},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			events, err := boundedEvents(tc.kind, tc.fields, 1)
			if err != nil {
				t.Fatal(err)
			}
			if len(events) != 1 {
				t.Fatal("非增量事件数量改变")
			}
			wire, _ := json.Marshal(events[0])
			if len(wire) > eventWireBudget || !strings.Contains(events[0].Content, "…") {
				t.Fatal("展示内容未按预算缩短")
			}
		})
	}
	if _, err := boundedEvents("TOOL_CALL_ARGS", map[string]any{"toolCallId": strings.Repeat("id", 2000), "delta": "text"}, 1); err == nil {
		t.Fatal("超长固定字段应当返回错误")
	}
	if _, err := boundedEvents("RUN_STARTED", make(chan int), 1); err == nil {
		t.Fatal("无法编码的内容应当返回错误")
	}
}
