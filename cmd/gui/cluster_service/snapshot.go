package cluster_service

import (
	"context"
	"log"
	"strings"
	"time"

	"bilibili-ticket-golang/cluster/domain"
	"bilibili-ticket-golang/lib/global"
)

// Snapshot returns a complete ClusterSnapshot for the frontend.
func (s *ClusterService) Snapshot() (ClusterSnapshot, error) {
	ctx := context.Background()
	accountList, err := s.repository.ListAccounts(ctx)
	if err != nil {
		return ClusterSnapshot{}, err
	}
	workerList, err := s.repository.ListWorkers(ctx)
	if err != nil {
		return ClusterSnapshot{}, err
	}
	macros, err := s.repository.ListMacroTasks(ctx)
	if err != nil {
		return ClusterSnapshot{}, err
	}
	taskGroups, err := s.repository.ListTaskGroups(ctx)
	if err != nil {
		return ClusterSnapshot{}, err
	}
	buyers, err := s.repository.ListLogicalBuyers(ctx)
	if err != nil {
		return ClusterSnapshot{}, err
	}
	if buyers == nil {
		buyers = make([]domain.Buyer, 0)
	}
	// Build buyer→accounts mapping from account_buyer_mappings.
	mappings, err := s.repository.ListBuyerMappings(ctx)
	if err != nil {
		return ClusterSnapshot{}, err
	}
	accountByID := make(map[string]domain.Account, len(accountList))
	for _, a := range accountList {
		accountByID[a.ID] = a
	}
	buyerAccounts := make(map[string][]BuyerAccountBadge)
	for _, m := range mappings {
		acc := accountByID[m.AccountID]
		uid := strings.TrimPrefix(m.AccountID, "bili-")
		buyerAccounts[m.LogicalBuyerID] = append(buyerAccounts[m.LogicalBuyerID], BuyerAccountBadge{
			AccountID:   m.AccountID,
			AccountName: acc.Name,
			UID:         uid,
		})
	}
	buyersWithAccounts := make([]BuyerWithAccounts, len(buyers))
	for i, b := range buyers {
		accs := buyerAccounts[b.LogicalID]
		if accs == nil {
			accs = make([]BuyerAccountBadge, 0)
		}
		buyersWithAccounts[i] = BuyerWithAccounts{Buyer: b, Accounts: accs}
	}
	result := ClusterSnapshot{TaskGroups: taskGroups, Buyers: buyersWithAccounts, EmployerVersion: global.GitCommit}

	for _, account := range accountList {
		summary := AccountSummary{ID: account.ID, Name: account.Name, Enabled: account.Enabled, VipStatus: account.VipStatus, CredentialVersion: account.Credentials.Version}
		if !account.CooldownUntil.IsZero() {
			cooldown := account.CooldownUntil
			summary.CooldownUntil = &cooldown
			if account.CooldownUntil.After(time.Now()) {
				summary.CooldownReason = "账号风控触发，冷却 5 分钟"
			}
		}
		result.Accounts = append(result.Accounts, summary)
	}
	for _, node := range workerList {
		summary := WorkerSummary{
			ID:               node.ID,
			Name:             node.Name,
			Address:          node.Address,
			Type:             node.Type,
			Version:          node.Version,
			Enabled:          node.Enabled,
			SkipVersionCheck: node.SkipVersionCheck,
		}
		summary.Healthy = s.client.IsHealthy(node.ID)

		// Last heartbeat info
		if hb, ok := s.client.LastHeartbeat(node.ID); ok {
			summary.LastHeartbeatAt = &hb
			summary.LastHeartbeatLatency = time.Since(hb).Milliseconds()
		}

		// Attach cooldown info from dispatcher for unhealthy workers
		if !summary.Healthy {
			info := s.dispatcher.WorkerCooldown(node.ID)
			if info.CooledDown {
				cooldown := WorkerCooldownInfo{
					CooledDown:      true,
					CooldownEnd:     info.CooldownEnd,
					StartedAt:       info.StartedAt,
					Reason:          info.Reason,
					RemainingMs:     max(0, info.CooldownEnd.Sub(time.Now()).Milliseconds()),
					TotalDurationMs: info.TotalDurationMs,
				}
				summary.Cooldown = cooldown
			}
		}
		// Fetch live metadata via Health RPC.  The worker always returns
		// its real version and clock offsets in the response map.
		if node.Type == domain.WorkerTypeRemote {
			healthCtx, healthCancel := context.WithTimeout(ctx, 800*time.Millisecond)
			health, healthErr := s.client.Health(healthCtx, node)
			healthCancel()
			if health != nil {
				summary.ActiveAttemptID, _ = health["activeAttemptId"].(string)
				if v, ok := health["version"].(string); ok && v != "" {
					summary.Version = v
				}
				if v, ok := health["bilibiliOffsetMs"].(int64); ok {
					summary.BilibiliOffsetMs = v
				}
				if v, ok := health["ntpOffsetMs"].(int64); ok {
					summary.NtpOffsetMs = v
				}
			}
			// If Health succeeded (versions match) and this worker was
			// previously forced, auto-clear SkipVersionCheck so the next
			// divergence requires manual approval again.
			if healthErr == nil && summary.SkipVersionCheck && global.GitCommit != "Development" {
				log.Printf("[cluster] versions converged for worker %s — clearing SkipVersionCheck", node.ID)
				node.SkipVersionCheck = false
				summary.SkipVersionCheck = false
				_ = s.repository.PutWorker(ctx, node)
			}
			_ = healthErr
		}
		result.Workers = append(result.Workers, summary)
	}
	s.mu.RLock()
	for _, macro := range macros {
		phase := s.phases[macro.ID]
		if phase == "" {
			phase = domain.PhasePunctual
		}
		groups, groupErr := s.repository.ListPurchaseGroups(ctx, macro.ID)
		if groupErr != nil {
			s.mu.RUnlock()
			return ClusterSnapshot{}, groupErr
		}
		if groups == nil {
			groups = make([]domain.PurchaseGroup, 0)
		}
		result.Macros = append(result.Macros, MacroSummary{MacroTask: macro, Phase: phase, PurchaseGroups: groups})
	}
	s.mu.RUnlock()

	// ── Intents (pending/armed plans) ──────────────────────────
	// Build a quick lookup: intentID → active (non-terminal) attempt count.
	activeByIntent := make(map[string]int)
	for _, value := range s.dispatcher.Attempts() {
		if !value.State.Terminal() {
			activeByIntent[value.IntentID]++
		}
	}
	targets := s.dispatcher.AllocationTargets()
	for _, plan := range s.dispatcher.Plans() {
		active := activeByIntent[plan.Intent.ID]
		w := plan.Intent.Weight
		if w <= 0 {
			w = 1
		}
		deficit := targets[plan.Intent.ID] - active
		if deficit < 0 {
			deficit = 0
		}
		result.Intents = append(result.Intents, IntentSummary{
			ID:              plan.Intent.ID,
			MacroTaskID:     plan.Intent.MacroTaskID,
			PurchaseGroupID: plan.Intent.PurchaseGroupID,
			Phase:           plan.Intent.Phase,
			Weight:          w,
			Priority:        plan.Intent.Priority,
			BuyerCount:      len(plan.Intent.Buyers),
			Succeeded:       plan.Intent.Succeeded,
			Terminal:        plan.Intent.Terminal,
			Armed:           plan.Intent.Armed,
			ActiveCount:     active,
			Deficit:         deficit,
			FailureReason:   plan.Intent.FailureReason,
			CreatedAt:       plan.Intent.CreatedAt,
		})
	}

	// ── Attempts: merge dispatcher in-memory + DB historical records ──────────
	seen := make(map[string]bool)
	for _, value := range s.dispatcher.Attempts() {
		result.Attempts = append(result.Attempts, AttemptSummary{ID: value.ID, IntentID: value.IntentID, AccountID: value.AccountID, WorkerID: value.WorkerID, State: value.State, OrderID: value.Result.OrderID, PaymentURL: value.Result.PaymentURL, Reason: value.Result.Reason})
		seen[value.ID] = true
	}
	// Also load terminal attempts from DB that were purged from dispatcher
	// memory (e.g. after force-reset), so the logs page retains history.
	dbAttempts, dbErr := s.repository.ListAttempts(ctx)
	if dbErr == nil {
		for _, value := range dbAttempts {
			if seen[value.ID] {
				continue
			}
			result.Attempts = append(result.Attempts, AttemptSummary{ID: value.ID, IntentID: value.IntentID, AccountID: value.AccountID, WorkerID: value.WorkerID, State: value.State, OrderID: value.Result.OrderID, PaymentURL: value.Result.PaymentURL, Reason: value.Result.Reason})
		}
	}
	return result, nil
}
