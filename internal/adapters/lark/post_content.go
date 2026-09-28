package lark

import (
	"encoding/json"
	larknormalize "github.com/larksuite/oapi-sdk-go/v3/channel/normalize"
	channeltypes "github.com/larksuite/oapi-sdk-go/v3/channel/types"
)

func parseOAPIMessageContent(messageType, raw string) (string, []channeltypes.Resource) {
	if messageType == "post" {
		var body map[string]json.RawMessage
		if json.Unmarshal([]byte(raw), &body) == nil {
			_, content := body["content"]
			_, contentV2 := body["content_v2"]
			if content || contentV2 {
				// SDK 的富文本解析器要求正文外层包含语言字段。
				wrapped, err := json.Marshal(map[string]json.RawMessage{"zh_cn": json.RawMessage(raw)})
				if err == nil {
					raw = string(wrapped)
				}
			}
		}
	}
	return larknormalize.ParseContent(messageType, raw)
}
