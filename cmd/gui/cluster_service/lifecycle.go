package cluster_service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"time"

	"bilibili-ticket-golang/cluster/accounts"
	"bilibili-ticket-golang/cluster/dispatcher"
	"bilibili-ticket-golang/cluster/domain"
	"bilibili-ticket-golang/cluster/employer"
	clusterstorage "bilibili-ticket-golang/cluster/storage"
	clusterworker "bilibili-ticket-golang/cluster/worker"
	"bilibili-ticket-golang/lib/global"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// NewClusterService creates a fully wired ClusterService backed by the
// given repository. It sets up the worker client, account manager,
// dispatcher, and success/notification callbacks.
func NewClusterService(repository *clusterstorage.Repository) *ClusterService {
	client := employer.NewWorkerClient()
	provisioner := NewWorkerProvisioner(client)
	service := &ClusterService{
		repository:           repository,
		client:               client,
		provisioner:          provisioner,
		phases:               make(map[string]domain.Phase),
		waveCancels:          make(map[string]context.CancelFunc),
		loginSessions:        make(map[string]*accountLoginSession),
		loginCaptchaSessions: make(map[string]*loginCaptchaSession),
		deployJobs:           make(map[string]*RemoteWorkerDeployJob),
		buyerSyncBatches:     make(map[string]*BuyerSyncBatch),
		bwsMeta:              make(map[string]BWSSubmitInput),
		openedPaymentWindows: make(map[string]bool),
	}
	service.loadBWSMetadata()
	// Wire the worker selection strategy: the provisioner uses the
	// current known worker set to decide which worker handles each
	// account.  The closure captures *ClusterService so it can read
	// the dispatcher's live worker state.
	provisioner.SetPickWorker(
		func(accountID string) (domain.WorkerNode, error) {
			return service.pickWorkerForAccount(accountID)
		},
		func(accountID string) {
			service.releaseAccount(accountID)
		},
	)
	service.accounts = accounts.NewManager(repository, provisioner)
	service.dispatcher = dispatcher.New(client, repository, buyerResolver{
		repository: repository,
		ensureFn: func(ctx context.Context, accountID string, buyer domain.Buyer) error {
			// confirmed=true: the real-name data was already persisted by
			// SyncBuyers / SyncAllBuyers; we are just replicating it now.
			_, err := service.accounts.EnsureBuyer(ctx, accountID, buyer, true)
			return err
		},
	})
	handleOrderResult := func(intent domain.LogicalOrderIntent, result domain.ExecutionResult) {
		log.Printf("[cluster] order result callback ENTER: intent=%s success=%v partial=%v orderID=%s",
			intent.ID, result.Success, result.Partial, result.OrderID)
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[cluster] order result callback PANIC: intent=%s panic=%v", intent.ID, r)
			}
		}()
		// Persist the order before starting any external notification request.
		// A slow or broken notification endpoint must never prevent the
		// employer from learning about an order that needs payment.
		records, err := service.saveOrderRecords(intent, result)
		if err != nil {
			log.Printf("[cluster] save order record failed: intent=%s orderID=%s: %v", intent.ID, result.OrderID, err)
		} else {
			for _, record := range records {
				if record.Status == "" || record.Status == domain.SubOrderSucceeded {
					go service.openOrderRecordPaymentWindowOnce(record)
				}
			}
		}
		if notify := service.notify; notify != nil {
			message := fmt.Sprintf("购票成功：Intent %s，订单 %s", intent.ID, result.OrderID)
			if result.Partial {
				message = fmt.Sprintf("购票部分完成：Intent %s，已创建 %d 个子订单", intent.ID, successfulSubOrderCount(result.SubOrders))
			}
			go func() {
				defer func() {
					if r := recover(); r != nil {
						log.Printf("[cluster] success notifier PANIC: intent=%s panic=%v", intent.ID, r)
					}
				}()
				notify(message)
			}()
		}
		log.Printf("[cluster] order result callback DONE: intent=%s", intent.ID)
	}
	service.dispatcher.SetSuccessHandler(handleOrderResult)
	service.dispatcher.SetPartialHandler(handleOrderResult)
	service.dispatcher.SetProgressHandler(handleOrderResult)

	// Wire the bidirectional heartbeat callback: when a worker pushes a
	// completed task, the dispatcher processes it immediately instead of
	// waiting for the next 15s polling cycle.  Also record the event in
	// the cluster-wide unified event log.
	client.SetOnCompletedTask(func(workerID string, result domain.ExecutionResult) {
		log.Printf("[cluster] heartbeat push received: worker=%s attempt=%s success=%v orderID=%s paymentURL=%q",
			workerID, result.AttemptID, result.Success, result.OrderID, result.PaymentURL)
		result = service.dispatcher.ProcessCompletedTask(workerID, result)
		if result.State.Terminal() {
			service.RecordTaskCompleted(workerID, result)
		}
	})

	// Restore persisted global configuration.
	service.LoadGlobalConfig(context.Background())

	// Configure buyer sync concurrency from the persisted worker pool.
	service.accounts.SetSyncConcurrency(len(service.GetBuyerManagerWorkerIDs()))

	return service
}

