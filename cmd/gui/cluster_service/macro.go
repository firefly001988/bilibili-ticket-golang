package cluster_service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"bilibili-ticket-golang/cluster/dispatcher"
	"bilibili-ticket-golang/cluster/domain"
	"bilibili-ticket-golang/cluster/planner"
)

// SaveMacro persists a macro task (create or update). Validates the
// execution window and capacity constraints.
func (s *ClusterService) SaveMacro(document string) error {
	var value domain.MacroTask
	if err := json.Unmarshal([]byte(document), &value); err != nil {
		return err
	}
	if value.ID == "" {
		value.ID = randomClusterID("macro")
	}
	if value.TaskGroupID == "" {
		return fmt.Errorf("taskGroupId is required")
	}
	if s.dispatcher.MacroActive(value.ID) {
		return fmt.Errorf("macro task cannot be edited while an attempt is active")
	}
	if value.OrderCapacity <= 0 {
		value.OrderCapacity = 4
		value.CapacitySource = domain.CapacityDefault
	}
	if value.Dispatchable() {
		if value.StartAt.IsZero() || value.Deadline.IsZero() || !value.Deadline.After(value.StartAt) {
			return fmt.Errorf("dispatchable macro requires a deadline after startAt")
		}
	}
	if err := s.invalidateMacroConfiguration(value.ID); err != nil {
		return err
	}
	return s.repository.PutMacroTask(context.Background(), value)
}

// DeleteMacro removes a macro task and cascades to intents/attempts.
func (s *ClusterService) DeleteMacro(macroID string) error {
	if s.dispatcher.MacroActive(macroID) {
		return fmt.Errorf("macro task cannot be deleted while an attempt is active")
	}
	if err := s.repository.DeleteMacroTask(context.Background(), macroID); err != nil {
		return err
	}
	if err := s.dispatcher.RemoveMacro(macroID); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.phases, macroID)
	s.mu.Unlock()
	return nil
}

// SavePurchaseGroup persists a purchase group (create or update).
func (s *ClusterService) SavePurchaseGroup(document string) error {
	var value domain.PurchaseGroup
	if err := json.Unmarshal([]byte(document), &value); err != nil {
		return err
	}
	if value.MacroTaskID == "" {
		return fmt.Errorf("macroTaskId is required")
	}
	if value.ID == "" {
		value.ID = randomClusterID("purchase")
	} else if existing, existingErr := s.repository.PurchaseGroup(context.Background(), value.ID); existingErr == nil {
		if existing.MacroTaskID != value.MacroTaskID {
			return fmt.Errorf("purchase group belongs to another macro task")
		}
		if value.CreatedAt.IsZero() {
			value.CreatedAt = existing.CreatedAt
		}
	} else if !errors.Is(existingErr, sql.ErrNoRows) {
		return existingErr
	}
	macros, err := s.repository.ListMacroTasks(context.Background())
	if err != nil {
		return err
	}
	capacity := 0
	for _, macro := range macros {
		if macro.ID == value.MacroTaskID {
			capacity = macro.EffectiveCapacity()
			break
		}
	}
	if capacity == 0 {
		return fmt.Errorf("macro task not found")
	}
	// Normalize Weight and Priority.
	if value.Weight <= 0 {
		value.Weight = 1
	}
	if len(value.Buyers) == 0 || len(value.Buyers) > capacity {
		return fmt.Errorf("buyer count must be between 1 and %d", capacity)
	}
	seen := make(map[string]struct{}, len(value.Buyers))
	for _, buyer := range value.Buyers {
		id := strings.TrimSpace(buyer.LogicalID)
		if id == "" {
			return fmt.Errorf("every buyer requires a logicalId")
		}
		if _, exists := seen[id]; exists {
			return fmt.Errorf("duplicate logical buyer %s", id)
		}
		seen[id] = struct{}{}
	}
	if value.CreatedAt.IsZero() {
		value.CreatedAt = time.Now()
	}
	if err := s.invalidateMacroConfiguration(value.MacroTaskID); err != nil {
		return err
	}
	return s.repository.PutPurchaseGroup(context.Background(), value)
}

// DeletePurchaseGroup removes a purchase group from a macro.
func (s *ClusterService) DeletePurchaseGroup(macroID, purchaseGroupID string) error {
	if strings.TrimSpace(macroID) == "" || strings.TrimSpace(purchaseGroupID) == "" {
		return fmt.Errorf("macroId and purchaseGroupId are required")
	}
	group, err := s.repository.PurchaseGroup(context.Background(), purchaseGroupID)
	if err != nil {
		return err
	}
	if group.MacroTaskID != macroID {
		return fmt.Errorf("purchase group belongs to another macro task")
	}
	if err := s.invalidateMacroConfiguration(macroID); err != nil {
		return err
	}
	return s.repository.DeletePurchaseGroup(context.Background(), purchaseGroupID, macroID)
}

