package worker

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	biliclock "bilibili-ticket-golang/biliutils/clock"
	"bilibili-ticket-golang/cluster/domain"
	"bilibili-ticket-golang/cluster/executor"
)

type Config struct {
	Listen           string        `json:"listen"`
	BearerKey        string        `json:"bearerKey"`
	DataDir          string        `json:"dataDir"`
	PollInterval     time.Duration `json:"-"`
	PollIntervalSec  int           `json:"pollIntervalSec"`
	LeaseDuration    time.Duration `json:"-"`
	LeaseDurationSec int           `json:"leaseDurationSec"`
	WorkerID         string        `json:"workerId"`
	Version          string        `json:"version"`
	PluginVersion    string        `json:"pluginVersion,omitempty"`
	AlgorithmVersion string        `json:"algorithmVersion,omitempty"`
	PluginDir        string        `json:"pluginDir,omitempty"`
	CaptchaPlugin    string        `json:"captchaPlugin,omitempty"`
	CalibrateClock   bool          `json:"calibrateClock,omitempty"`
}

func (c *Config) Normalize() error {
	if c.Listen == "" {
		c.Listen = "127.0.0.1:18080"
	}
	if c.BearerKey == "" {
		return errors.New("bearerKey is required")
	}
	if c.DataDir == "" {
		c.DataDir = "data/worker"
	}
	if c.PollInterval == 0 {
		c.PollInterval = time.Duration(c.PollIntervalSec) * time.Second
	}
	if c.PollInterval == 0 {
		c.PollInterval = 15 * time.Second
	}
	if c.PollInterval < 10*time.Second || c.PollInterval > 60*time.Second {
		return errors.New("poll interval must be between 10 and 60 seconds")
	}
	if c.LeaseDuration == 0 {
		c.LeaseDuration = time.Duration(c.LeaseDurationSec) * time.Second
	}
	minimum := 3 * c.PollInterval
	if minimum < 180*time.Second {
		minimum = 180 * time.Second
	}
	if c.LeaseDuration < minimum {
		c.LeaseDuration = minimum
	}
	return nil
}

type BackendFactory func(domain.ExecutionSpec) (executor.Backend, error)

type task struct {
	spec       domain.ExecutionSpec
	specHash   string
	state      domain.AttemptState
	result     domain.ExecutionResult
	leaseUntil time.Time
	cancel     context.CancelFunc
}

type Server struct {
	config  Config
	factory BackendFactory
	store   *SuccessStore
	mu      sync.Mutex
	tasks   map[string]*task
	active  string
	now     func() time.Time
}

func NewServer(config Config, factory BackendFactory) (*Server, error) {
	if err := config.Normalize(); err != nil {
		return nil, err
	}
	store, err := OpenSuccessStore(filepath.Join(config.DataDir, "success-orders.jsonl"))
	if err != nil {
		return nil, err
	}
	if factory == nil {
		factory = func(spec domain.ExecutionSpec) (executor.Backend, error) {
			return executor.NewBilibiliBackend(spec.Credentials)
		}
	}
	s := &Server{config: config, factory: factory, store: store, tasks: make(map[string]*task), now: time.Now}
	for id, result := range store.All() {
		s.tasks[id] = &task{spec: domain.ExecutionSpec{AttemptID: id, IntentID: result.IntentID}, specHash: result.SpecHash, state: domain.AttemptSucceeded, result: result}
	}
	go s.reapLeases()
	return s, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", s.health)
	mux.HandleFunc("POST /v1/tasks", s.create)
	mux.HandleFunc("GET /v1/tasks/{attemptId}", s.status)
	mux.HandleFunc("POST /v1/tasks/{attemptId}/stop", s.stop)
	mux.HandleFunc("POST /v1/tasks/{attemptId}/ack", s.ack)
	return s.authenticate(mux)
}

func (s *Server) ListenAndServe() error { return http.ListenAndServe(s.config.Listen, s.Handler()) }

func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if len(provided) != len(s.config.BearerKey) || subtle.ConstantTimeCompare([]byte(provided), []byte(s.config.BearerKey)) != 1 {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	active := s.active
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"workerId": s.config.WorkerID, "version": s.config.Version, "pluginVersion": s.config.PluginVersion, "algorithmVersion": s.config.AlgorithmVersion, "captchaPlugin": s.config.CaptchaPlugin, "clockCalibration": s.config.CalibrateClock, "activeAttemptId": active})
}

func (s *Server) create(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	var spec domain.ExecutionSpec
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&spec); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := spec.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	hash := spec.Hash()
	s.mu.Lock()
	if existing, ok := s.tasks[spec.AttemptID]; ok {
		if existing.specHash != hash {
			s.mu.Unlock()
			writeError(w, http.StatusConflict, "attemptId already exists with different spec")
			return
		}
		response := s.snapshot(existing)
		s.mu.Unlock()
		writeJSON(w, http.StatusOK, response)
		return
	}
	if s.active != "" {
		s.mu.Unlock()
		writeError(w, http.StatusConflict, "worker is busy")
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	t := &task{spec: spec, specHash: hash, state: domain.AttemptWaiting, leaseUntil: s.now().Add(s.config.LeaseDuration), cancel: cancel}
	s.tasks[spec.AttemptID], s.active = t, spec.AttemptID
	response := s.snapshot(t)
	s.mu.Unlock()
	go s.run(ctx, t)
	_ = WriteRedactedLog(s.config.DataDir, fmt.Sprintf("accepted attempt=%s intent=%s", spec.AttemptID, spec.IntentID))
	writeJSON(w, http.StatusAccepted, response)
}

