package lark

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestFinalDeliveryRetriesWithStableUUID(t *testing.T) {
	for _, reply := range []bool{false, true} {
		t.Run(fmt.Sprint(reply), func(t *testing.T) {
			var ids []string
			var bodies []string
			server := newOAPICommentTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if strings.Contains(r.URL.Path, "tenant_access_token") {
					fmt.Fprint(w, `{"code":0,"tenant_access_token":"test-token","expire":7200}`)
					return
				}

				var body struct {
					UUID    string `json:"uuid"`
					Content string `json:"content"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				ids = append(ids, body.UUID)
				bodies = append(bodies, body.Content)
				if len(ids) == 1 {
					fmt.Fprint(w, `{"code":99991400,"msg":"rate limited"}`)
					return
				}
				fmt.Fprint(w, `{"code":0,"data":{"message_id":"om_final"}}`)
			})
			transport := newOAPICommentTestTransport(t, server)
			opts := SendOptions{Metadata: map[string]any{"finalDelivery": true}}
			if reply {
				opts.ReplyTo = "om_original"
			}
			result, err := transport.SendMessage(context.Background(), SendMessageRequest{ChatID: "oc_chat", Content: MessageContent{Markdown: "最终回复"}, Options: opts})
			if err != nil {
				t.Fatal(err)
			}
			if result.MessageID != "om_final" || len(ids) != 2 || ids[0] == "" || ids[0] != ids[1] || bodies[0] != bodies[1] {
				t.Fatalf("ids=%v result=%#v", ids, result)
			}
		})
	}
}

func TestFinalDeliveryChunksUseDistinctUUIDs(t *testing.T) {
	seen := map[string]bool{}
	server := newOAPICommentTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "tenant_access_token") {
			fmt.Fprint(w, `{"code":0,"tenant_access_token":"test-token","expire":7200}`)
			return
		}

		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		id, _ := body["uuid"].(string)
		if id == "" || seen[id] {
			t.Errorf("duplicate or empty uuid %q", id)
		}
		seen[id] = true
		fmt.Fprint(w, `{"code":0,"data":{"message_id":"om_final"}}`)
	})
	transport := newOAPICommentTestTransport(t, server)
	_, err := transport.SendMessage(context.Background(), SendMessageRequest{ChatID: "oc_chat", Content: MessageContent{Markdown: strings.Repeat("hello world\n", 500)}, Options: SendOptions{Metadata: map[string]any{"finalDelivery": true}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) < 2 {
		t.Fatal("expected multiple chunks")
	}
}