func (s *ClusterService) invalidateMacroConfiguration(macroID string) error {
	if s.dispatcher.MacroActive(macroID) {
		return fmt.Errorf("macro task cannot be changed while an attempt is active")
	}
	if err := s.repository.ResetMacroExecution(context.Background(), macroID); err != nil {
		// If the normal reset is blocked by a successful order, force-reset.
		// Purchase group edits are orthogonal to the previous success and
		// should always be allowed when no attempt is active.
		return s.repository.ForceResetMacroExecution(context.Background(), macroID)
	}
	return s.dispatcher.RemoveMacro(macroID)
}

// StartMacro is intentionally disabled: ticket dispatch is task-group scoped.
func (s *ClusterService) StartMacro(macroID string) error {
	return fmt.Errorf("macro task cannot be started independently; start the task group instead")
}

// StartTaskGroup plans and dispatches all macros in a task group using the
// given workers. workerIDsJSON is a JSON array of worker ID strings.
func (s *ClusterService) StartTaskGroup(taskGroupID string, workerIDsJSON string) error {
	ctx := context.Background()
	if err := s.refreshResources(ctx); err != nil {
		return err
	}
	// Parse worker IDs.
	var workerIDs []string
	if workerIDsJSON != "" {
		if err := json.Unmarshal([]byte(workerIDsJSON), &workerIDs); err != nil {
			return fmt.Errorf("parse worker IDs: %w", err)
		}
	}
	if len(workerIDs) == 0 {
		return fmt.Errorf("at least one worker must be selected")
	}

	// Reserve workers before planning so Reconcile only sees allowed workers.
	s.dispatcher.ReserveWorkers(taskGroupID, workerIDs)
	log.Printf("[cluster] reserved %d workers for task group %s", len(workerIDs), taskGroupID)

	macros, err := s.repository.ListMacroTasks(ctx)
	if err != nil {
		s.dispatcher.ReleaseWorkers()
		return err
	}
	selected := make([]domain.MacroTask, 0)
	for _, macro := range macros {
		if macro.TaskGroupID == taskGroupID {
			selected = append(selected, macro)
		}
	}
	if len(selected) == 0 {
		s.dispatcher.ReleaseWorkers()
		return fmt.Errorf("task group has no macro tasks")
	}

	// rollback disarms macros that were planned and releases worker
	// reservations.  Must be called on any error path after ReserveWorkers.
	rollback := func(reason string) {
		for _, macro := range selected {
			s.dispatcher.DisarmMacro(macro.ID)
		}
		s.dispatcher.ReleaseWorkers()
		log.Printf("[cluster] start task group %s rolled back: %s", taskGroupID, reason)
	}
	sort.SliceStable(selected, func(i, j int) bool {
		if selected[i].Priority == selected[j].Priority {
			return selected[i].ID < selected[j].ID
		}
		return selected[i].Priority > selected[j].Priority
	})
	var failures []string
	started := 0
	plannedIntentIDs := make(map[string]struct{})
	for _, macro := range selected {
		intentIDs, err := s.planMacro(ctx, macro.ID, domain.PhasePunctual)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", macro.ID, err))
			continue
		}
		for id := range intentIDs {
			plannedIntentIDs[id] = struct{}{}
		}
		started++
	}
	if started == 0 {
		rollback("no macro task planned")
		if len(failures) > 0 {
			return fmt.Errorf("no macro task started: %s", strings.Join(failures, "; "))
		}
		return fmt.Errorf("no macro task started")
	}
	if len(failures) > 0 {
		rollback("partial plan failure")
		return fmt.Errorf("started %d macro task(s), but some failed: %s", started, strings.Join(failures, "; "))
	}
	if err := s.dispatcher.Reconcile(ctx); err != nil {
		rollback(err.Error())
		return err
	}
	if s.dispatcher.ActiveAttemptsFor(plannedIntentIDs) == 0 {
		rollback("no active attempt after reconcile")
		return fmt.Errorf("task group was planned but no attempt started: check deadline, healthy workers, enabled accounts, and buyer mappings")
	}
	return nil
}

