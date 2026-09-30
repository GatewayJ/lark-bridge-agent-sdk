package cardauth

import (
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPendingCallbackSurvivesRestartAndAcceptsOnlyOneChoice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonces.json")
	now := time.Unix(100, 0)
	newAuth := func() *Auth {
		store := NewNonceStore(path)
		if err := store.Load(); err != nil {
			t.Fatal(err)
		}
		a, err := New(Options{Keys: []Key{{Version: 1, Secret: "test-key"}}, NonceStore: store, Now: func() time.Time { return now }})
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	a := newAuth()
	input := SignInput{RunID: "finished-run", Scope: "chat:thread", ChatID: "chat", OperatorOpenID: "user", Action: "agent_callback", PolicyFingerprint: "fp", TTL: time.Hour}
	values := []map[string]any{{"choice": "test"}, {"choice": "production"}}
	token, err := a.SignPending(input, values)
	if err != nil {
		t.Fatal(err)
	}
	a = newAuth()
	expected := VerifyExpected{Scope: input.Scope, ChatID: input.ChatID, OperatorOpenID: input.OperatorOpenID, Action: input.Action}
	for _, field := range []string{"scope", "chat", "operator", "action", "value", "signature"} {
		t.Run(field, func(t *testing.T) {
			e, v, candidate := expected, values[0], token
			switch field {
			case "scope":
				e.Scope = "other"
			case "chat":
				e.ChatID = "other"
			case "operator":
				e.OperatorOpenID = "other"
			case "action":
				e.Action = "stop"
			case "value":
				v = map[string]any{"choice": "injected"}
			case "signature":
				candidate += "x"
			}
			if result := a.VerifyPending(candidate, e, v); result.OK {
				t.Fatal("invalid callback accepted")
			}
		})
	}
	if result := a.VerifyPending(token, expected, values[1]); !result.OK {
		t.Fatalf("valid callback rejected: %s", result.Reason)
	}
	a = newAuth()
	if result := a.VerifyPending(token, expected, values[0]); result.OK {
		t.Fatal("card replay accepted after restart")
	}
}

func TestPendingCallbackExpiryCancellationAndConcurrentSubmission(t *testing.T) {
	now := time.Unix(100, 0)
	a, err := New(Options{Keys: []Key{{Version: 1, Secret: "test-key"}}, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	input := SignInput{RunID: "run", Scope: "chat", ChatID: "chat", OperatorOpenID: "user", Action: "agent_callback", PolicyFingerprint: "fp", TTL: time.Hour}
	values := []map[string]any{{"submit": true}}
	expected := VerifyExpected{Scope: "chat", ChatID: "chat", OperatorOpenID: "user", Action: "agent_callback"}
	issue := func() string {
		token, err := a.SignPending(input, values)
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	expired := issue()
	now = now.Add(time.Hour)
	if result := a.VerifyPending(expired, expected, values[0]); result.Reason != VerifyExpired {
		t.Fatalf("result=%+v", result)
	}
	canceled := issue()
	if err := a.CancelPending(canceled); err != nil {
		t.Fatal(err)
	}
	if result := a.VerifyPending(canceled, expected, values[0]); result.OK {
		t.Fatal("canceled callback accepted")
	}
	token := issue()
	var accepted atomic.Int32
	var wg sync.WaitGroup
	for j := 0; j < 10; j++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if a.VerifyPending(token, expected, values[0]).OK {
				accepted.Add(1)
			}
		}()
	}
	wg.Wait()
	if accepted.Load() != 1 {
		t.Fatalf("accepted=%d", accepted.Load())
	}
}