func (s *Server) run(ctx context.Context, t *task) {
	backend, err := s.factory(t.spec)
	if err != nil {
		s.complete(t, domain.ExecutionResult{AttemptID: t.spec.AttemptID, IntentID: t.spec.IntentID, State: domain.AttemptFailed, Reason: domain.FailureInternal, Message: err.Error(), FinishedAt: s.now()})
		return
	}
	s.mu.Lock()
	if t.state == domain.AttemptWaiting {
		t.state = domain.AttemptRunning
	}
	s.mu.Unlock()
	_ = WriteRedactedLog(s.config.DataDir, fmt.Sprintf("started attempt=%s mode=%s deadline=%s", t.spec.AttemptID, t.spec.StartMode, t.spec.Deadline.Format(time.RFC3339)))
	var executionClock executor.Clock
	if s.config.CalibrateClock {
		if offset, err := biliclock.GetBilibiliClockOffset(); err == nil {
			executionClock = executor.OffsetClock{Offset: offset}
		} else {
			_ = WriteRedactedLog(s.config.DataDir, "clock calibration failed: "+err.Error())
		}
	}
	result := (executor.Engine{Backend: backend, Clock: executionClock}).Run(ctx, t.spec)
	if result.Success {
		if err := s.store.Append(result); err != nil {
			result.Success, result.State, result.Reason, result.Message = false, domain.AttemptFailed, domain.FailureInternal, "persist success: "+err.Error()
		}
	}
	s.complete(t, result)
}

func (s *Server) complete(t *task, result domain.ExecutionResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t.result, t.state = result, result.State
	if s.active == t.spec.AttemptID {
		s.active = ""
	}
	_ = WriteRedactedLog(s.config.DataDir, fmt.Sprintf("completed attempt=%s state=%s reason=%s order=%s", t.spec.AttemptID, result.State, result.Reason, result.OrderID))
}

type Status struct {
	AttemptID  string                 `json:"attemptId"`
	SpecHash   string                 `json:"specHash"`
	State      domain.AttemptState    `json:"state"`
	LeaseUntil time.Time              `json:"leaseUntil,omitempty"`
	Result     domain.ExecutionResult `json:"result"`
}

func (s *Server) snapshot(t *task) Status {
	return Status{AttemptID: t.spec.AttemptID, SpecHash: t.specHash, State: t.state, LeaseUntil: t.leaseUntil, Result: t.result}
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	t, ok := s.tasks[r.PathValue("attemptId")]
	if !ok {
		s.mu.Unlock()
		writeError(w, http.StatusNotFound, "attempt not found")
		return
	}
	if !t.state.Terminal() {
		t.leaseUntil = s.now().Add(s.config.LeaseDuration)
	}
	response := s.snapshot(t)
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) stop(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	t, ok := s.tasks[r.PathValue("attemptId")]
	if !ok {
		s.mu.Unlock()
		writeError(w, http.StatusNotFound, "attempt not found")
		return
	}
	if !t.state.Terminal() {
		t.state = domain.AttemptStopping
		t.cancel()
	}
	response := s.snapshot(t)
	s.mu.Unlock()
	writeJSON(w, http.StatusAccepted, response)
}

func (s *Server) ack(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("attemptId")
	s.mu.Lock()
	t, ok := s.tasks[id]
	if !ok {
		s.mu.Unlock()
		writeError(w, http.StatusNotFound, "attempt not found")
		return
	}
	if !t.state.Terminal() {
		s.mu.Unlock()
		writeError(w, http.StatusConflict, "attempt is not terminal")
		return
	}
	if !t.result.Success {
		delete(s.tasks, id)
	}
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) reapLeases() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for range ticker.C {
		s.mu.Lock()
		if t := s.tasks[s.active]; t != nil && !t.state.Terminal() && !t.leaseUntil.After(s.now()) {
			t.state = domain.AttemptStopping
			t.cancel()
		}
		s.mu.Unlock()
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func WriteRedactedLog(dataDir, line string) error {
	for _, marker := range []string{"SESSDATA", "bili_jct", "refresh_token", "refreshToken"} {
		if i := strings.Index(line, marker); i >= 0 {
			line = line[:i] + marker + "=[REDACTED]"
		}
	}
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return err
	}
	path := filepath.Join(dataDir, "worker.log")
	if info, err := os.Stat(path); err == nil && info.Size() >= 5<<20 {
		for i := 2; i >= 0; i-- {
			from := path
			if i > 0 {
				from = fmt.Sprintf("%s.%d", path, i)
			}
			to := fmt.Sprintf("%s.%d", path, i+1)
			_ = os.Rename(from, to)
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintln(f, line)
	return err
}
