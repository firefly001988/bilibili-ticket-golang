package cluster_service

import (
	"context"
	"fmt"
	"time"

	"bilibili-ticket-golang/cluster/domain"
	clusterworker "bilibili-ticket-golang/cluster/worker"
)

// AttemptLogs retrieves log entries for a specific execution attempt from its worker.
func (s *ClusterService) AttemptLogs(attemptID string) ([]clusterworker.LogEntry, error) {
	var selected *domain.ExecutionAttempt
	for _, attempt := range s.dispatcher.Attempts() {
		if attempt.ID == attemptID {
			copy := attempt
			selected = &copy
			break
		}
	}
	if selected == nil {
		return nil, fmt.Errorf("attempt not found")
	}
	workers, err := s.repository.ListWorkers(context.Background())
	if err != nil {
		return nil, err
	}
	for _, node := range workers {
		if node.ID == selected.WorkerID {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			return s.client.Logs(ctx, node, attemptID)
		}
	}
	return nil, fmt.Errorf("worker %s not found", selected.WorkerID)
}
