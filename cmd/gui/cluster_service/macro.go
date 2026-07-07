package cluster_service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"time"

	"bilibili-ticket-golang/cluster/dispatcher"
	"bilibili-ticket-golang/cluster/domain"
	"bilibili-ticket-golang/cluster/planner"
	"bilibili-ticket-golang/lib/global"
)

const startTaskGroupReflowNowToken = "__cluster_reflow_now__"

type flexibleInt int

func (v *flexibleInt) UnmarshalJSON(data []byte) error {
	raw := strings.TrimSpace(string(data))
	if raw == "" || raw == "null" {
		*v = 0
		return nil
	}
	if unquoted, err := strconv.Unquote(raw); err == nil {
		raw = strings.TrimSpace(unquoted)
		if raw == "" {
			*v = 0
			return nil
		}
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid integer %q", raw)
	}
	*v = flexibleInt(n)
	return nil
}

type purchaseGroupDocument struct {
	ID          string         `json:"id"`
	MacroTaskID string         `json:"macroTaskId"`
	Buyers      []domain.Buyer `json:"buyers"`
	AllowSplit  bool           `json:"allowSplit"`
	Weight      flexibleInt    `json:"weight"`
	Priority    flexibleInt    `json:"priority"`
	CreatedAt   time.Time      `json:"createdAt"`
}

func (d purchaseGroupDocument) domainValue() domain.PurchaseGroup {
	return domain.PurchaseGroup{
		ID:          d.ID,
		MacroTaskID: d.MacroTaskID,
		Buyers:      d.Buyers,
		AllowSplit:  d.AllowSplit,
		Weight:      int(d.Weight),
		Priority:    int(d.Priority),
		CreatedAt:   d.CreatedAt,
	}
}

