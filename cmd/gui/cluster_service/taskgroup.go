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
	if value.ID != "" && s.taskGroupActive(context.Background(), value.ID) {
		return fmt.Errorf("task group cannot be edited while it is running")
	}
	normalizeTaskGroupDefaults(&value)
	if err := s.repository.PutTaskGroup(context.Background(), value); err != nil {
		return err
	}
	return s.refreshResources(context.Background())
}

func normalizeTaskGroupDefaults(value *domain.TaskGroup) {
	value.AccountIDs = uniqueStrings(value.AccountIDs)
	value.PrimaryWorkerIDs = uniqueStrings(value.PrimaryWorkerIDs)
	value.StandbyWorkerIDs = removeStrings(uniqueStrings(value.StandbyWorkerIDs), value.PrimaryWorkerIDs)
	if value.PaymentTimeoutMinutes <= 0 {
		value.PaymentTimeoutMinutes = 10
	}
	if value.WaveDurationMinutes <= 0 {
		value.WaveDurationMinutes = 3
	}
	if value.MaxWaves <= 0 {
		value.MaxWaves = 3
	}
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func removeStrings(values, forbidden []string) []string {
	forbiddenSet := make(map[string]struct{}, len(forbidden))
	for _, value := range forbidden {
		forbiddenSet[value] = struct{}{}
	}
	result := values[:0]
	for _, value := range values {
		if _, ok := forbiddenSet[value]; ok {
			continue
		}
		result = append(result, value)
	}
	return result
}

func (s *ClusterService) taskGroupByID(ctx context.Context, id string) (domain.TaskGroup, error) {
	groups, err := s.repository.ListTaskGroups(ctx)
	if err != nil {
		return domain.TaskGroup{}, err
	}
	for _, group := range groups {
		if group.ID == id {
			return group, nil
		}
	}
	return domain.TaskGroup{}, fmt.Errorf("task group %s not found", id)
}

func (s *ClusterService) taskGroupActive(ctx context.Context, taskGroupID string) bool {
	if active := s.dispatcher.ActiveTaskGroup(); active == taskGroupID {
		return true
	}
	macros, err := s.repository.ListMacroTasks(ctx)
	if err != nil {
		return false
	}
	macroIDs := make(map[string]struct{})
	for _, macro := range macros {
		if macro.TaskGroupID != taskGroupID {
			continue
		}
		macroIDs[macro.ID] = struct{}{}
		if s.dispatcher.MacroActive(macro.ID) {
			return true
		}
	}
	for _, plan := range s.dispatcher.Plans() {
		if _, ok := macroIDs[plan.Macro.ID]; ok && plan.Intent.Armed && !plan.Intent.Terminal {
			return true
		}
	}
	return false
}

// DeleteTaskGroup removes a task group by ID.
func (s *ClusterService) DeleteTaskGroup(id string) error {
	if s.taskGroupActive(context.Background(), id) {
		return fmt.Errorf("task group cannot be deleted while it is running")
	}
	if err := s.repository.DeleteTaskGroup(context.Background(), id); err != nil {
		return err
	}
	return s.refreshResources(context.Background())
}
