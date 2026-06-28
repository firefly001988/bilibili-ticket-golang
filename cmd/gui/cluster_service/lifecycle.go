package cluster_service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"runtime"
	"strings"
	"time"

	"bilibili-ticket-golang/cluster/accounts"
	"bilibili-ticket-golang/cluster/dispatcher"
	"bilibili-ticket-golang/cluster/domain"
	"bilibili-ticket-golang/cluster/employer"
	clusterstorage "bilibili-ticket-golang/cluster/storage"
	clusterworker "bilibili-ticket-golang/cluster/worker"
	"bilibili-ticket-golang/cmd/gui/payqr"
	"bilibili-ticket-golang/lib/global"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// NewClusterService creates a fully wired ClusterService backed by the
// given repository. It sets up the worker client, account manager,
// dispatcher, and success/notification callbacks.
func NewClusterService(repository *clusterstorage.Repository) *ClusterService {
	client := employer.NewWorkerClient()
	service := &ClusterService{repository: repository, client: client, phases: make(map[string]domain.Phase), loginSessions: make(map[string]*accountLoginSession)}
	service.accounts = accounts.NewManager(repository, biliProvisioner{})
	service.dispatcher = dispatcher.New(client, repository, buyerResolver{
		repository: repository,
		ensureFn: func(ctx context.Context, accountID string, buyer domain.Buyer) error {
			// confirmed=true: the real-name data was already persisted by
			// SyncBuyers / SyncAllBuyers; we are just replicating it now.
			_, err := service.accounts.EnsureBuyer(ctx, accountID, buyer, true)
			return err
		},
	})
	service.dispatcher.SetSuccessHandler(func(intent domain.LogicalOrderIntent, result domain.ExecutionResult) {
		log.Printf("[cluster] onSuccess callback ENTER: intent=%s success=%v orderID=%s paymentURL=%q",
			intent.ID, result.Success, result.OrderID, result.PaymentURL)
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[cluster] onSuccess callback PANIC: intent=%s panic=%v", intent.ID, r)
			}
		}()
		if service.notify != nil {
			service.notify(fmt.Sprintf("购票成功：Intent %s，订单 %s", intent.ID, result.OrderID))
		}
		service.openPayQRWindow(intent, result)
		log.Printf("[cluster] onSuccess callback DONE: intent=%s", intent.ID)
	})

	// Wire the bidirectional heartbeat callback: when a worker pushes a
	// completed task, the dispatcher processes it immediately instead of
	// waiting for the next 15s polling cycle.
	client.SetOnCompletedTask(func(workerID string, result domain.ExecutionResult) {
		log.Printf("[cluster] heartbeat push received: worker=%s attempt=%s success=%v orderID=%s paymentURL=%q",
			workerID, result.AttemptID, result.Success, result.OrderID, result.PaymentURL)
		service.dispatcher.ProcessCompletedTask(workerID, result)
	})
	return service
}

// SetNotifier sets the notification callback invoked on ticket success.
func (s *ClusterService) SetNotifier(notify func(string)) { s.notify = notify }

// SetApp stores the Wails app reference for opening payment QR windows.
func (s *ClusterService) SetApp(app *application.App) { s.wailsApp = app }