// SetNotifier sets the notification callback invoked on ticket success.
func (s *ClusterService) SetNotifier(notify func(string)) { s.notify = notify }

// SetApp stores the Wails app reference for opening payment QR windows.
func (s *ClusterService) SetApp(app *application.App) { s.wailsApp = app }

// SetLocalWorkerSolver installs a captcha solving function on the local
// worker manager. When set, local workers will use this solver for
// voucher resolution, and the TestCaptcha gRPC RPC will be functional.
func (s *ClusterService) SetLocalWorkerSolver(
	solver func(gt, challenge string) (string, error),
	tester func() (elapsed, validate, captchaType string, err error),
) {
	s.captchaSolver = solver
	s.local.SetSolver(solver)
	s.local.SetCaptchaTester(tester)
}

func (s *ClusterService) openPayQRWindow(intent domain.LogicalOrderIntent, result domain.ExecutionResult) {
	log.Printf("[cluster] openPayQRWindow called: intent=%s success=%v orderID=%s paymentURL=%q wailsApp=%v",
		intent.ID, result.Success, result.OrderID, result.PaymentURL, s.wailsApp != nil)
	record, err := s.saveOrderRecord(intent, result)
	if err != nil {
		log.Printf("[cluster] save order record failed: intent=%s orderID=%s: %v", intent.ID, result.OrderID, err)
		return
	}
	s.openOrderRecordPaymentWindow(record)
}