// SaveMacro persists a macro task (create or update). Validates the
// execution window and capacity constraints.
func (s *ClusterService) SaveMacro(document string) error {
	var value domain.MacroTask
	if err := json.Unmarshal([]byte(document), &value); err != nil {
		return err
	}
	ctx := context.Background()
	if value.ID == "" {
		value.ID = randomClusterID("macro")
	}
	if value.TaskGroupID == "" {
		return fmt.Errorf("taskGroupId is required")
	}
	if s.taskGroupActive(ctx, value.TaskGroupID) {
		return fmt.Errorf("macro task cannot be edited while its task group is running")
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
	return s.repository.PutMacroTask(ctx, value)
}

// DeleteMacro removes a macro task and cascades to intents/attempts.
func (s *ClusterService) DeleteMacro(macroID string) error {
	ctx := context.Background()
	macros, err := s.repository.ListMacroTasks(ctx)
	if err != nil {
		return err
	}
	for _, macro := range macros {
		if macro.ID == macroID && s.taskGroupActive(ctx, macro.TaskGroupID) {
			return fmt.Errorf("macro task cannot be deleted while its task group is running")
		}
	}
	if s.dispatcher.MacroActive(macroID) {
		return fmt.Errorf("macro task cannot be deleted while an attempt is active")
	}
	if err := s.repository.DeleteMacroTask(ctx, macroID); err != nil {
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
	var input purchaseGroupDocument
	if err := json.Unmarshal([]byte(document), &input); err != nil {
		return err
	}
	value := input.domainValue()
	ctx := context.Background()
	if value.MacroTaskID == "" {
		return fmt.Errorf("macroTaskId is required")
	}
	if value.ID == "" {
		value.ID = randomClusterID("purchase")
	} else if existing, existingErr := s.repository.PurchaseGroup(ctx, value.ID); existingErr == nil {
		if existing.MacroTaskID != value.MacroTaskID {
			return fmt.Errorf("purchase group belongs to another macro task")
		}
		if value.CreatedAt.IsZero() {
			value.CreatedAt = existing.CreatedAt
		}
	} else if !errors.Is(existingErr, sql.ErrNoRows) {
		return existingErr
	}
	macros, err := s.repository.ListMacroTasks(ctx)
	if err != nil {
		return err
	}
	capacity := 0
	taskGroupID := ""
	for _, macro := range macros {
		if macro.ID == value.MacroTaskID {
			capacity = macro.EffectiveCapacity()
			taskGroupID = macro.TaskGroupID
			break
		}
	}
	if capacity == 0 {
		return fmt.Errorf("macro task not found")
	}
	if s.taskGroupActive(ctx, taskGroupID) {
		return fmt.Errorf("purchase group cannot be edited while its task group is running")
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
	return s.repository.PutPurchaseGroup(ctx, value)
}

// DeletePurchaseGroup removes a purchase group from a macro.
func (s *ClusterService) DeletePurchaseGroup(macroID, purchaseGroupID string) error {
	ctx := context.Background()
	if strings.TrimSpace(macroID) == "" || strings.TrimSpace(purchaseGroupID) == "" {
		return fmt.Errorf("macroId and purchaseGroupId are required")
	}
	group, err := s.repository.PurchaseGroup(ctx, purchaseGroupID)
	if err != nil {
		return err
	}
	if group.MacroTaskID != macroID {
		return fmt.Errorf("purchase group belongs to another macro task")
	}
	macros, err := s.repository.ListMacroTasks(ctx)
	if err != nil {
		return err
	}
	for _, macro := range macros {
		if macro.ID == macroID && s.taskGroupActive(ctx, macro.TaskGroupID) {
			return fmt.Errorf("purchase group cannot be deleted while its task group is running")
		}
	}
	if err := s.invalidateMacroConfiguration(macroID); err != nil {
		return err
	}
	return s.repository.DeletePurchaseGroup(ctx, purchaseGroupID, macroID)
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

// StartTaskGroup plans and dispatches all macros in a task group. The normal
// path starts the punctual phase and lets the task-group wave controller manage
// later reflow waves. Passing startTaskGroupReflowNowToken is an internal
// compatibility path used by the frontend to start the reflow phase
// immediately without adding a new Wails binding.
func (s *ClusterService) StartTaskGroup(taskGroupID string, workerIDsJSON string) error {
	if workerIDsJSON == startTaskGroupReflowNowToken {
		return s.startTaskGroupPhase(taskGroupID, domain.PhaseReflow, true, "")
	}
	return s.startTaskGroupPhase(taskGroupID, domain.PhasePunctual, false, workerIDsJSON)
}

// startTaskGroupPhase plans and dispatches all macros in a task group using
// the task group's reserved primary/standby worker pools. workerIDsJSON is kept
// only for backwards compatibility with old frontend calls.
func (s *ClusterService) startTaskGroupPhase(taskGroupID string, phase domain.Phase, reflowNow bool, workerIDsJSON string) error {
	ctx := context.Background()
	if err := s.refreshResources(ctx); err != nil {
		return err
	}
	if activeTaskGroup := s.dispatcher.ActiveTaskGroup(); activeTaskGroup != "" {
		if activeTaskGroup == taskGroupID {
			return fmt.Errorf("task group %s is already running; stop it before starting again", taskGroupID)
		}
		return fmt.Errorf("task group %s is already running; stop it before starting task group %s", activeTaskGroup, taskGroupID)
	}
	if s.taskGroupActive(ctx, taskGroupID) {
		return fmt.Errorf("task group %s is already running; stop it before starting again", taskGroupID)
	}
	taskGroup, err := s.taskGroupByID(ctx, taskGroupID)
	if err != nil {
		return err
	}
	normalizeTaskGroupDefaults(&taskGroup)
	if reflowNow {
		if err := s.validateTaskGroupSaleStarted(ctx, taskGroupID); err != nil {
			return err
		}
	}

	accountIDs := uniqueStrings(taskGroup.AccountIDs)
	if len(accountIDs) == 0 {
		return fmt.Errorf("at least one task-group account must be selected")
	}
	primaryWorkerIDs := append([]string(nil), taskGroup.PrimaryWorkerIDs...)
	standbyWorkerIDs := append([]string(nil), taskGroup.StandbyWorkerIDs...)
	// Compatibility path: older frontend calls pass a single worker ID list.
	// Treat it as a primary-only one-shot override.
	if workerIDsJSON != "" {
		var workerIDs []string
		if err := json.Unmarshal([]byte(workerIDsJSON), &workerIDs); err != nil {
			return fmt.Errorf("parse worker IDs: %w", err)
		}
		if len(workerIDs) > 0 {
			primaryWorkerIDs = workerIDs
			standbyWorkerIDs = nil
		}
	}
	primaryWorkerIDs = uniqueStrings(primaryWorkerIDs)
	standbyWorkerIDs = removeStrings(uniqueStrings(standbyWorkerIDs), primaryWorkerIDs)
	if len(primaryWorkerIDs)+len(standbyWorkerIDs) == 0 {
		return fmt.Errorf("at least one task-group worker must be selected")
	}

	// Reserve accounts and workers before planning so Reconcile only sees
	// allowed resources.
	s.dispatcher.ReserveAccounts(taskGroupID, accountIDs)
	s.dispatcher.ReserveWorkerPools(taskGroupID, primaryWorkerIDs, standbyWorkerIDs)
	log.Printf("[cluster] reserved %d accounts, %d primary and %d standby workers for task group %s", len(accountIDs), len(primaryWorkerIDs), len(standbyWorkerIDs), taskGroupID)
	s.RecordDispatchInfo("reserve", fmt.Sprintf("task group %s reserved %d account(s), %d primary worker(s), %d standby worker(s)", taskGroupID, len(accountIDs), len(primaryWorkerIDs), len(standbyWorkerIDs)))

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
	if phase == domain.PhasePunctual && taskGroupMissedAllWaves(taskGroup, selected, time.Now()) {
		phase = domain.PhaseReflow
		reflowNow = true
		s.RecordDispatchInfo("plan", fmt.Sprintf("task group %s missed all configured waves; starting unbounded reflow", taskGroupID))
		log.Printf("[cluster] task group %s missed all configured waves; starting unbounded reflow", taskGroupID)
	}
	var failures []string
	started := 0
	plannedIntentIDs := make(map[string]struct{})
	for _, macro := range selected {
		intentIDs, err := s.planMacro(ctx, macro.ID, phase)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", macro.ID, err))
			s.RecordDispatchWarning("plan", fmt.Sprintf("macro %s plan failed: %v", macro.ID, err))
			continue
		}
		for id := range intentIDs {
			plannedIntentIDs[id] = struct{}{}
		}
		started++
		s.RecordDispatchInfo("plan", fmt.Sprintf("macro %s planned %d intent(s) for %s", macro.ID, len(intentIDs), phase))
	}
	if started == 0 {
		rollback("no macro task planned")
		if len(failures) > 0 {
			return global.NewFault("启动任务组 "+taskGroupID, fmt.Errorf("所有宏任务规划失败: %s", strings.Join(failures, "; ")), "请检查任务配置是否正确")
		}
		return global.NewFault("启动任务组 "+taskGroupID, fmt.Errorf("没有可启动的宏任务"), "请先添加 SKU 宏任务")
	}
	if len(failures) > 0 {
		rollback("partial plan failure")
		return global.NewFault("启动任务组 "+taskGroupID, fmt.Errorf("已启动 %d 个宏任务，部分失败: %s", started, strings.Join(failures, "; ")), "检查失败的宏任务配置")
	}
	if err := s.dispatcher.Reconcile(ctx); err != nil {
		rollback(err.Error())
		return global.NewFault("调度器协调", err, "检查 Worker 和账号状态")
	}
	if s.dispatcher.ActiveAttemptsFor(plannedIntentIDs) == 0 {
		diagnosis := s.diagnoseNoAttemptStart(ctx, taskGroup, selected, plannedIntentIDs)
		rollback("no active attempt after reconcile: " + diagnosis)
		return global.NewFault("调度任务组 "+taskGroup.ID, fmt.Errorf("已规划但无尝试启动: %s", diagnosis), "请根据诊断信息检查账号、Worker 和买家配置")
	}
	s.startTaskGroupWaveController(taskGroup, phase, reflowNow)
	return nil
}

func taskGroupMissedAllWaves(taskGroup domain.TaskGroup, macros []domain.MacroTask, now time.Time) bool {
	saleStart, err := taskGroupSaleStart(macros)
	if err != nil {
		return false
	}
	paymentTimeout, waveDuration, maxWaves := taskGroupWaveSettings(taskGroup)
	return firstPendingTaskGroupWave(saleStart, now, paymentTimeout, waveDuration, maxWaves) > maxWaves
}

func (s *ClusterService) diagnoseNoAttemptStart(ctx context.Context, taskGroup domain.TaskGroup, macros []domain.MacroTask, plannedIntentIDs map[string]struct{}) string {
	reasons := make([]string, 0)
	now := time.Now()

	accounts, err := s.repository.ListAccounts(ctx)
	if err != nil {
		reasons = append(reasons, "failed to list accounts: "+err.Error())
	} else {
		selectedAccounts := make(map[string]domain.Account)
		for _, id := range taskGroup.AccountIDs {
			for _, account := range accounts {
				if account.ID == id {
					selectedAccounts[id] = account
					break
				}
			}
		}
		if len(selectedAccounts) == 0 {
			reasons = append(reasons, fmt.Sprintf("任务组未选择任何账号（AccountIDs 为空，共 %d 个可用账号）", len(accounts)))
		} else {
			enabledCount := 0
			for id, account := range selectedAccounts {
				if !account.Enabled {
					reasons = append(reasons, fmt.Sprintf("账号 %s (%s): 已禁用", id, account.Name))
				} else if account.CooldownUntil.After(now) {
					remaining := account.CooldownUntil.Sub(now).Round(time.Second)
					reasons = append(reasons, fmt.Sprintf("账号 %s (%s): 冷却中 (剩余 %v)", id, account.Name, remaining))
				} else {
					enabledCount++
				}
			}
			if enabledCount == 0 {
				reasons = append(reasons, "所有选定账号均已禁用或冷却中")
			}
		}
	}

	workers, err := s.repository.ListWorkers(ctx)
	if err != nil {
		reasons = append(reasons, "failed to list workers: "+err.Error())
	} else {
		allowedWorkers := append([]string(nil), taskGroup.PrimaryWorkerIDs...)
		allowedWorkers = append(allowedWorkers, taskGroup.StandbyWorkerIDs...)
		allowedSet := make(map[string]bool, len(allowedWorkers))
		for _, id := range uniqueStrings(allowedWorkers) {
			allowedSet[id] = true
		}
		selectedCount := 0
		enabledCount := 0
		healthyCount := 0
		for _, worker := range workers {
			if !allowedSet[worker.ID] {
				continue
			}
			selectedCount++
			status := "健康"
			if !worker.Enabled {
				status = "已禁用"
			} else {
				enabledCount++
				isHealthy := s.client != nil && s.client.IsHealthy(worker.ID)
				isDisconnected := s.client != nil && s.client.IsDisconnected(worker.ID)
				if isDisconnected {
					status = "离线 (连接断开)"
				} else if !isHealthy {
					status = "不健康 (健康检查失败)"
				} else {
					healthyCount++
				}
			}
			if status != "健康" {
				reasons = append(reasons, fmt.Sprintf("worker %s (%s): %s", worker.ID, worker.Name, status))
			}
		}
		if selectedCount == 0 {
			reasons = append(reasons, fmt.Sprintf("任务组未选择任何 Worker（共 %d 个可用 Worker）", len(workers)))
		} else if enabledCount == 0 {
			reasons = append(reasons, "所有选定的 Worker 均已禁用")
		} else if healthyCount == 0 {
			reasons = append(reasons, "所有选定的已启用 Worker 均不健康——请检查 Worker 连接状态")
		}
	}

	intents, err := s.repository.ListIntents(ctx)
	if err != nil {
		reasons = append(reasons, "failed to list intents: "+err.Error())
	} else {
		macrosByID := make(map[string]domain.MacroTask, len(macros))
		for _, macro := range macros {
			macrosByID[macro.ID] = macro
		}
		accountIDs := uniqueStrings(taskGroup.AccountIDs)
		missingBuyerReasons := make([]string, 0)
		deadlineCount := 0
		for _, intent := range intents {
			if _, ok := plannedIntentIDs[intent.ID]; !ok {
				continue
			}
			if macro, ok := macrosByID[intent.MacroTaskID]; ok && now.After(macro.Deadline) {
				deadlineCount++
			}
			for _, buyer := range intent.Buyers {
				if buyer.LogicalID == "" {
					missingBuyerReasons = append(missingBuyerReasons, "buyer without logicalId in intent "+intent.ID)
					continue
				}
				if _, err := s.repository.LogicalBuyer(ctx, buyer.LogicalID); err != nil {
					missingBuyerReasons = append(missingBuyerReasons, fmt.Sprintf("buyer %s is not usable: %v", buyer.LogicalID, err))
					continue
				}
				mapped := 0
				for _, accountID := range accountIDs {
					if _, err := s.repository.BuyerMapping(ctx, accountID, buyer.LogicalID); err == nil {
						mapped++
					}
				}
				if mapped == 0 {
					name := buyer.Name
					if name == "" {
						name = buyer.LogicalID
					}
					missingBuyerReasons = append(missingBuyerReasons, fmt.Sprintf("buyer %s has no mapping on selected accounts", name))
				}
			}
		}
		if deadlineCount > 0 {
			reasons = append(reasons, fmt.Sprintf("%d planned intent(s) are past deadline", deadlineCount))
		}
		if len(missingBuyerReasons) > 0 {
			if len(missingBuyerReasons) > 5 {
				missingBuyerReasons = append(missingBuyerReasons[:5], fmt.Sprintf("...and %d more buyer mapping issue(s)", len(missingBuyerReasons)-5))
			}
			reasons = append(reasons, strings.Join(missingBuyerReasons, "; "))
		}
	}

	if len(reasons) == 0 {
		reasons = append(reasons, "no eligible account/worker pair after dispatcher reconciliation; check worker health, selected account pool, cooldowns, and buyer mappings")
	}
	message := strings.Join(reasons, "; ")
	s.RecordDispatchWarning("diagnose", "task group "+taskGroup.ID+" planned but no attempt started: "+message)
	return message
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
	taskGroup, err := s.taskGroupByID(ctx, selected.TaskGroupID)
	if err != nil {
		return nil, err
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
		s.dispatcher.Add(dispatcher.IntentPlan{TaskGroup: taskGroup, Macro: *selected, Intent: intent})
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
	taskGroup, err := s.taskGroupByID(ctx, selected.TaskGroupID)
	if err != nil {
		return err
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
		s.dispatcher.Add(dispatcher.IntentPlan{TaskGroup: taskGroup, Macro: *selected, Intent: intent})
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