func (s *ClusterService) openPayQRWindow(intent domain.LogicalOrderIntent, result domain.ExecutionResult) {
	log.Printf("[cluster] openPayQRWindow called: intent=%s success=%v orderID=%s paymentURL=%q wailsApp=%v",
		intent.ID, result.Success, result.OrderID, result.PaymentURL, s.wailsApp != nil)
	if s.wailsApp == nil {
		log.Printf("[cluster] openPayQRWindow SKIP: wailsApp is nil (SetApp not called)")
		return
	}
	if result.PaymentURL == "" {
		log.Printf("[cluster] openPayQRWindow SKIP: PaymentURL is empty (orderID=%s)", result.OrderID)
		return
	}
	var macro domain.MacroTask
	if s.repository != nil {
		if macros, err := s.repository.ListMacroTasks(context.Background()); err == nil {
			for _, current := range macros {
				if current.ID == intent.MacroTaskID {
					macro = current
					break
				}
			}
		}
	}
	buyerNames := make([]string, 0, len(intent.Buyers))
	for _, buyer := range intent.Buyers {
		if buyer.Name != "" {
			buyerNames = append(buyerNames, buyer.Name)
		}
	}
	values := url.Values{}
	values.Set("link", result.PaymentURL)
	values.Set("title", "支付二维码")
	values.Set("project", macro.ProjectName)
	values.Set("screen", macro.ScreenName)
	values.Set("sku", macro.SKUName)
	values.Set("buyer", strings.Join(buyerNames, ", "))
	if result.PaymentExpire > 0 {
		values.Set("expire", fmt.Sprint(result.PaymentExpire))
	}
	if result.OrderTime > 0 {
		values.Set("orderTime", fmt.Sprint(result.OrderTime))
	}

	payqr.OpenWindow(s.wailsApp, "支付二维码", values)
}

