package cluster_service

import (
	"time"

	"bilibili-ticket-golang/cluster/domain"
)

const maxEventLogSize = 5000

// recordEvent appends an event to the ring buffer, trimming old entries
// when the capacity is exceeded.
func (s *ClusterService) recordEvent(e ClusterEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	s.eventLog = append(s.eventLog, e)
	if len(s.eventLog) > maxEventLogSize {
		// Discard the oldest half to keep the buffer bounded.
		cut := len(s.eventLog) - maxEventLogSize/2
		s.eventLog = append([]ClusterEvent(nil), s.eventLog[cut:]...)
	}
}

// RecordWorkerConnected logs that a heartbeat stream was established.
func (s *ClusterService) RecordWorkerConnected(workerID, address, version string) {
	s.recordEvent(ClusterEvent{
		Kind:     EventWorkerConnected,
		WorkerID: workerID,
		Stage:    "heartbeat",
		Message:  "worker " + workerID + " (" + address + ") connected, version=" + version,
		Code:     0,
	})
}

// RecordWorkerDisconnected logs that a heartbeat stream was lost.
func (s *ClusterService) RecordWorkerDisconnected(workerID, reason string) {
	s.recordEvent(ClusterEvent{
		Kind:     EventWorkerDisconnected,
		WorkerID: workerID,
		Stage:    "heartbeat",
		Message:  "worker " + workerID + " disconnected: " + reason,
		Code:     0,
	})
}

// RecordWorkerHealth logs a health-check status change.
func (s *ClusterService) RecordWorkerHealth(workerID string, healthy bool, version, plugin string) {
	kind := EventWorkerHealthy
	msg := "worker " + workerID + " is healthy"
	if !healthy {
		kind = EventWorkerUnhealthy
		msg = "worker " + workerID + " is unhealthy"
	}
	s.recordEvent(ClusterEvent{
		Kind:     kind,
		WorkerID: workerID,
		Stage:    "health",
		Message:  msg + " (version=" + version + " plugin=" + plugin + ")",
		Code:     0,
	})
}

// RecordHeartbeatLatency logs an unusually high heartbeat round-trip.
func (s *ClusterService) RecordHeartbeatLatency(workerID string, latencyMs int64) {
	s.recordEvent(ClusterEvent{
		Kind:     EventHeartbeatLatency,
		WorkerID: workerID,
		Stage:    "heartbeat",
		Message:  "worker " + workerID + " heartbeat latency spike",
		Code:     int(latencyMs),
	})
}

// RecordTaskCompleted logs a task completion event pushed by a worker.
func (s *ClusterService) RecordTaskCompleted(workerID string, result domain.ExecutionResult) {
	kind := EventTaskCompleted
	stage := "complete"
	if !result.Success {
		kind = EventTaskFailed
		stage = "failed"
	}
	ev := ClusterEvent{
		Kind:      kind,
		WorkerID:  workerID,
		Stage:     stage,
		AttemptID: result.AttemptID,
		OrderID:   result.OrderID,
		Message:   "worker " + workerID + " reported task " + result.AttemptID,
		Code:      0,
		Retryable: result.Retryable,
	}
	if result.Message != "" {
		ev.Message += ": " + result.Message
	}
	s.recordEvent(ev)
}

// RecordWorkerInfo logs miscellaneous worker info (clock offsets etc.).
func (s *ClusterService) RecordWorkerInfo(workerID, msg string) {
	s.recordEvent(ClusterEvent{
		Kind:     EventWorkerInfo,
		WorkerID: workerID,
		Stage:    "info",
		Message:  "worker " + workerID + ": " + msg,
		Code:     0,
	})
}

// GetClusterEventLog returns all buffered events for the unified log page.
func (s *ClusterService) GetClusterEventLog() ClusterEventLog {
	s.mu.RLock()
	defer s.mu.RUnlock()
	// Return a copy to avoid races with background writes.
	events := make([]ClusterEvent, len(s.eventLog))
	copy(events, s.eventLog)
	return ClusterEventLog{Events: events}
}
