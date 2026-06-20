package worker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"bilibili-ticket-golang/cluster/domain"
	"bilibili-ticket-golang/cluster/executor"
)

type backend struct {
	block   <-chan struct{}
	outcome executor.Outcome
}

func (b backend) Attempt(ctx context.Context, _ domain.ExecutionSpec) executor.Outcome {
	if b.block != nil {
		select {
		case <-ctx.Done():
			return executor.Outcome{Err: ctx.Err()}
		case <-b.block:
		}
	}
	return b.outcome
}
func (backend) Credentials() domain.Credentials { return domain.Credentials{Version: 3} }

func workerSpec(id string) domain.ExecutionSpec {
	return domain.ExecutionSpec{AttemptID: id, IntentID: "i", ProjectID: 1, ScreenID: 2, SKUID: 3, Buyers: []domain.Buyer{{LogicalID: "b"}}, StartMode: domain.StartImmediate, Deadline: time.Now().Add(time.Minute)}
}
func request(t *testing.T, h http.Handler, method, path, body, key string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestAuthIdempotencyConflictAndSingleSlot(t *testing.T) {
	block := make(chan struct{})
	s, err := NewServer(Config{BearerKey: "secret", DataDir: t.TempDir(), PollInterval: 10 * time.Second}, func(domain.ExecutionSpec) (executor.Backend, error) { return backend{block: block}, nil })
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(workerSpec("a"))
	h := s.Handler()
	if got := request(t, h, http.MethodGet, "/v1/health", "", "bad").Code; got != http.StatusUnauthorized {
		t.Fatalf("auth=%d", got)
	}
	if got := request(t, h, http.MethodPost, "/v1/tasks", string(b), "secret").Code; got != http.StatusAccepted {
		t.Fatalf("create=%d", got)
	}
	if got := request(t, h, http.MethodPost, "/v1/tasks", string(b), "secret").Code; got != http.StatusOK {
		t.Fatalf("idempotent=%d", got)
	}
	other := workerSpec("a")
	other.SKUID = 9
	changed, _ := json.Marshal(other)
	if got := request(t, h, http.MethodPost, "/v1/tasks", string(changed), "secret").Code; got != http.StatusConflict {
		t.Fatalf("conflict=%d", got)
	}
	busy, _ := json.Marshal(workerSpec("b"))
	if got := request(t, h, http.MethodPost, "/v1/tasks", string(busy), "secret").Code; got != http.StatusConflict {
		t.Fatalf("busy=%d", got)
	}
	if got := request(t, h, http.MethodPost, "/v1/tasks/a/stop", "", "secret").Code; got != http.StatusAccepted {
		t.Fatalf("stop=%d", got)
	}
}

func TestSuccessPersistsAndSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{BearerKey: "secret", DataDir: dir, PollInterval: 10 * time.Second}
	s, _ := NewServer(cfg, func(domain.ExecutionSpec) (executor.Backend, error) {
		return backend{outcome: executor.Outcome{OrderID: "o"}}, nil
	})
	b, _ := json.Marshal(workerSpec("a"))
	_ = request(t, s.Handler(), http.MethodPost, "/v1/tasks", string(b), "secret")
	deadline := time.Now().Add(time.Second)
	succeeded := false
	for time.Now().Before(deadline) {
		w := request(t, s.Handler(), http.MethodGet, "/v1/tasks/a", "", "secret")
		var status Status
		_ = json.Unmarshal(w.Body.Bytes(), &status)
		if status.State == domain.AttemptSucceeded {
			succeeded = true
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !succeeded {
		t.Fatal("attempt did not succeed")
	}
	restarted, err := NewServer(cfg, func(domain.ExecutionSpec) (executor.Backend, error) { return backend{}, nil })
	if err != nil {
		t.Fatal(err)
	}
	if got := request(t, restarted.Handler(), http.MethodGet, "/v1/tasks/a", "", "secret").Code; got != http.StatusOK {
		t.Fatalf("persisted status=%d", got)
	}
	changed := workerSpec("a")
	changed.SKUID = 99
	changedJSON, _ := json.Marshal(changed)
	if got := request(t, restarted.Handler(), http.MethodPost, "/v1/tasks", string(changedJSON), "secret").Code; got != http.StatusConflict {
		t.Fatalf("persisted spec conflict=%d", got)
	}
}

func TestLeaseDefaultsAndStatusRenews(t *testing.T) {
	cfg := Config{BearerKey: "secret", DataDir: t.TempDir(), PollInterval: 10 * time.Second}
	s, _ := NewServer(cfg, func(domain.ExecutionSpec) (executor.Backend, error) { return backend{block: make(chan struct{})}, nil })
	b, _ := json.Marshal(workerSpec("a"))
	_ = request(t, s.Handler(), http.MethodPost, "/v1/tasks", string(b), "secret")
	s.mu.Lock()
	first := s.tasks["a"].leaseUntil
	s.mu.Unlock()
	if first.Sub(time.Now()) < 179*time.Second {
		t.Fatalf("lease too short: %v", first.Sub(time.Now()))
	}
	time.Sleep(2 * time.Millisecond)
	_ = request(t, s.Handler(), http.MethodGet, "/v1/tasks/a", "", "secret")
	s.mu.Lock()
	renewed := s.tasks["a"].leaseUntil
	s.mu.Unlock()
	if !renewed.After(first) {
		t.Fatal("status did not renew lease")
	}
}

func TestSuccessStoreNeverPersistsCredentials(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenSuccessStore(filepath.Join(dir, "success-orders.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	result := domain.ExecutionResult{AttemptID: "a", SpecHash: "hash", Success: true, Credentials: domain.Credentials{Cookies: map[string]string{"SESSDATA": "secret"}, RefreshToken: "refresh-secret"}}
	if err := store.Append(result); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "success-orders.jsonl"))
	if strings.Contains(string(data), "secret") {
		t.Fatalf("credential leaked to success record: %s", data)
	}
	info, _ := os.Stat(filepath.Join(dir, "success-orders.jsonl"))
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode=%o", info.Mode().Perm())
	}
}