// Start brings the cluster online: loads persisted resources, refreshes
// account credentials, starts local workers, recovers in-flight attempts,
// and launches the background reconciliation loop.
func (s *ClusterService) Start(parent context.Context) error {
	ctx, cancel := context.WithCancel(parent)
	s.cancel = cancel
	accountsList, err := s.repository.ListAccounts(ctx)
	if err != nil {
		return err
	}
	for _, account := range accountsList {
		if account.ID == "migrated-account" && !account.Enabled {
			if deleteErr := s.repository.DeleteAccount(ctx, account.ID); deleteErr != nil && !errors.Is(deleteErr, sql.ErrNoRows) {
				return deleteErr
			}
		}
	}
	accountsList, err = s.repository.ListAccounts(ctx)
	if err != nil {
		return err
	}
	// Refresh credentials for every enabled account so workers always
	// operate on fresh cookies.  Workers MUST NOT refresh cookies on
	// their own — credential rotation is the employer's responsibility.
	// Also verify login status — stale cookies that can no longer
	// authenticate are marked disabled and require re-login.
	refreshedCount := 0
	disabledCount := 0
	for i, account := range accountsList {
		if !account.Enabled {
			continue
		}
		client, jar, clientErr := accountClient(account)
		if clientErr != nil {
			log.Printf("[cluster] create client for account %s: %v", account.ID, clientErr)
			continue
		}
		client.SetRefreshToken(account.Credentials.RefreshToken)

		// Check login status first.
		loginInfo, statusErr := client.GetAccountStatus()
		if statusErr != nil || loginInfo == nil || !loginInfo.Login || loginInfo.UID == 0 {
			reason := "api error"
			if statusErr != nil {
				reason = statusErr.Error()
			} else if loginInfo == nil {
				reason = "nil response"
			} else if !loginInfo.Login {
				reason = "not logged in"
			}
			log.Printf("[cluster] account %s (%s) login check failed: %s — disabling", account.ID, account.Name, reason)
			accountsList[i].Enabled = false
			accountsList[i].Credentials.Version++
			if putErr := s.repository.PutAccount(ctx, accountsList[i], nil); putErr != nil {
				log.Printf("[cluster] persist disabled account %s: %v", account.ID, putErr)
			}
			disabledCount++
			continue
		}

		// Update VIP status from the API response.
		if loginInfo.IsVip != account.VipStatus {
			accountsList[i].VipStatus = loginInfo.IsVip
			changed := false
			if loginInfo.IsVip == 1 {
				log.Printf("[cluster] account %s (%s) is VIP", account.ID, account.Name)
				changed = true
			}
			if changed {
				if putErr := s.repository.PutAccount(ctx, accountsList[i], nil); putErr != nil {
					log.Printf("[cluster] persist VIP status for account %s: %v", account.ID, putErr)
				}
			}
		}

		// Logged in — attempt cookie refresh.
		refreshed, refreshErr := client.CheckAndUpdateCookie()
		if refreshErr != nil {
			log.Printf("[cluster] cookie refresh for account %s: %v", account.ID, refreshErr)
			continue
		}
		if refreshed {
			updated := credentialsFrom(client, jar, account.Credentials)
			if updated.Version != account.Credentials.Version {
				accountsList[i].Credentials = updated
				if putErr := s.repository.PutAccount(ctx, accountsList[i], nil); putErr != nil {
					log.Printf("[cluster] persist refreshed credentials for account %s: %v", account.ID, putErr)
				}
				refreshedCount++
			}
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
		return err
	}
	for _, node := range workers {
		if tls, err := s.repository.WorkerTLS(ctx, node.ID); err == nil {
			if setErr := s.client.SetTLSFromConfig(node.ID, tls); setErr != nil {
				log.Printf("[cluster] TLS config for worker %s: %v", node.ID, setErr)
			}
		}
	}
	pluginName := ""
	pluginFile := "plugins/captcha-plugin"
	if runtime.GOOS == "windows" {
		pluginFile += ".exe"
	}
	if _, statErr := os.Stat(pluginFile); statErr == nil {
		pluginName = "captcha-plugin"
	}
	// Recover all local workers persisted in the repository (no longer
	// auto-create a default "local" worker — users add them manually).
	for _, node := range workers {
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

	workers, err = s.repository.ListWorkers(ctx)
	if err != nil {
		return err
	}
	// Load all resources and eagerly dial every worker so connection
	// problems surface immediately instead of waiting for a dispatch.
	_ = s.refreshResources(ctx)
	macros, err := s.repository.ListMacroTasks(ctx)
	if err != nil {
		return err
	}
	macroByID := make(map[string]domain.MacroTask, len(macros))
	for _, macro := range macros {
		macroByID[macro.ID] = macro
	}
	intents, err := s.repository.ListIntents(ctx)
	if err != nil {
		return err
	}
	intentByID := make(map[string]domain.LogicalOrderIntent, len(intents))
	for _, intent := range intents {
		intentByID[intent.ID] = intent
		if macro, ok := macroByID[intent.MacroTaskID]; ok {
			s.dispatcher.Add(dispatcher.IntentPlan{Macro: macro, Intent: intent})
			s.phases[macro.ID] = intent.Phase
		}
	}
	attempts, err := s.repository.ListAttempts(ctx)
	if err != nil {
		return err
	}
	for _, value := range attempts {
		if intent, known := intentByID[value.IntentID]; known && !intent.Armed && !value.State.Terminal() {
			value.State = domain.AttemptStopped
			value.UpdatedAt = time.Now()
			value.Result = domain.ExecutionResult{AttemptID: value.ID, IntentID: value.IntentID, SpecHash: value.SpecHash, State: domain.AttemptStopped, Reason: domain.FailureStopped, Message: "legacy unarmed attempt stopped during recovery", FinishedAt: value.UpdatedAt}
			if err := s.repository.PutAttempt(ctx, value); err != nil {
				return err
			}
			continue
		}
		if err := s.dispatcher.RestoreAttempt(value); err != nil {
			return err
		}
	}
	go func() {
		normalTicker := time.NewTicker(15 * time.Second)
		fastTicker := time.NewTicker(5 * time.Second)
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
				s.autoStartReadyTaskGroups(ctx)
				if err := s.dispatcher.Reconcile(ctx); err != nil {
					log.Printf("[cluster] reconcile: Reconcile error: %v", err)
				}
				// Switch to 5s polling if any worker is under 412 cooldown
				// and there are intents waiting for an attempt.
				useFast = s.dispatcher.HasCooldownWorkersWithDeficit()
			}
		}
	}()
	return nil
}

// Close shuts down the cluster: cancels the reconciliation loop, stops
// local workers, and closes the repository.
func (s *ClusterService) Close() {
	if s.cancel != nil {
		s.cancel()
	}
	_ = s.local.Stop()
	_ = s.repository.Close()
}
