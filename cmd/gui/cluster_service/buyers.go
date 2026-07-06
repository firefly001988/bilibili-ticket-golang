package cluster_service

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"bilibili-ticket-golang/cluster/dispatcher"
	"bilibili-ticket-golang/cluster/domain"
	clusterstorage "bilibili-ticket-golang/cluster/storage"
)

// ProvisionBuyer creates or updates a buyer on a specific account.
func (s *ClusterService) ProvisionBuyer(document string, confirmed bool) error {
	var input struct {
		AccountID string       `json:"accountId"`
		Buyer     domain.Buyer `json:"buyer"`
	}
	if err := json.Unmarshal([]byte(document), &input); err != nil {
		return err
	}
	_, err := s.accounts.EnsureBuyer(context.Background(), input.AccountID, input.Buyer, confirmed)
	return err
}

// StartBuyerSync starts a background buyer provisioning batch. The request is
// a JSON document containing buyerIds and, optionally, accountIds. Empty
// accountIds means every enabled account.
func (s *ClusterService) StartBuyerSync(document string) (BuyerSyncBatch, error) {
	var req BuyerSyncStartRequest
	if err := json.Unmarshal([]byte(document), &req); err != nil {
		return BuyerSyncBatch{}, err
	}
	return s.runBuyerSyncBatch(context.Background(), req, false)
}

func (s *ClusterService) GetBuyerSyncBatch(batchID string) (BuyerSyncBatch, error) {
	s.buyerSyncMu.RLock()
	defer s.buyerSyncMu.RUnlock()
	batch, ok := s.buyerSyncBatches[batchID]
	if !ok {
		return BuyerSyncBatch{}, fmt.Errorf("buyer sync batch %s not found", batchID)
	}
	return cloneBuyerSyncBatch(batch), nil
}

func (s *ClusterService) runBuyerSyncBatch(ctx context.Context, req BuyerSyncStartRequest, wait bool) (BuyerSyncBatch, error) {
	req.BuyerIDs = uniqueBuyerSyncNonEmpty(req.BuyerIDs)
	req.AccountIDs = uniqueBuyerSyncNonEmpty(req.AccountIDs)
	if len(req.BuyerIDs) == 0 {
		return BuyerSyncBatch{}, errors.New("at least one buyer is required")
	}
	accounts, err := s.repository.ListAccounts(ctx)
	if err != nil {
		return BuyerSyncBatch{}, err
	}
	accountByID := make(map[string]domain.Account, len(accounts))
	for _, account := range accounts {
		accountByID[account.ID] = account
	}
	var selectedAccounts []domain.Account
	if len(req.AccountIDs) == 0 {
		for _, account := range accounts {
			if account.Enabled {
				selectedAccounts = append(selectedAccounts, account)
			}
		}
	} else {
		for _, accountID := range req.AccountIDs {
			account, ok := accountByID[accountID]
			if !ok {
				return BuyerSyncBatch{}, fmt.Errorf("account %s not found", accountID)
			}
			if !account.Enabled {
				return BuyerSyncBatch{}, fmt.Errorf("account %s is disabled", accountID)
			}
			selectedAccounts = append(selectedAccounts, account)
		}
	}
	if len(selectedAccounts) == 0 {
		return BuyerSyncBatch{}, errors.New("no enabled account selected")
	}
	sort.Slice(selectedAccounts, func(i, j int) bool { return selectedAccounts[i].ID < selectedAccounts[j].ID })

	buyers := make([]domain.Buyer, 0, len(req.BuyerIDs))
	for _, buyerID := range req.BuyerIDs {
		buyer, err := s.repository.LogicalBuyer(ctx, buyerID)
		if err != nil {
			return BuyerSyncBatch{}, fmt.Errorf("logical buyer %s: %w", buyerID, err)
		}
		buyers = append(buyers, buyer)
	}
	sort.SliceStable(buyers, func(i, j int) bool { return buyers[i].LogicalID < buyers[j].LogicalID })

	now := time.Now()
	batch := &BuyerSyncBatch{
		ID:        newBuyerSyncID(),
		State:     BuyerSyncPending,
		CreatedAt: now,
		UpdatedAt: now,
	}
	for _, account := range selectedAccounts {
		for _, buyer := range buyers {
			batch.Jobs = append(batch.Jobs, BuyerSyncJob{
				ID:          buyerSyncJobID(account.ID, buyer.LogicalID),
				BuyerID:     buyer.LogicalID,
				BuyerName:   buyer.Name,
				AccountID:   account.ID,
				AccountName: firstNonEmpty(account.Name, account.ID),
				State:       BuyerSyncPending,
			})
		}
	}
	batch.Total = len(batch.Jobs)
	s.buyerSyncMu.Lock()
	if s.buyerSyncBatches == nil {
		s.buyerSyncBatches = make(map[string]*BuyerSyncBatch)
	}
	s.buyerSyncBatches[batch.ID] = batch
	s.buyerSyncMu.Unlock()
	s.appendBuyerSyncLog(batch.ID, "", "", "", BuyerSyncPending, "info", fmt.Sprintf("准备同步 %d 个购票人到 %d 个账号，共 %d 个任务", len(buyers), len(selectedAccounts), batch.Total))

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.executeBuyerSyncBatch(ctx, batch.ID, selectedAccounts, buyers)
	}()
	if wait {
		<-done
		if err := s.refreshResources(ctx); err != nil {
			return BuyerSyncBatch{}, err
		}
		return s.GetBuyerSyncBatch(batch.ID)
	}
	return s.GetBuyerSyncBatch(batch.ID)
}