// Start brings the cluster online: loads persisted resources, refreshes
// account credentials, starts local workers, recovers in-flight attempts,
// and launches the background reconciliation loop.
func (s *ClusterService) Start(parent context.Context) error {
	log.Printf("[cluster] starting cluster service (employer commit=%s)", global.GitCommit)
	ctx, cancel := context.WithCancel(parent)
	s.cancel = cancel

	// Clean up expired manual captcha sessions every minute.
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.mu.Lock()
				now := time.Now()
				for id, sess := range s.loginCaptchaSessions {
					if now.Sub(sess.CreatedAt) > 5*time.Minute {
						delete(s.loginCaptchaSessions, id)
					}
				}
				s.mu.Unlock()
			}
		}
	}()

	accountsList, err := s.repository.ListAccounts(ctx)
	if err != nil {
		return global.NewFault("列出账号", err, "检查集群数据库 data/employer.db 是否可读")
	}
	for _, account := range accountsList {
		if account.ID == "migrated-account" && !account.Enabled {
			if deleteErr := s.repository.DeleteAccount(ctx, account.ID); deleteErr != nil && !errors.Is(deleteErr, sql.ErrNoRows) {
				return global.NewFault("清理迁移账号", deleteErr, "数据库操作失败，检查 data/employer.db 完整性")
			}
		}
	}
	accountsList, err = s.repository.ListAccounts(ctx)
	if err != nil {
		return global.NewFault("重新列出账号", err, "检查集群数据库 data/employer.db 是否可读")
	}
	// Refresh credentials for every enabled account so workers always
	// operate on fresh cookies.  Workers MUST NOT refresh cookies on
	// their own — credential rotation is the employer's responsibility.
	// Also verify login status — stale cookies that can no longer
	// authenticate are marked disabled and require re-login.
	refreshedCount := 0
	disabledCount := 0
	for _, account := range accountsList {
		if !account.Enabled {
			continue
		}
		result, refreshErr := s.refreshAccountStatus(ctx, account.ID)
		if refreshErr != nil {
			log.Printf("[cluster] refresh status for account %s: %v", account.ID, refreshErr)
			continue
		}
		if result.Refreshed {
			refreshedCount++
		}
		if result.Disabled {
			disabledCount++
		}
	}
	if refreshedCount > 0 {
		log.Printf("[cluster] refreshed cookies for %d account(s)", refreshedCount)
	}
	if disabledCount > 0 {
		log.Printf("[cluster] disabled %d account(s) due to lost login", disabledCount)
	}

	workers, err := s.repository.ListWorkers(ctx)
	if err != nil {
		return global.NewFault("列出 Worker", err, "检查集群数据库 data/employer.db 是否可读")
	}
	pluginName := ""
	// Recover all local workers persisted in the repository and ensure
	// the primary "local" worker always exists — it can never be deleted.
	hasLocal := false
	for _, node := range workers {
		if node.ID == "local" {
			hasLocal = true
		}
		if node.Type != domain.WorkerTypeLocal {
			continue
		}
		if _, startErr := s.local.AddWorker(ctx, s.client, node.ID, node.Name, node.Address, employer.LocalWorkerOptions{
			PluginDir:     "plugins",
			CaptchaPlugin: pluginName,
			Version:       global.GitCommit,
		}); startErr != nil {
			log.Printf("[cluster] recover local worker %s: %v", node.ID, startErr)
			continue
		}
		// Persist TLS for this recovered worker.
		if tlsBundle, _, tlsErr := clusterworker.LoadOrGenerateLocalTLS("data/" + node.ID); tlsErr == nil {
			_ = s.repository.PutWorkerTLS(ctx, node.ID, domain.WorkerTLSConfig{
				CACertPEM:     tlsBundle.CAPEM,
				ClientCertPEM: tlsBundle.CertPEM,
				ClientKeyPEM:  tlsBundle.KeyPEM,
				ServerName:    "localhost",
			})
		}
	}
	// Ensure the primary "local" worker always exists.
	if !hasLocal {
		log.Printf("[cluster] auto-creating primary local worker")
		if _, startErr := s.local.AddWorker(ctx, s.client, "local", "Local Worker", "127.0.0.1:37900", employer.LocalWorkerOptions{
			PluginDir:     "plugins",
			CaptchaPlugin: pluginName,
			Version:       global.GitCommit,
		}); startErr != nil {
			log.Printf("[cluster] auto-create local worker: %v", startErr)
		} else {
			localNode := domain.WorkerNode{ID: "local", Name: "Local Worker", Address: "127.0.0.1:37900", Type: domain.WorkerTypeLocal, Enabled: true}
			_ = s.repository.PutWorker(ctx, localNode)
			if tlsBundle, _, tlsErr := clusterworker.LoadOrGenerateLocalTLS("data/local"); tlsErr == nil {
				_ = s.repository.PutWorkerTLS(ctx, "local", domain.WorkerTLSConfig{
					CACertPEM:     tlsBundle.CAPEM,
					ClientCertPEM: tlsBundle.CertPEM,
					ClientKeyPEM:  tlsBundle.KeyPEM,
					ServerName:    "localhost",
				})
			}
		}
		// Reload workers after adding the local one.
		workers, _ = s.repository.ListWorkers(ctx)
	}

	workers, err = s.repository.ListWorkers(ctx)
	if err != nil {
		return global.NewFault("重新列出 Worker", err, "检查集群数据库 data/employer.db 是否可读")
	}
	// Load TLS for every worker into the client *after* all local workers
	// have been started and their TLS keys persisted to the database.
	// This must happen before refreshResources / pushGlobalConfigToAll
	// because both require the client to be able to dial each worker.
	for _, node := range workers {
		if tls, err := s.repository.WorkerTLS(ctx, node.ID); err == nil {
			if setErr := s.client.SetTLSFromConfig(node.ID, tls); setErr != nil {
				log.Printf("[cluster] TLS config for worker %s: %v", node.ID, setErr)
			}
		} else {
			log.Printf("[cluster] missing TLS for worker %s: %v", node.ID, err)
		}
	}
	// Load all resources and eagerly dial every worker so connection
	// problems surface immediately instead of waiting for a dispatch.
	_ = s.refreshResources(ctx)

	// Push the persisted global config to every worker that is now
	// reachable.  Local workers are skipped in refreshResources' health
	// loop (they have no RPC health check) but they still need to
	// receive the retry-interval and start-delay settings.
	s.pushGlobalConfigToAll(context.Background())
	macros, err := s.repository.ListMacroTasks(ctx)
	if err != nil {
		return global.NewFault("列出宏任务", err, "检查集群数据库 data/employer.db 是否可读")
	}
	macroByID := make(map[string]domain.MacroTask, len(macros))
	for _, macro := range macros {
		macroByID[macro.ID] = macro
	}
	groups, err := s.repository.ListTaskGroups(ctx)
	if err != nil {
		return global.NewFault("列出任务组", err, "检查集群数据库 data/employer.db 是否可读")
	}
	taskGroupByID := make(map[string]domain.TaskGroup, len(groups))
	for _, taskGroup := range groups {
		normalizeTaskGroupDefaults(&taskGroup)
		taskGroupByID[taskGroup.ID] = taskGroup
	}
	intents, err := s.repository.ListIntents(ctx)
	if err != nil {
		return global.NewFault("列出意图", err, "检查集群数据库 data/employer.db 是否可读")
	}
	intentByID := make(map[string]domain.LogicalOrderIntent, len(intents))
	for _, intent := range intents {
		intentByID[intent.ID] = intent
		if macro, ok := macroByID[intent.MacroTaskID]; ok {
			s.dispatcher.Add(dispatcher.IntentPlan{TaskGroup: taskGroupByID[macro.TaskGroupID], Macro: macro, Intent: intent})
			s.phases[macro.ID] = intent.Phase
		}
	}
	attempts, err := s.repository.ListAttempts(ctx)
	if err != nil {
		return global.NewFault("列出尝试记录", err, "检查集群数据库 data/employer.db 是否可读")
	}
	for _, value := range attempts {
		if intent, known := intentByID[value.IntentID]; known && !intent.Armed && !value.State.Terminal() {
			value.State = domain.AttemptStopped
			value.UpdatedAt = time.Now()
			value.Result = domain.ExecutionResult{AttemptID: value.ID, IntentID: value.IntentID, SpecHash: value.SpecHash, State: domain.AttemptStopped, Reason: domain.FailureStopped, Message: "legacy unarmed attempt stopped during recovery", FinishedAt: value.UpdatedAt}
			if err := s.repository.PutAttempt(ctx, value); err != nil {
				return global.NewFault("保存已停止的尝试记录", err, "检查集群数据库 data/employer.db 是否可写")
			}
			continue
		}
		if err := s.dispatcher.RestoreAttempt(value); err != nil {
			return global.NewFault("恢复尝试记录", err, "尝试记录数据可能已损坏，可尝试删除 data/employer.db 重建")
		}
	}
	go func() {
		normalTicker := time.NewTicker(5 * time.Second)
		fastTicker := time.NewTicker(2 * time.Second)
		defer normalTicker.Stop()
		defer fastTicker.Stop()
		// Drain the fast ticker channel when not in fast mode so we don't
		// get a burst of ticks when switching.
		fastCh := make(chan time.Time)
		go func() {
			for t := range fastTicker.C {
				select {
				case fastCh <- t:
				default:
				}
			}
		}()
		useFast := false
		for {
			var tickCh <-chan time.Time
			if useFast {
				tickCh = fastCh
			} else {
				tickCh = normalTicker.C
			}
			select {
			case <-ctx.Done():
				return
			case <-tickCh:
				if err := s.refreshResources(ctx); err != nil {
					log.Printf("[cluster] reconcile: refreshResources: %v", err)
				}
				if err := s.dispatcher.Reconcile(ctx); err != nil {
					log.Printf("[cluster] reconcile: Reconcile error: %v", err)
				}
				// Switch to 5s polling if any worker is under 412 cooldown
				// and there are intents waiting for an attempt.
				useFast = s.dispatcher.HasCooldownWorkersWithDeficit()
			}
		}
	}()
	// Wait for local workers to become healthy before returning.
	// This prevents the frontend from immediately dispatching tasks before
	// the gRPC servers are ready to accept connections.
	s.waitForLocalWorkers(ctx, 5*time.Second)
	return nil
}

// waitForLocalWorkers polls all known local worker slots until they are
// healthy or the timeout expires.  It logs a warning for any worker that
// does not become healthy in time.
func (s *ClusterService) waitForLocalWorkers(ctx context.Context, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	workers, err := s.repository.ListWorkers(ctx)
	if err != nil {
		log.Printf("[cluster] waitForLocalWorkers: unable to list workers: %v", err)
		return
	}
	for _, node := range workers {
		if node.Type != domain.WorkerTypeLocal {
			continue
		}
		for time.Now().Before(deadline) {
			if s.local.Healthy(node.ID) {
				log.Printf("[cluster] local worker %s is healthy", node.ID)
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		if !s.local.Healthy(node.ID) {
			log.Printf("[cluster] WARNING: local worker %s did not become healthy within %v", node.ID, timeout)
		}
	}
}

// Close shuts down the cluster: cancels the reconciliation loop, stops
// local workers, and closes the repository.
func (s *ClusterService) Close() {
	if s.cancel != nil {
		s.cancel()
	}
	s.cancelAllTaskGroupWaves()
	_ = s.local.Stop()
	_ = s.repository.Close()
}
