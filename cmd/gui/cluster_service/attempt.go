package cluster_service

import (
	"context"
	"fmt"
	"time"

	"bilibili-ticket-golang/cluster/domain"
	clusterworker "bilibili-ticket-golang/cluster/worker"
)

// AttemptLogs retrieves log entries for a specific execution attempt.
// It first reads cached logs from the local JSONL file (up to the last
// maxCachedLines entries), then fetches live logs from the worker.
// New entries are appended to the local cache so that logs survive
// worker restarts.
func (s *ClusterService) AttemptLogs(attemptID string) ([]clusterworker.LogEntry, error) {
	// Read locally cached logs first (last 100 lines).
	cached, _ := readCachedLogs(attemptID)

	var selected *domain.ExecutionAttempt
	for _, attempt := range s.dispatcher.Attempts() {
		if attempt.ID == attemptID {
			copy := attempt
			selected = &copy
			break
		}
	}
	if selected == nil {
		// Attempt not in memory — return cached logs if available.
		if len(cached) > 0 {
			return cached, nil
		}
		return nil, fmt.Errorf("attempt not found")
	}
	workers, err := s.repository.ListWorkers(context.Background())
	if err != nil {
		// Return cached logs on worker list error.
		if len(cached) > 0 {
			return cached, nil
		}
		return nil, err
	}
	for _, node := range workers {
		if node.ID == selected.WorkerID {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			live, liveErr := s.client.Logs(ctx, node, attemptID)
			if liveErr != nil {
				// Worker unreachable — return cached logs if available.
				if len(cached) > 0 {
					return cached, nil
				}
				return nil, liveErr
			}
			merged, newEntries := mergeLogs(cached, live)
			if len(newEntries) > 0 {
				_ = appendCachedLogs(attemptID, newEntries)
			}
			return merged, nil
		}
	}
	// Worker not found — return cached logs if available.
	if len(cached) > 0 {
		return cached, nil
	}
	return nil, fmt.Errorf("worker %s not found", selected.WorkerID)
}

// DeleteTerminalAttempts removes terminal (succeeded/failed/stopped) attempts
// from the database and cleans up their local log cache files.
// Running or queued attempts are silently kept.
func (s *ClusterService) DeleteTerminalAttempts(attemptIDs []string) error {
	if err := s.repository.DeleteAttempts(context.Background(), attemptIDs); err != nil {
		return err
	}
	removed := s.dispatcher.RemoveTerminalAttempts(attemptIDs)
	for _, id := range attemptIDs {
		_ = removeCachedLogs(id)
	}
	if len(removed) > 0 {
		s.RecordDispatchInfo("attempt-cleanup", fmt.Sprintf("removed %d terminal attempts from scheduler memory", len(removed)))
	}
	return nil
}
