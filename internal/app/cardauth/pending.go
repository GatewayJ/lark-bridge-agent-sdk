package cardauth

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"sync"
)

// Pending callbacks belong to a sent card and survive the issuing run.
// Only token hashes are stored; signatures and signing keys stay private.
type pendingCallback struct {
	Expected  VerifyExpected `json:"expected"`
	ExpiresAt int64          `json:"expiresAt"`
	Values    []string       `json:"values"`
}

type pendingStore struct {
	mu      sync.Mutex
	path    string
	entries map[string]pendingCallback
}

func newPendingStore(path string) (*pendingStore, error) {
	s := &pendingStore{path: path, entries: map[string]pendingCallback{}}
	if path == "" {
		return s, nil
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &s.entries); err != nil {
		return nil, err
	}
	if s.entries == nil {
		s.entries = map[string]pendingCallback{}
	}
	return s, nil
}

func digest(raw []byte) string {
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}

func valueDigest(value map[string]any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return digest(raw), nil
}

func (s *pendingStore) persist() error {
	if s.path == "" {
		return nil
	}
	raw, err := json.Marshal(s.entries)
	if err != nil {
		return err
	}
	return writeAtomic(s.path, raw, 0o600)
}

// SignPending issues one single-use token for all choices on a card.
func (a *Auth) SignPending(input SignInput, values []map[string]any) (string, error) {
	if input.Action != "agent_callback" || input.TTL <= 0 || len(values) == 0 || input.RunID == "" || input.Scope == "" || input.ChatID == "" || input.OperatorOpenID == "" || input.PolicyFingerprint == "" {
		return "", errors.New("pending card requires agent_callback, a positive TTL and callback values")
	}
	record := pendingCallback{
		Expected:  VerifyExpected{RunID: input.RunID, Scope: input.Scope, ChatID: input.ChatID, OperatorOpenID: input.OperatorOpenID, Action: input.Action, PolicyFingerprint: input.PolicyFingerprint},
		ExpiresAt: a.now().Add(input.TTL).UnixMilli(),
	}
	for _, value := range values {
		h, err := valueDigest(value)
		if err != nil {
			return "", err
		}
		record.Values = append(record.Values, h)
	}
	token, err := a.Sign(input)
	if err != nil {
		return "", err
	}
	s := a.pending
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, entry := range s.entries {
		if entry.ExpiresAt <= a.now().UnixMilli() {
			delete(s.entries, key)
		}
	}
	key := digest([]byte(token))
	s.entries[key] = record
	if err := s.persist(); err != nil {
		delete(s.entries, key)
		return "", err
	}
	return token, nil
}

// VerifyPending verifies the recorded issuing context and the actual submitter.
// Command callbacks continue to use Verify with an active run.
func (a *Auth) VerifyPending(token string, expected VerifyExpected, value map[string]any) VerifyResult {
	s := a.pending
	s.mu.Lock()
	defer s.mu.Unlock()
	key := digest([]byte(token))
	record, ok := s.entries[key]
	if !ok {
		return failed(VerifyContextMismatch)
	}
	if record.ExpiresAt <= a.now().UnixMilli() {
		return failed(VerifyExpired)
	}
	if expected.Action != "agent_callback" || record.Expected.Scope != expected.Scope || record.Expected.ChatID != expected.ChatID || record.Expected.OperatorOpenID != expected.OperatorOpenID {
		return failed(VerifyContextMismatch)
	}
	h, err := valueDigest(value)
	if err != nil {
		return failed(VerifyMalformed)
	}
	allowed := false
	for _, candidate := range record.Values {
		if h == candidate {
			allowed = true
			break
		}
	}
	if !allowed {
		return failed(VerifyContextMismatch)
	}
	result := a.Verify(token, record.Expected)
	if result.OK {
		delete(s.entries, key)
		// The nonce is persisted by Verify before the pending record is removed.
		if err := errors.Join(a.nonceStore.Flush(), s.persist()); err != nil {
			return failed(VerifyNonceRevoked)
		}
	}
	return result
}

func (a *Auth) CancelPending(token string) error {
	s := a.pending
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, digest([]byte(token)))
	return s.persist()
}
