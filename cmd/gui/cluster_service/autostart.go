package cluster_service

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"time"

	"bilibili-ticket-golang/cluster/domain"
)

// autoStartReadyTaskGroups checks all task groups whose macros have a StartAt
// time that has already passed (or is within 30 seconds) and the Deadline has
// not yet elapsed.  Task groups that are already running are skipped.
// Workers are selected automatically: primary workers from each macro take
// precedence, then standby workers, then all enabled healthy workers.
func (s *ClusterService) autoStartReadyTaskGroups(ctx context.Context) {
	macros, err := s.repository.ListMacroTasks(ctx)
	if err != nil {
		log.Printf("[cluster] auto-start: list macros: %v", err)
		return
	}
	// Build task-group → macros mapping and check if any macro in a
	// task group is already active (has armed intents or active attempts).
	tgMacros := make(map[string][]domain.MacroTask)
	tgActive := make(map[string]bool)
	for _, macro := range macros {
		tgMacros[macro.TaskGroupID] = append(tgMacros[macro.TaskGroupID], macro)
		if s.dispatcher.MacroActive(macro.ID) {
			tgActive[macro.TaskGroupID] = true
		}
	}
	// Also consider a task group active if it holds worker reservations.
	activeTG := s.dispatcher.ActiveTaskGroup()
	if activeTG != "" {
		tgActive[activeTG] = true
	}
	// Also consider a task group active if the dispatcher has any
	// armed intents for its macros.
	for _, plan := range s.dispatcher.Plans() {
		for _, macro := range macros {
			if plan.Macro.ID == macro.ID && plan.Intent.Armed && !plan.Intent.Terminal {
				tgActive[macro.TaskGroupID] = true
				break
			}
		}
	}

	workers, err := s.repository.ListWorkers(ctx)
	if err != nil {
		log.Printf("[cluster] auto-start: list workers: %v", err)
		return
	}
	healthyWorkers := make([]domain.WorkerNode, 0, len(workers))
	for _, w := range workers {
		if !w.Enabled {
			continue
		}
		if w.Type == domain.WorkerTypeRemote && !s.client.IsHealthy(w.ID) {
			continue
		}
		healthyWorkers = append(healthyWorkers, w)
	}

	now := time.Now()
	for tgID, tgMacroList := range tgMacros {
		if tgActive[tgID] {
			continue
		}
		// A task group is ready when at least one of its macros has
		// StartAt <= now (with 30-second pre-start window) and
		// Deadline > now.
		ready := false
		for _, macro := range tgMacroList {
			if !macro.Dispatchable() {
				continue
			}
			if macro.StartAt.IsZero() || macro.Deadline.IsZero() {
				continue
			}
			if macro.Deadline.Before(now) {
				continue
			}
			// Start within 30 seconds before StartAt, or anytime after.
			if !macro.StartAt.After(now.Add(30 * time.Second)) {
				ready = true
				break
			}
		}
		if !ready {
			continue
		}

		// Skip task groups where all intents have already succeeded.
		// For the purpose of this check, a macro without any intents
		// is NOT considered "all succeeded" (it still needs planning).
		intents, _ := s.repository.ListIntents(ctx)
		allSucceeded := len(tgMacroList) > 0
		for _, macro := range tgMacroList {
			macroIntents := 0
			macroSucceeded := 0
			for _, intent := range intents {
				if intent.MacroTaskID != macro.ID {
					continue
				}
				macroIntents++
				if intent.Succeeded {
					macroSucceeded++
				}
			}
			if macroIntents == 0 {
				allSucceeded = false
				break
			}
			if macroSucceeded < macroIntents {
				allSucceeded = false
				break
			}
		}
		if allSucceeded {
			log.Printf("[cluster] auto-start: skip task group %s — all %d macro(s) fully succeeded", tgID, len(tgMacroList))
			continue
		}

		// Check that task group has purchase groups.  We only need
		// one macro with purchase groups to be actionable.
		hasGroups := false
		for _, macro := range tgMacroList {
			groups, err := s.repository.ListPurchaseGroups(ctx, macro.ID)
			if err != nil {
				log.Printf("[cluster] auto-start: list purchase groups for %s: %v", macro.ID, err)
				continue
			}
			if len(groups) > 0 {
				hasGroups = true
				break
			}
		}
		if !hasGroups {
			continue
		}

		// Check that at least one account has buyer mappings for the
		// purchase groups.  Without buyer mappings the dispatcher can
		// never pair an account with a buyer.
		accounts, _ := s.repository.ListAccounts(ctx)
		hasMapping := false
		for _, macro := range tgMacroList {
			groups, _ := s.repository.ListPurchaseGroups(ctx, macro.ID)
			for _, group := range groups {
				for _, buyer := range group.Buyers {
					for _, account := range accounts {
						if _, err := s.repository.BuyerMapping(ctx, account.ID, buyer.LogicalID); err == nil {
							hasMapping = true
							break
						}
					}
					if hasMapping {
						break
					}
				}
				if hasMapping {
					break
				}
			}
			if hasMapping {
				break
			}
		}

		// Collect worker IDs: primary workers from macros first,
		// then standby, then all healthy workers.
		workerIDs := s.collectWorkersForTaskGroup(tgMacroList, healthyWorkers)

		// Build a diagnostic message for the frontend.
		var blocks []string
		if len(healthyWorkers) == 0 {
			blocks = append(blocks, "没有健康的 Worker")
		}
		if len(accounts) == 0 || !hasMapping {
			blocks = append(blocks, "没有账号或购票人尚未同步到账号——请先在「账号管理」中同步购票人")
		}
		if len(workerIDs) == 0 {
			blocks = append(blocks, "没有可用的 Worker（Worker 已被其他任务组占用或离线）")
		}
		if len(blocks) > 0 {
			msg := "任务组「" + tgID + "」已到开票时间但无法自动启动：" + strings.Join(blocks, "；")
			log.Printf("[cluster] auto-start: %s", msg)
			if s.notify != nil {
				s.notify(msg)
			}
			continue
		}

		log.Printf("[cluster] auto-start: starting task group %s with %d workers (macros=%d)", tgID, len(workerIDs), len(tgMacroList))
		workerIDsJSON, _ := json.Marshal(workerIDs)
		if err := s.StartTaskGroup(tgID, string(workerIDsJSON)); err != nil {
			log.Printf("[cluster] auto-start: task group %s failed: %v", tgID, err)
			if s.notify != nil {
				s.notify("任务组「" + tgID + "」启动失败：" + err.Error())
			}
		} else {
			log.Printf("[cluster] auto-start: task group %s started successfully", tgID)
			// Only one task group can be active at a time.
			// Stop processing further task groups to avoid
			// overwriting the worker reservations just made.
			return
		}
	}
}