func (s *ClusterService) executeBuyerSyncBatch(ctx context.Context, batchID string, accounts []domain.Account, buyers []domain.Buyer) {
	s.updateBuyerSyncBatchState(batchID, BuyerSyncRunning, "")
	workerCount := len(s.GetBuyerManagerWorkerIDs())
	if workerCount <= 0 {
		workerCount = 1
	}
	sem := make(chan struct{}, workerCount)
	var wg sync.WaitGroup
	for _, account := range accounts {
		account := account
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			for _, buyer := range buyers {
				if ctx.Err() != nil {
					return
				}
				s.executeBuyerSyncJob(ctx, batchID, account, buyer)
			}
		}()
	}
	wg.Wait()
	_ = s.refreshResources(context.Background())
	final := s.finalizeBuyerSyncBatch(batchID)
	if final.Failed > 0 {
		s.appendBuyerSyncLog(batchID, "", "", "", BuyerSyncFailed, "warn", fmt.Sprintf("同步完成，成功 %d，跳过 %d，失败 %d", final.Succeeded, final.Skipped, final.Failed))
	} else {
		s.appendBuyerSyncLog(batchID, "", "", "", BuyerSyncSuccess, "info", fmt.Sprintf("同步完成，成功 %d，跳过 %d", final.Succeeded, final.Skipped))
	}
}

