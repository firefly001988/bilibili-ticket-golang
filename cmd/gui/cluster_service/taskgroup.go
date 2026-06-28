package cluster_service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"bilibili-ticket-golang/cluster/domain"
)

// SaveTaskGroup persists a task group (create or update).
func (s *ClusterService) SaveTaskGroup(document string) error {
	var value domain.TaskGroup
	if err := json.Unmarshal([]byte(document), &value); err != nil {
		return err
	}
	if value.ID == "" {
		value.ID = randomClusterID("group")
	}
	if value.Name == "" {
		return fmt.Errorf("task group name is required")
	}
	if value.CreatedAt.IsZero() {
		value.CreatedAt = time.Now()
	}
	if err := s.repository.PutTaskGroup(context.Background(), value); err != nil {
		return err
	}
	return s.refreshResources(context.Background())
}

// DeleteTaskGroup removes a task group by ID.
func (s *ClusterService) DeleteTaskGroup(id string) error {
	if err := s.repository.DeleteTaskGroup(context.Background(), id); err != nil {
		return err
	}
	return s.refreshResources(context.Background())
}
