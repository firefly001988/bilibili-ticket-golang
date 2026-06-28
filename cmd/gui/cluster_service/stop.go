package cluster_service

import (
	"context"
	"fmt"
	"log"
	"time"

	"bilibili-ticket-golang/cluster/domain"
)

// StopAttempt stops a running attempt by telling its worker to cancel it.
func (s *ClusterService) StopAttempt(attemptID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	workerList, err := s.repository.ListWorkers(ctx)
	if err != nil {
		return err
	}
	workerByID := make(map[string]domain.WorkerNode, len(workerList))
	for _, w := range workerList {
		workerByID[w.ID] = w
	}
	for _, a := range s.dispatcher.Attempts() {
		if a.ID != attemptID {
			continue
		}
		if a.State.Terminal() {
			return fmt.Errorf("attempt %s is already terminal (%s)", attemptID, a.State)
		}
		worker, ok := workerByID[a.WorkerID]
		if !ok {
			return fmt.Errorf("worker %s not found for attempt %s", a.WorkerID, attemptID)
		}
		return s.client.Stop(ctx, worker, attemptID)
	}
	return fmt.Errorf("attempt %s not found", attemptID)
}

// StopMacro is intentionally disabled: ticket dispatch is task-group scoped.
func (s *ClusterService) StopMacro(macroID string) error {
	return fmt.Errorf("macro task cannot be stopped independently; stop the task group instead")
}

// StopTaskGroup stops all active attempts belonging to a task group, disarms
// its intents, and releases the workers reserved by the task group.
func (s *ClusterService) StopTaskGroup(taskGroupID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	activeTaskGroupID := s.dispatcher.ActiveTaskGroup()
	if activeTaskGroupID != "" && activeTaskGroupID != taskGroupID {
		return fmt.Errorf("task group %s is not active; active task group is %s", taskGroupID, activeTaskGroupID)
	}
	return s.stopTaskGroupInternal(ctx, taskGroupID)
}

// ForceStopTaskGroup stops a task group unconditionally — even if another
// task group is marked as active.  It also force-resets all macros so that
// the group can be re-run after a successful order.
func (s *ClusterService) ForceStopTaskGroup(taskGroupID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	macros, err := s.repository.ListMacroTasks(ctx)
	if err != nil {
		return err
	}
	for _, macro := range macros {
		if macro.TaskGroupID != taskGroupID {
			continue
		}
		if resetErr := s.repository.ForceResetMacroExecution(ctx, macro.ID); resetErr != nil {
			return fmt.Errorf("force reset macro %s: %w", macro.ID, resetErr)
		}
	}
	return s.stopTaskGroupInternal(ctx, taskGroupID)
}

// ForceRestartTaskGroup force-stops a task group, resets all macros, and
// immediately re-plans and starts the task group with the given workers.
func (s *ClusterService) ForceRestartTaskGroup(taskGroupID string, workerIDsJSON string) error {
	if err := s.ForceStopTaskGroup(taskGroupID); err != nil {
		return fmt.Errorf("force stop: %w", err)
	}
	return s.StartTaskGroup(taskGroupID, workerIDsJSON)
}

func (s *ClusterService) stopTaskGroupInternal(ctx context.Context, taskGroupID string) error {
	workerList, err := s.repository.ListWorkers(ctx)
	if err != nil {
		return err
	}
	macros, err := s.repository.ListMacroTasks(ctx)
	if err != nil {
		return err
	}
	macroIDs := make(map[string]struct{})
	for _, macro := range macros {
		if macro.TaskGroupID == taskGroupID {
			macroIDs[macro.ID] = struct{}{}
		}
	}
	if len(macroIDs) == 0 {
		return fmt.Errorf("task group has no macro tasks")
	}
	workerByID := make(map[string]domain.WorkerNode, len(workerList))
	for _, w := range workerList {
		workerByID[w.ID] = w
	}
	// Send stop to all active worker attempts.
	for macroID := range macroIDs {
		for _, a := range s.dispatcher.MacroAttempts(macroID) {
			if a.State.Terminal() {
				continue
			}
			worker, ok := workerByID[a.WorkerID]
			if !ok {
				continue
			}
			_ = s.client.Stop(ctx, worker, a.ID)
		}
		s.dispatcher.DisarmMacro(macroID)
	}
	if activeTG := s.dispatcher.ActiveTaskGroup(); activeTG == taskGroupID {
		s.dispatcher.ReleaseWorkers()
		log.Printf("[cluster] released worker reservations for task group %s", taskGroupID)
	}

	return nil
}