func (s *ClusterService) executeBuyerSyncJob(ctx context.Context, batchID string, account domain.Account, buyer domain.Buyer) {
	jobID := buyerSyncJobID(account.ID, buyer.LogicalID)
	s.updateBuyerSyncJob(batchID, jobID, BuyerSyncRunning, "正在检查账号购票人列表")
	s.appendBuyerSyncLog(batchID, jobID, buyer.LogicalID, account.ID, BuyerSyncRunning, "info", fmt.Sprintf("开始同步 %s 到账号 %s", buyer.Name, firstNonEmpty(account.Name, account.ID)))

	if _, err := s.repository.BuyerMapping(ctx, account.ID, buyer.LogicalID); err == nil {
		s.updateBuyerSyncJob(batchID, jobID, BuyerSyncSkipped, "本地已存在映射，已跳过")
		s.appendBuyerSyncLog(batchID, jobID, buyer.LogicalID, account.ID, BuyerSyncSkipped, "info", "本地已存在映射，跳过")
		return
	} else if !errors.Is(err, sql.ErrNoRows) {
		msg := fmt.Sprintf("读取本地映射失败：%v", err)
		s.updateBuyerSyncJob(batchID, jobID, BuyerSyncFailed, msg)
		s.appendBuyerSyncLog(batchID, jobID, buyer.LogicalID, account.ID, BuyerSyncFailed, "error", msg)
		return
	}

	s.updateBuyerSyncJob(batchID, jobID, BuyerSyncRunning, "正在远端检查/创建购票人")
	if _, err := s.accounts.EnsureBuyer(ctx, account.ID, buyer, true); err != nil {
		msg := err.Error()
		s.updateBuyerSyncJob(batchID, jobID, BuyerSyncFailed, msg)
		s.appendBuyerSyncLog(batchID, jobID, buyer.LogicalID, account.ID, BuyerSyncFailed, "error", msg)
		return
	}
	s.updateBuyerSyncJob(batchID, jobID, BuyerSyncSuccess, "同步成功")
	s.appendBuyerSyncLog(batchID, jobID, buyer.LogicalID, account.ID, BuyerSyncSuccess, "info", "同步成功")
}

func (s *ClusterService) updateBuyerSyncBatchState(batchID string, state BuyerSyncState, message string) {
	s.buyerSyncMu.Lock()
	defer s.buyerSyncMu.Unlock()
	batch, ok := s.buyerSyncBatches[batchID]
	if !ok {
		return
	}
	batch.State = state
	batch.Message = message
	batch.UpdatedAt = time.Now()
}

func (s *ClusterService) updateBuyerSyncJob(batchID, jobID string, state BuyerSyncState, message string) {
	s.buyerSyncMu.Lock()
	defer s.buyerSyncMu.Unlock()
	batch, ok := s.buyerSyncBatches[batchID]
	if !ok {
		return
	}
	now := time.Now()
	for i := range batch.Jobs {
		if batch.Jobs[i].ID != jobID {
			continue
		}
		old := batch.Jobs[i].State
		if old == BuyerSyncRunning && state != BuyerSyncRunning && batch.Running > 0 {
			batch.Running--
		}
		if old != BuyerSyncRunning && state == BuyerSyncRunning {
			batch.Running++
			batch.Jobs[i].StartedAt = now
		}
		if old != state {
			switch state {
			case BuyerSyncSuccess:
				batch.Succeeded++
			case BuyerSyncSkipped:
				batch.Skipped++
			case BuyerSyncFailed:
				batch.Failed++
			}
		}
		batch.Jobs[i].State = state
		batch.Jobs[i].Message = message
		if state == BuyerSyncSuccess || state == BuyerSyncSkipped || state == BuyerSyncFailed {
			batch.Jobs[i].FinishedAt = now
		}
		batch.UpdatedAt = now
		return
	}
}

func (s *ClusterService) appendBuyerSyncLog(batchID, jobID, buyerID, accountID string, state BuyerSyncState, level, message string) {
	s.buyerSyncMu.Lock()
	defer s.buyerSyncMu.Unlock()
	batch, ok := s.buyerSyncBatches[batchID]
	if !ok {
		return
	}
	batch.Logs = append(batch.Logs, BuyerSyncLogItem{
		Time:      time.Now(),
		Level:     level,
		JobID:     jobID,
		BuyerID:   buyerID,
		AccountID: accountID,
		State:     state,
		Message:   message,
	})
	if len(batch.Logs) > 500 {
		batch.Logs = append([]BuyerSyncLogItem(nil), batch.Logs[len(batch.Logs)-500:]...)
	}
	batch.UpdatedAt = time.Now()
}

func (s *ClusterService) finalizeBuyerSyncBatch(batchID string) BuyerSyncBatch {
	s.buyerSyncMu.Lock()
	defer s.buyerSyncMu.Unlock()
	batch, ok := s.buyerSyncBatches[batchID]
	if !ok {
		return BuyerSyncBatch{}
	}
	if batch.Failed > 0 {
		batch.State = BuyerSyncFailed
		batch.Message = fmt.Sprintf("部分失败：%d/%d", batch.Failed, batch.Total)
	} else {
		batch.State = BuyerSyncSuccess
		batch.Message = "同步完成"
	}
	batch.Running = 0
	batch.UpdatedAt = time.Now()
	return cloneBuyerSyncBatch(batch)
}

