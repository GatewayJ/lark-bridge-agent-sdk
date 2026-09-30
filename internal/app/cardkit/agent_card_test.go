package cardkit

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPrepareAgentFormAddsSubmitCallbackWithoutMutatingRequest(t *testing.T) {
	var request map[string]any
	if err := json.Unmarshal([]byte(`{"schema":"2.0","body":{"elements":[{"tag":"form","name":"app","elements":[{"tag":"select_static","name":"environment","required":true,"options":[{"text":{"tag":"plain_text","content":"Test"},"value":"test"},{"text":{"tag":"plain_text","content":"Production"},"value":"production"}]},{"tag":"input","name":"app_name","required":true},{"tag":"button","name":"submit","form_action_type":"submit"}]}]}}`), &request); err != nil {
		t.Fatal(err)
	}
	card, values, err := PrepareAgentCard(request)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 {
		t.Fatalf("callbacks=%d", len(values))
	}
	values[0]["bridge_token"] = "test-token"
	raw, _ := json.Marshal(card)
	if !strings.Contains(string(raw), "test-token") {
		t.Fatal("callback value is detached from card")
	}
	original, _ := json.Marshal(request)
	if strings.Contains(string(original), "behaviors") {
		t.Fatal("request was mutated")
	}
}

func TestPrepareAgentCardRejectsReservedCallbackFields(t *testing.T) {
	for _, key := range []string{"cmd", "bridge_token", "__bridge_cb", "__claude_cb", "form_value"} {
		request := map[string]any{"schema": "2.0", "body": map[string]any{"elements": []any{map[string]any{"tag": "button", "behaviors": []any{map[string]any{"type": "callback", "value": map[string]any{key: "injected"}}}}}}}
		if _, _, err := PrepareAgentCard(request); err == nil {
			t.Fatalf("accepted reserved field %s", key)
		}
	}
}
