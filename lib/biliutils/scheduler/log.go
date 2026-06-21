package scheduler

import (
	"bilibili-ticket-golang/lib/global"
	"context"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// LogLevel represents the severity level of a log entry.
type LogLevel string

const (
	LogDebug   LogLevel = "debug"
	LogInfo    LogLevel = "info"
	LogWarn    LogLevel = "warn"
	LogError   LogLevel = "error"
	LogSuccess LogLevel = "success"
)

// LogEntry is a single structured log line emitted by a task.
type LogEntry struct {
	TaskID    string    `json:"taskID"`
	Level     LogLevel  `json:"level"`
	Message   string    `json:"message"`
	Timestamp time.Time `json:"timestamp"`
}

// ringBuffer is a fixed-capacity circular buffer for LogEntry.
type ringBuffer struct {
	buf      []LogEntry
	head     int
	size     int
	capacity int
	mu       sync.RWMutex
}

func newRingBuffer(cap int) *ringBuffer {
	return &ringBuffer{
		buf:      make([]LogEntry, cap),
		capacity: cap,
	}
}

// push adds an entry to the ring buffer, overwriting the oldest if full.
func (rb *ringBuffer) push(e LogEntry) {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	rb.buf[rb.head] = e
	rb.head = (rb.head + 1) % rb.capacity
	if rb.size < rb.capacity {
		rb.size++
	}
}

// snapshot returns all entries in insertion order (oldest first).
func (rb *ringBuffer) snapshot() []LogEntry {
	rb.mu.RLock()
	defer rb.mu.RUnlock()
	out := make([]LogEntry, rb.size)
	if rb.size == 0 {
		return out
	}
	start := (rb.head - rb.size + rb.capacity) % rb.capacity
	for i := 0; i < rb.size; i++ {
		out[i] = rb.buf[(start+i)%rb.capacity]
	}
	return out
}

// clear removes all entries.
func (rb *ringBuffer) clear() {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	rb.head = 0
	rb.size = 0
}

// LogBroker manages per-task log rings and pushes real-time entries to the
// frontend via Wails runtime events. It is exposed as a Wails binding so the
// frontend can call GetHistory / ClearHistory directly.
// Logs are persisted to disk via LogStorage so they survive application restarts.
type LogBroker struct {
	ctx     context.Context
	mu      sync.RWMutex
	rings   map[string]*ringBuffer
	streams map[string]chan LogEntry
	maxCap  int
	storage *LogStorage
}

// NewLogBroker creates a LogBroker with a default per-task capacity of 1000 entries.
// storage is used to persist logs across restarts; call Load() on it beforehand.
func NewLogBroker(storage *LogStorage) *LogBroker {
	return &LogBroker{
		rings:   make(map[string]*ringBuffer),
		streams: make(map[string]chan LogEntry),
		maxCap:  global.DefaultRingCapacity,
		storage: storage,
	}
}

// SetContext must be called once during application startup (OnStartup).
// It stores the Wails context required for runtime.EventsEmit.
func (lb *LogBroker) SetContext(ctx context.Context) {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	lb.ctx = ctx
}

// CreateStream returns a send-only channel that the task uses to emit logs.
// A background goroutine reads from this channel, stores entries in the ring
// buffer, and fires frontend events. The caller may close the returned channel
// to clean up the forwarding goroutine, or call CloseStream(taskID) to do so
// from the outside.
func (lb *LogBroker) CreateStream(taskID string) chan<- LogEntry {
	lb.mu.Lock()
	if _, exists := lb.rings[taskID]; !exists {
		lb.rings[taskID] = newRingBuffer(lb.maxCap)
	}
	ch, exists := lb.streams[taskID]
	if !exists {
		ch = make(chan LogEntry, 64)
		lb.streams[taskID] = ch
		go lb.forward(taskID, ch)
	}
	lb.mu.Unlock()
	return ch
}

// CloseStream closes the forwarding channel for the given task and removes
// the in-memory ring buffer. Persisted logs on disk are preserved. Safe to
// call multiple times.
func (lb *LogBroker) CloseStream(taskID string) {
	lb.mu.Lock()
	ch, ok := lb.streams[taskID]
	if ok {
		delete(lb.streams, taskID)
	}
	lb.mu.Unlock()
	if ok {
		defer func() { _ = recover() }()
		close(ch)
	}
	lb.mu.Lock()
	delete(lb.rings, taskID)
	lb.mu.Unlock()
}

// forward reads from ch until it is closed, ingesting entries.
func (lb *LogBroker) forward(taskID string, ch <-chan LogEntry) {
	for entry := range ch {
		lb.ingest(taskID, entry)
	}
}

// ingest stores an entry in the ring buffer, persists it to disk (debounced),
// and emits it via Wails runtime events.
func (lb *LogBroker) ingest(taskID string, entry LogEntry) {
	// 1. Store in ring buffer
	lb.mu.RLock()
	ring, ok := lb.rings[taskID]
	lb.mu.RUnlock()
	if ok {
		ring.push(entry)
	}

	// 2. Persist to disk (debounced)
	lb.storage.Append(taskID, entry)

	// 3. Emit to frontend (non-blocking via EventsEmit, which is inherently async)
	lb.mu.RLock()
	ctx := lb.ctx
	lb.mu.RUnlock()
	if ctx != nil {
		runtime.EventsEmit(ctx, "ticket:log", entry)
	}
}

// PutLog is exported as a Wails binding so the Go backend can also push logs
// programmatically. Usually tasks will use the channel from CreateStream instead.
func (lb *LogBroker) PutLog(taskID string, level LogLevel, message string) {
	entry := LogEntry{
		TaskID:    taskID,
		Level:     level,
		Message:   message,
		Timestamp: time.Now(),
	}
	lb.ingest(taskID, entry)
}

// GetHistory returns all log entries for a task from persistent storage,
// falling back to the in-memory ring buffer if nothing has been persisted yet.
// This is a Wails binding called by the frontend when entering a task view.
func (lb *LogBroker) GetHistory(taskID string) []LogEntry {
	// Flush any pending writes to ensure the latest entries are on disk
	lb.storage.Flush()

	entries := lb.storage.GetEntries(taskID)
	if len(entries) > 0 {
		return entries
	}

	// Fall back to in-memory ring buffer (e.g. task just created, not yet persisted)
	lb.mu.RLock()
	ring, ok := lb.rings[taskID]
	lb.mu.RUnlock()
	if !ok {
		return nil
	}
	return ring.snapshot()
}

// GetRecentLogs returns the most recent log entry for every known task.
// Useful for a dashboard overview.
func (lb *LogBroker) GetRecentLogs() map[string]LogEntry {
	lb.mu.RLock()
	defer lb.mu.RUnlock()

	out := make(map[string]LogEntry, len(lb.rings))
	for taskID, ring := range lb.rings {
		snapshot := ring.snapshot()
		if len(snapshot) > 0 {
			out[taskID] = snapshot[len(snapshot)-1]
		}
	}
	return out
}

// ClearHistory clears both the in-memory ring buffer and the persisted logs
// for a specific task.
func (lb *LogBroker) ClearHistory(taskID string) {
	lb.mu.RLock()
	ring, ok := lb.rings[taskID]
	lb.mu.RUnlock()
	if ok {
		ring.clear()
	}
	lb.storage.Clear(taskID)
}

// GetPersistedTaskIDs returns the IDs of all tasks that have logs persisted on disk.
// This is a Wails binding useful for showing historical task log availability.
func (lb *LogBroker) GetPersistedTaskIDs() []string {
	return lb.storage.GetAllTaskIDs()
}

// FlushLogs immediately persists all pending log writes to disk.
// Should be called on application shutdown.
func (lb *LogBroker) FlushLogs() {
	lb.storage.Flush()
}