func cloneBuyerSyncBatch(batch *BuyerSyncBatch) BuyerSyncBatch {
	if batch == nil {
		return BuyerSyncBatch{}
	}
	result := *batch
	result.Jobs = append([]BuyerSyncJob(nil), batch.Jobs...)
	result.Logs = append([]BuyerSyncLogItem(nil), batch.Logs...)
	return result
}

func uniqueBuyerSyncNonEmpty(values []string) []string {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

func newBuyerSyncID() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err == nil {
		return "buyer-sync-" + hex.EncodeToString(buf[:])
	}
	return fmt.Sprintf("buyer-sync-%d", time.Now().UnixNano())
}

func buyerSyncJobID(accountID, buyerID string) string {
	return accountID + "::" + buyerID
}

type buyerResolver struct {
	repository *clusterstorage.Repository
	ensureFn   func(ctx context.Context, accountID string, buyer domain.Buyer) error
}

func (r buyerResolver) Resolve(ctx context.Context, accountID string, buyers []domain.Buyer) ([]domain.Buyer, error) {
	result := append([]domain.Buyer(nil), buyers...)
	for i := range result {
		mapping, err := r.repository.BuyerMapping(ctx, accountID, result[i].LogicalID)
		if err == nil {
			// Buyer already mapped — merge in the BuyerID. Use the
			// stored unmasked record as the authoritative source so
			// workers always receive complete real-name data.
			full, fullErr := r.repository.LogicalBuyer(ctx, result[i].LogicalID)
			if fullErr != nil {
				// Fall back to the incoming buyer when the DB record
				// is unavailable (e.g. masked and therefore filtered).
				// This preserves forward progress for already-mapped
				// buyers whose DB entry is temporarily masked.
				result[i].BuyerID = mapping.BuyerID
				continue
			}
			full.BuyerID = mapping.BuyerID
			result[i] = full
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("buyer %s is not provisioned on account %s: %w", result[i].LogicalID, accountID, err)
		}
		// Buyer not yet provisioned on this account — auto-sync using the
		// stored unmasked real-name information, then retry the mapping
		// lookup once.
		if r.ensureFn == nil {
			return nil, fmt.Errorf("%w: buyer %s on account %s", dispatcher.ErrBuyerUnavailable, result[i].LogicalID, accountID)
		}
		full, fullErr := r.repository.LogicalBuyer(ctx, result[i].LogicalID)
		if fullErr != nil {
			// LogicalBuyer itself guards against masked data — if it
			// failed (e.g. masked ID card), we cannot proceed.
			return nil, fmt.Errorf("%w: buyer %s on account %s (logical lookup: %w)", dispatcher.ErrBuyerUnavailable, result[i].LogicalID, accountID, fullErr)
		}
		if ensureErr := r.ensureFn(ctx, accountID, full); ensureErr != nil {
			return nil, fmt.Errorf("%w: buyer %s on account %s (ensure: %w)", dispatcher.ErrBuyerUnavailable, result[i].LogicalID, accountID, ensureErr)
		}
		mapping2, retryErr := r.repository.BuyerMapping(ctx, accountID, result[i].LogicalID)
		if retryErr != nil {
			return nil, fmt.Errorf("%w: buyer %s on account %s after ensure: %w", dispatcher.ErrBuyerUnavailable, result[i].LogicalID, accountID, retryErr)
		}
		full.BuyerID = mapping2.BuyerID
		result[i] = full
	}
	return result, nil
}

// biliProvisioner has been replaced by WorkerProvisioner in worker_provisioner.go.
// Buyer CRUD is now delegated to workers via gRPC instead of calling the
// Bilibili API directly from the employer process.