func (s *ClusterService) planMacro(ctx context.Context, macroID string, phase domain.Phase) (map[string]struct{}, error) {
	s.dispatcher.ResumePhase(phase)
	macros, err := s.repository.ListMacroTasks(ctx)
	if err != nil {
		return nil, err
	}
	var selected *domain.MacroTask
	for i := range macros {
		if macros[i].ID == macroID {
			selected = &macros[i]
			break
		}
	}
	if selected == nil {
		return nil, fmt.Errorf("macro task not found")
	}
	groups, err := s.repository.ListPurchaseGroups(ctx, macroID)
	if err != nil {
		return nil, err
	}
	if len(groups) == 0 {
		return nil, fmt.Errorf("macro task has no purchase groups")
	}
	intents, err := planner.Plan(*selected, groups, phase, time.Now())
	if err != nil {
		return nil, err
	}
	if len(intents) == 0 {
		return nil, fmt.Errorf("planner produced no order intents")
	}
	existing, err := s.repository.ListIntents(ctx)
	if err != nil {
		return nil, err
	}
	existingByID := make(map[string]domain.LogicalOrderIntent, len(existing))
	for _, intent := range existing {
		existingByID[intent.ID] = intent
	}
	intentIDs := make(map[string]struct{}, len(intents))
	skipped := 0
	for _, intent := range intents {
		if previous, ok := existingByID[intent.ID]; ok && previous.Succeeded {
			// Already succeeded — skip, don't re-dispatch.
			skipped++
			continue
		}
		if err := s.repository.PutIntent(ctx, intent); err != nil {
			return nil, err
		}
		s.dispatcher.Add(dispatcher.IntentPlan{Macro: *selected, Intent: intent})
		intentIDs[intent.ID] = struct{}{}
	}
	if skipped > 0 && len(intentIDs) == 0 {
		return nil, fmt.Errorf("all %d intent(s) already succeeded", skipped)
	}
	s.mu.Lock()
	s.phases[macroID] = phase
	s.mu.Unlock()
	return intentIDs, nil
}

// RestartMacro force-resets a single macro and re-plans it.  Intents that
// already succeeded are skipped (not re-dispatched).  Callers should
// ensure workers are reserved before calling this.
func (s *ClusterService) RestartMacro(macroID string) error {
	ctx := context.Background()
	if err := s.repository.ForceResetMacroExecution(ctx, macroID); err != nil {
		return err
	}
	if err := s.dispatcher.RemoveMacro(macroID); err != nil {
		return err
	}
	_, err := s.planMacro(ctx, macroID, domain.PhasePunctual)
	if err != nil {
		return err
	}
	return s.dispatcher.Reconcile(ctx)
}

// StopIntent stops all active attempts belonging to a single intent.
func (s *ClusterService) StopIntent(intentID string) error {
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
		if a.IntentID != intentID || a.State.Terminal() {
			continue
		}
		worker, ok := workerByID[a.WorkerID]
		if !ok {
			continue
		}
		_ = s.client.Stop(ctx, worker, a.ID)
	}
	return nil
}

// StartPurchaseGroup plans and dispatches intents for a single purchase group
// within a macro.  Only this purchase group's intents are armed; other
// groups' intents remain untouched.  Already-succeeded intents from this
// group are overwritten (upsert) so they can be re-dispatched.
func (s *ClusterService) StartPurchaseGroup(macroID, purchaseGroupID string) error {
	ctx := context.Background()

	if s.dispatcher.MacroActive(macroID) {
		return fmt.Errorf("macro task is already active; stop it first")
	}

	macros, err := s.repository.ListMacroTasks(ctx)
	if err != nil {
		return err
	}
	var selected *domain.MacroTask
	for i := range macros {
		if macros[i].ID == macroID {
			selected = &macros[i]
			break
		}
	}
	if selected == nil {
		return fmt.Errorf("macro task not found")
	}

	group, err := s.repository.PurchaseGroup(ctx, purchaseGroupID)
	if err != nil {
		return fmt.Errorf("purchase group not found: %w", err)
	}
	if group.MacroTaskID != macroID {
		return fmt.Errorf("purchase group belongs to another macro")
	}

	s.dispatcher.ResumePhase(domain.PhasePunctual)
	intents, err := planner.PlanGroups(*selected, []domain.PurchaseGroup{group}, domain.PhasePunctual, time.Now())
	if err != nil {
		return err
	}
	if len(intents) == 0 {
		return fmt.Errorf("planner produced no order intents for purchase group %s", purchaseGroupID)
	}

	for _, intent := range intents {
		if err := s.repository.PutIntent(ctx, intent); err != nil {
			return err
		}
		s.dispatcher.Add(dispatcher.IntentPlan{Macro: *selected, Intent: intent})
	}

	s.mu.Lock()
	s.phases[macroID] = domain.PhasePunctual
	s.mu.Unlock()

	return s.dispatcher.Reconcile(ctx)
}

// SwitchToReflow transitions all punctual intents to the reflow phase,
// stopping active punctual attempts and re-planning with relaxed constraints.
func (s *ClusterService) SwitchToReflow() error {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	if err := s.dispatcher.SwitchToReflow(ctx); err != nil {
		return err
	}
	for !s.dispatcher.PunctualStopped() {
		if err := s.dispatcher.Reconcile(ctx); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	macros, err := s.repository.ListMacroTasks(ctx)
	if err != nil {
		return err
	}
	for _, macro := range macros {
		if _, err := s.planMacro(ctx, macro.ID, domain.PhaseReflow); err != nil {
			return err
		}
	}
	return s.dispatcher.Reconcile(ctx)
}
