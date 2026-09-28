package lark

import (
	"encoding/json"
	channeltypes "github.com/larksuite/oapi-sdk-go/v3/channel/types"
	"strings"
	"testing"
)

func TestMapMessagePreservesFlatPostTextAndImages(t *testing.T) {
	body := `{"title":"任务","content":[[{"tag":"text","text":"旧内容"}]],"content_v2":[[{"tag":"img","image_key":"img_test"}],[{"tag":"text","text":"移除更多按钮并提交 draft PR"},{"tag":"a","text":"参考","href":"https://example.com"}]]}`
	for _, wrapped := range []bool{false, true} {
		raw := body
		if wrapped {
			raw = `{"zh_cn":` + body + `}`
		}
		content, resources := parseOAPIMessageContent("post", raw)
		if !strings.Contains(content, "移除更多按钮并提交 draft PR") || !strings.Contains(content, "https://example.com") || strings.Contains(content, "旧内容") {
			t.Fatalf("正文解析错误：%s", content)
		}
		if len(resources) != 1 || resources[0].FileKey != "img_test" {
			t.Fatalf("图片解析错误：%#v", resources)
		}
	}
	transport := &OAPITransport{}
	mapped := transport.mapMessage(&channeltypes.NormalizedMessage{
		MessageID: "om_post", ChatID: "oc_chat", ChatType: "p2p", RawContentType: "post", Content: "[rich text message]",
		RawEvent: map[string]any{"event": map[string]any{"message": map[string]any{"content": body}}},
	})
	if !strings.Contains(mapped.Content, "移除更多按钮") || len(mapped.Resources) != 1 {
		t.Fatalf("输入消息缺少正文或附件：%#v", mapped)
	}
}

func TestPostContentVariants(t *testing.T) {
	for _, tc := range []struct {
		name, raw, want string
		images          int
	}{
		{"content", `{"title":"","content":[[{"tag":"text","text":"正文"}]]}`, "正文", 0},
		{"image", `{"title":"","content":[[{"tag":"img","image_key":"img_only"}]]}`, "img_only", 1},
		{"invalid", `{`, "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			content, resources := parseOAPIMessageContent("post", tc.raw)
			if !strings.Contains(content, tc.want) || len(resources) != tc.images {
				t.Fatalf("解析结果：%q %#v", content, resources)
			}
		})
	}
	raw, _ := json.Marshal(map[string]string{"text": "普通文字"})
	content, _ := parseOAPIMessageContent("text", string(raw))
	if content != "普通文字" {
		t.Fatalf("普通文字改变：%s", content)
	}
}