// collectWorkersForTaskGroup returns a deduplicated list of worker IDs for a
// task group, preferring primary workers from macros, then standby workers,
// then all healthy workers.
func (s *ClusterService) collectWorkersForTaskGroup(macros []domain.MacroTask, healthyWorkers []domain.WorkerNode) []string {
	primarySet := make(map[string]struct{})
	standbySet := make(map[string]struct{})
	for _, macro := range macros {
		for _, id := range macro.PrimaryWorkerIDs {
			primarySet[id] = struct{}{}
		}
		for _, id := range macro.StandbyWorkerIDs {
			standbySet[id] = struct{}{}
		}
	}
	healthySet := make(map[string]struct{}, len(healthyWorkers))
	for _, w := range healthyWorkers {
		healthySet[w.ID] = struct{}{}
	}

	// Collect primary workers that are healthy.
	var result []string
	for id := range primarySet {
		if _, ok := healthySet[id]; ok {
			result = append(result, id)
		}
	}
	// Add standby workers that are healthy.
	for id := range standbySet {
		if _, ok := healthySet[id]; ok {
			// Avoid duplicates (a worker could be both primary and standby).
			found := false
			for _, existing := range result {
				if existing == id {
					found = true
					break
				}
			}
			if !found {
				result = append(result, id)
			}
		}
	}
	// If no primary/standby specified, use all healthy workers.
	if len(result) == 0 {
		for _, w := range healthyWorkers {
			result = append(result, w.ID)
		}
	}
	return result
}
