package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"bilibili-ticket-golang/cluster/accounts"
	"bilibili-ticket-golang/cluster/dispatcher"
	"bilibili-ticket-golang/cluster/domain"
	"bilibili-ticket-golang/cluster/employer"
	"bilibili-ticket-golang/cluster/planner"
	clusterstorage "bilibili-ticket-golang/cluster/storage"
	clusterworker "bilibili-ticket-golang/cluster/worker"
	"bilibili-ticket-golang/cmd/gui/store/cookiejar"
	"bilibili-ticket-golang/lib/biliutils"
	"bilibili-ticket-golang/lib/global"

	"github.com/wailsapp/wails/v3/pkg/application"
)

type ClusterSnapshot struct {
	TaskGroups []domain.TaskGroup  `json:"taskGroups"`
	Accounts   []AccountSummary    `json:"accounts"`
	Buyers     []BuyerWithAccounts `json:"buyers"`
	Workers    []WorkerSummary     `json:"workers"`
	Macros     []MacroSummary      `json:"macros"`
	Attempts   []AttemptSummary    `json:"attempts"`
}

// BuyerAccountBadge represents an account that owns a particular buyer.
type BuyerAccountBadge struct {
	AccountID   string `json:"accountId"`
	AccountName string `json:"accountName"`
	UID         string `json:"uid"`
}

// BuyerWithAccounts extends domain.Buyer with the list of accounts that
// have this buyer in their real-name list.
type BuyerWithAccounts struct {
	domain.Buyer
	Accounts []BuyerAccountBadge `json:"accounts"`
}
type AccountSummary struct {
	ID                string              `json:"id"`
	Name              string              `json:"name"`
	Role              domain.ResourceRole `json:"role"`
	Enabled           bool                `json:"enabled"`
	CooldownUntil     *time.Time          `json:"cooldownUntil,omitempty"`
	CredentialVersion int64               `json:"credentialVersion"`
}
type WorkerSummary struct {
	ID              string              `json:"id"`
	Name            string              `json:"name"`
	Address         string              `json:"address"`
	Type            domain.WorkerType   `json:"type"`
	Role            domain.ResourceRole `json:"role"`
	Enabled         bool                `json:"enabled"`
	Healthy         bool                `json:"healthy"`
	ActiveAttemptID string              `json:"activeAttemptId,omitempty"`
	Version         string              `json:"version,omitempty"`
}
type MacroSummary struct {
	domain.MacroTask
	Phase          domain.Phase           `json:"phase"`
	PurchaseGroups []domain.PurchaseGroup `json:"purchaseGroups"`
}
type AttemptSummary struct {
	ID         string               `json:"id"`
	IntentID   string               `json:"intentId"`
	AccountID  string               `json:"accountId"`
	WorkerID   string               `json:"workerId"`
	State      domain.AttemptState  `json:"state"`
	OrderID    string               `json:"orderId,omitempty"`
	PaymentURL string               `json:"paymentUrl,omitempty"`
	Reason     domain.FailureReason `json:"reason,omitempty"`
}

type ClusterService struct {
	repository    *clusterstorage.Repository
	client        *employer.WorkerClient
	dispatcher    *dispatcher.Dispatcher
	accounts      *accounts.Manager
	local         employer.LocalWorkerManager
	mu            sync.RWMutex
	mainAccountMu sync.Mutex
	phases        map[string]domain.Phase
	loginSessions map[string]*accountLoginSession
	catalog       *biliutils.BiliClient
	cancel        context.CancelFunc
	notify        func(string)
	wailsApp      *application.App
}

func (s *ClusterService) SetCatalogClient(client *biliutils.BiliClient) { s.catalog = client }

// SyncMainAccount mirrors the credentials used by the employer UI into the
// account pool. The UID-derived ID makes this converge with an independently
// scanned pool account instead of creating a duplicate.
func (s *ClusterService) SyncMainAccount() error {
	s.mainAccountMu.Lock()
	defer s.mainAccountMu.Unlock()
	if s.catalog == nil {
		return fmt.Errorf("catalog client is unavailable")
	}
	jar, ok := s.catalog.GetCookieJar().(*cookiejar.Jar)
	if !ok {
		return fmt.Errorf("main account cookie jar cannot be exported")
	}
	credentials := credentialsFrom(s.catalog, jar, domain.Credentials{})
	if credentials.Cookies["SESSDATA"] == "" || credentials.Cookies["bili_jct"] == "" {
		return fmt.Errorf("main account is not logged in")
	}
	info, err := s.catalog.GetAccountStatus()
	if err != nil {
		return err
	}
	if info == nil || !info.Login || info.UID == 0 {
		return fmt.Errorf("main account is not logged in")
	}
	ctx := context.Background()
	accountID := fmt.Sprintf("bili-%d", info.UID)
	account := domain.Account{ID: accountID, Name: info.Name, Role: domain.RolePrimary, Enabled: true, Credentials: credentials}
	if existing, existingErr := s.repository.Account(ctx, accountID); existingErr == nil {
		account.Name = existing.Name
		if account.Name == "" {
			account.Name = info.Name
		}
		account.Role = existing.Role
		account.Enabled = existing.Enabled
		account.CooldownUntil = existing.CooldownUntil
		account.Credentials.Version = existing.Credentials.Version + 1
	} else if !errors.Is(existingErr, sql.ErrNoRows) {
		return existingErr
	} else {
		account.Credentials.Version = 1
	}
	if err := s.repository.PutAccount(ctx, account, nil); err != nil {
		return err
	}
	// Once the UID is known, the anonymous legacy migration row is obsolete.
	if err := s.repository.DeleteAccount(ctx, "migrated-account"); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, _ = s.accounts.SyncBuyers(ctx, accountID)
	return s.refreshResources(ctx)
}

type SKUInspection struct {
	EventDay       string                `json:"eventDay"`
	OrderCapacity  int                   `json:"orderCapacity"`
	CapacitySource domain.CapacitySource `json:"capacitySource"`
	SaleStart      time.Time             `json:"saleStart"`
	SaleEnd        time.Time             `json:"saleEnd"`
}

type ProjectCatalog struct {
	ID            string       `json:"id"`
	Name          string       `json:"name"`
	ForceRealName bool         `json:"forceRealName"`
	IDBind        int          `json:"idBind"`
	Start         time.Time    `json:"start"`
	End           time.Time    `json:"end"`
	Tickets       []CatalogSKU `json:"tickets"`
}

type CatalogSKU struct {
	ScreenID      int64     `json:"screenId"`
	SKUID         int64     `json:"skuId"`
	ScreenName    string    `json:"screenName"`
	SKUName       string    `json:"skuName"`
	Price         int       `json:"price"`
	Status        string    `json:"status"`
	EventTime     time.Time `json:"eventTime"`
	SaleStart     time.Time `json:"saleStart"`
	SaleEnd       time.Time `json:"saleEnd"`
	OrderCapacity int       `json:"orderCapacity"`
}

func (s *ClusterService) LoadProject(projectID string) (ProjectCatalog, error) {
	if s.catalog == nil {
		return ProjectCatalog{}, fmt.Errorf("catalog client is unavailable")
	}
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return ProjectCatalog{}, fmt.Errorf("project id is required")
	}
	info, err := s.catalog.GetProjectInformationNew(projectID)
	if err != nil {
		return ProjectCatalog{}, err
	}
	tickets, err := s.catalog.GetTicketSkuIDsByProjectIDNew(projectID)
	if err != nil {
		return ProjectCatalog{}, err
	}
	result := ProjectCatalog{ID: info.ProjectID, Name: info.ProjectName, ForceRealName: info.IsForceRealName, IDBind: info.IDBind, Start: info.StartTime, End: info.EndTime}
	for _, ticket := range tickets {
		capacity := ticket.BuyLimit
		if capacity <= 0 {
			capacity = 4
		}
		result.Tickets = append(result.Tickets, CatalogSKU{ScreenID: ticket.ScreenID, SKUID: ticket.SkuID, ScreenName: ticket.Name, SKUName: ticket.Desc, Price: ticket.Price, Status: ticket.Flags.DisplayName, EventTime: ticket.EventTime, SaleStart: ticket.SaleStat.Start, SaleEnd: ticket.SaleStat.End, OrderCapacity: capacity})
	}
	return result, nil
}

func (s *ClusterService) InspectSKU(projectID, screenID, skuID int64) (SKUInspection, error) {
	if s.catalog == nil {
		return SKUInspection{}, fmt.Errorf("catalog client is unavailable")
	}
	items, err := s.catalog.GetTicketSkuIDsByProjectIDNew(fmt.Sprint(projectID))
	if err != nil {
		return SKUInspection{}, err
	}
	for _, item := range items {
		if item.ScreenID == screenID && item.SkuID == skuID {
			capacity, source := item.BuyLimit, domain.CapacityAPI
			if capacity <= 0 {
				capacity, source = 4, domain.CapacityDefault
			}
			eventDay := ""
			if !item.EventTime.IsZero() {
				eventDay = item.EventTime.Format("2006-01-02")
			}
			return SKUInspection{EventDay: eventDay, OrderCapacity: capacity, CapacitySource: source, SaleStart: item.SaleStat.Start, SaleEnd: item.SaleStat.End}, nil
		}
	}
	return SKUInspection{}, fmt.Errorf("SKU not found")
}

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
		if service.notify != nil {
			service.notify(fmt.Sprintf("购票成功：Intent %s，订单 %s", intent.ID, result.OrderID))
		}
		service.openPayQRWindow(intent, result)
	})
	return service
}

func (s *ClusterService) SetNotifier(notify func(string)) { s.notify = notify }

func (s *ClusterService) SetApp(app *application.App) { s.wailsApp = app }

// isLocalWorkerID returns true for worker IDs managed in-process (starts
// with "local").
func isLocalWorkerID(id string) bool { return strings.HasPrefix(id, "local") }

func (s *ClusterService) openPayQRWindow(intent domain.LogicalOrderIntent, result domain.ExecutionResult) {
	if s.wailsApp == nil || result.PaymentURL == "" {
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
	window := s.wailsApp.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "支付二维码",
		BackgroundColour: application.RGBA{Red: 27, Green: 38, Blue: 54, Alpha: 255},
		URL:              "/#/pay-qr?" + values.Encode(),
	})
	window.Show()
	window.Center()
	window.Focus()
}

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
	var localNode domain.WorkerNode
	localReady := false
	for _, node := range workers {
		if node.ID == "local" && s.client.IsHealthy(node.ID) {
			localNode, localReady = node, true
			break
		}
	}
	var localErr error
	if !localReady {
		localNode, localErr = s.local.Start(ctx, s.client, employer.LocalWorkerOptions{PluginDir: "plugins", CaptchaPlugin: pluginName, Version: global.GitCommit})
	}
	if localErr == nil {
		if err := s.repository.PutWorker(ctx, localNode); err != nil {
			return err
		}
		// Persist TLS config for the primary local worker.
		tlsBundle, _, tlsErr := clusterworker.LoadOrGenerateLocalTLS("data/local-worker")
		if tlsErr == nil {
			_ = s.repository.PutWorkerTLS(ctx, "local", domain.WorkerTLSConfig{
				CACertPEM:     tlsBundle.CAPEM,
				ClientCertPEM: tlsBundle.CertPEM,
				ClientKeyPEM:  tlsBundle.KeyPEM,
				ServerName:    "localhost",
			})
		}

		// Recover all other local workers persisted in the repository.
		for _, node := range workers {
			if node.ID == "local" || !isLocalWorkerID(node.ID) {
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
	} else {
		log.Printf("[cluster] local worker unavailable: %v", localErr)
		filtered := workers[:0]
		for _, node := range workers {
			if node.ID != "local" {
				filtered = append(filtered, node)
			}
		}
		workers = filtered
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
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = s.refreshResources(ctx)
				_ = s.dispatcher.Reconcile(ctx)
			}
		}
	}()
	return nil
}

func (s *ClusterService) Close() {
	if s.cancel != nil {
		s.cancel()
	}
	_ = s.local.Stop()
	_ = s.repository.Close()
}

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

// StopMacro stops all active attempts belonging to a macro and disarms its intents.
func (s *ClusterService) StopMacro(macroID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	workerList, err := s.repository.ListWorkers(ctx)
	if err != nil {
		return err
	}
	workerByID := make(map[string]domain.WorkerNode, len(workerList))
	for _, w := range workerList {
		workerByID[w.ID] = w
	}
	// Send stop to all active worker attempts.
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
	// Force-disarm the macro: mark intents terminal and release resources.
	s.dispatcher.DisarmMacro(macroID)
	return nil
}

func (s *ClusterService) refreshResources(ctx context.Context) error {
	accountList, err := s.repository.ListAccounts(ctx)
	if err != nil {
		return err
	}
	workers, err := s.repository.ListWorkers(ctx)
	if err != nil {
		return err
	}
	dispatchWorkers := make([]domain.WorkerNode, len(workers))
	copy(dispatchWorkers, workers)
	var wg sync.WaitGroup
	for i := range dispatchWorkers {
		i := i
		node := dispatchWorkers[i]
		if !node.Enabled {
			continue
		}
		if s.client.IsHealthy(node.ID) {
			s.dispatcher.MarkWorkerHealthy(node.ID)
			continue
		}
		if s.client.IsDisconnected(node.ID) {
			dispatchWorkers[i].Enabled = false
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			healthCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			info, err := s.client.Health(healthCtx, node)
			cancel()
			if err != nil {
				log.Printf("[cluster] health check failed for worker %s (%s): %v", node.ID, node.Address, err)
				dispatchWorkers[i].Enabled = false
				return
			}
			log.Printf("[cluster] worker %s connected (version=%s, plugin=%s)", node.ID, info["version"], info["pluginVersion"])
			if s.client.IsHealthy(node.ID) {
				s.dispatcher.MarkWorkerHealthy(node.ID)
			}
		}()
	}
	wg.Wait()
	for i := range dispatchWorkers {
		if dispatchWorkers[i].Enabled && !s.client.IsHealthy(dispatchWorkers[i].ID) {
			dispatchWorkers[i].Enabled = false
		}
	}
	s.dispatcher.SetResources(accountList, dispatchWorkers)

	return nil
}

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
	result := ClusterSnapshot{TaskGroups: taskGroups, Buyers: buyersWithAccounts}
	for _, account := range accountList {
		summary := AccountSummary{ID: account.ID, Name: account.Name, Role: account.Role, Enabled: account.Enabled, CredentialVersion: account.Credentials.Version}
		if !account.CooldownUntil.IsZero() {
			cooldown := account.CooldownUntil
			summary.CooldownUntil = &cooldown
		}
		result.Accounts = append(result.Accounts, summary)
	}
	for _, node := range workerList {
		summary := WorkerSummary{ID: node.ID, Name: node.Name, Address: node.Address, Type: node.Type, Role: node.Role, Enabled: node.Enabled}
		summary.Healthy = s.client.IsHealthy(node.ID)
		if summary.Healthy {
			// Fetch additional metadata via gRPC Health call (best-effort).
			if hb, ok := s.client.LastHeartbeat(node.ID); ok {
				_ = hb
			}
			healthCtx, healthCancel := context.WithTimeout(ctx, 800*time.Millisecond)
			health, healthErr := s.client.Health(healthCtx, node)
			healthCancel()
			if healthErr == nil {
				summary.ActiveAttemptID, _ = health["activeAttemptId"].(string)
				summary.Version, _ = health["version"].(string)
			}
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
	for _, value := range s.dispatcher.Attempts() {
		result.Attempts = append(result.Attempts, AttemptSummary{ID: value.ID, IntentID: value.IntentID, AccountID: value.AccountID, WorkerID: value.WorkerID, State: value.State, OrderID: value.Result.OrderID, PaymentURL: value.Result.PaymentURL, Reason: value.Result.Reason})
	}
	return result, nil
}

type accountLoginSession struct {
	Client    *biliutils.BiliClient
	Jar       *cookiejar.Jar
	QRCodeKey string
	Name      string
	Role      domain.ResourceRole
	CreatedAt time.Time
}

type AccountLoginStart struct {
	SessionID string `json:"sessionId"`
	URL       string `json:"url"`
}
type AccountLoginPoll struct {
	Code      int    `json:"code"`
	Message   string `json:"message"`
	AccountID string `json:"accountId,omitempty"`
}

func (s *ClusterService) BeginAccountLogin(name string, role domain.ResourceRole) (AccountLoginStart, error) {
	jar := cookiejar.New(nil)
	client, err := biliutils.NewBiliClientWithCookiejar(jar)
	if err != nil {
		return AccountLoginStart{}, err
	}
	qr, err := client.GetQRCodeUrlAndKey()
	if err != nil {
		return AccountLoginStart{}, err
	}
	if role == "" {
		role = domain.RolePrimary
	}
	id := randomClusterID("login")
	s.mu.Lock()
	s.loginSessions[id] = &accountLoginSession{Client: client, Jar: jar, QRCodeKey: qr.QRCodeKey, Name: name, Role: role, CreatedAt: time.Now()}
	s.mu.Unlock()
	return AccountLoginStart{SessionID: id, URL: qr.URL}, nil
}

func (s *ClusterService) PollAccountLogin(sessionID string) (AccountLoginPoll, error) {
	s.mu.RLock()
	session := s.loginSessions[sessionID]
	s.mu.RUnlock()
	if session == nil {
		return AccountLoginPoll{}, fmt.Errorf("login session not found")
	}
	if time.Since(session.CreatedAt) > 5*time.Minute {
		s.mu.Lock()
		delete(s.loginSessions, sessionID)
		s.mu.Unlock()
		return AccountLoginPoll{}, fmt.Errorf("login session expired")
	}
	state, err := session.Client.GetQRLoginState(session.QRCodeKey)
	if err != nil {
		return AccountLoginPoll{}, err
	}
	result := AccountLoginPoll{Code: state.Code, Message: state.Message}
	if state.Code != 0 {
		return result, nil
	}
	session.Client.SetRefreshToken(state.RefreshToken)
	info, err := session.Client.GetAccountStatus()
	if err != nil {
		return result, err
	}
	profile, _ := json.Marshal(session.Client.ExportDeviceProfile())
	credentials := credentialsFrom(session.Client, session.Jar, domain.Credentials{RefreshToken: state.RefreshToken, Version: 1, DeviceProfile: profile})
	accountID := fmt.Sprintf("bili-%d", info.UID)
	name := session.Name
	if name == "" {
		name = info.Name
	}
	account := domain.Account{ID: accountID, Name: name, Role: session.Role, Enabled: true, Credentials: credentials}
	if err := s.repository.PutAccount(context.Background(), account, nil); err != nil {
		return result, err
	}
	// Best-effort import of the account's existing buyers. Login remains
	// successful if Bilibili's buyer endpoint is temporarily unavailable.
	_, _ = s.accounts.SyncBuyers(context.Background(), accountID)
	s.mu.Lock()
	delete(s.loginSessions, sessionID)
	s.mu.Unlock()
	result.AccountID = accountID
	_ = s.refreshResources(context.Background())
	return result, nil
}

func randomClusterID(prefix string) string {
	var value [12]byte
	_, _ = rand.Read(value[:])
	return prefix + "-" + hex.EncodeToString(value[:])
}

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

func (s *ClusterService) DeleteTaskGroup(id string) error {
	if err := s.repository.DeleteTaskGroup(context.Background(), id); err != nil {
		return err
	}
	return s.refreshResources(context.Background())
}

func (s *ClusterService) ImportAccount(document string) error {
	_, err := s.accounts.Import(context.Background(), []byte(document))
	if err == nil {
		err = s.refreshResources(context.Background())
	}
	return err
}

func (s *ClusterService) SyncAccountBuyers(accountID string) ([]domain.Buyer, error) {
	buyers, err := s.accounts.SyncBuyers(context.Background(), accountID)
	if err != nil {
		return nil, err
	}
	if err := s.refreshResources(context.Background()); err != nil {
		return nil, err
	}
	return buyers, nil
}

// SyncAllAccountBuyers syncs buyers from every enabled account and ensures
// the logical_buyers table retains the most complete (unmasked) real-name
// information. The same real person on multiple accounts is matched and
// deduplicated into a single logical buyer entry.
func (s *ClusterService) SyncAllAccountBuyers() ([]domain.Buyer, error) {
	buyers, err := s.accounts.SyncAllBuyers(context.Background())
	if err != nil {
		return nil, err
	}
	if err := s.refreshResources(context.Background()); err != nil {
		return nil, err
	}
	return buyers, nil
}

func (s *ClusterService) DeleteAccount(accountID string) error {
	for _, attempt := range s.dispatcher.Attempts() {
		if attempt.AccountID == accountID && !attempt.State.Terminal() {
			return fmt.Errorf("account is used by active attempt %s", attempt.ID)
		}
	}
	if err := s.repository.DeleteAccount(context.Background(), accountID); err != nil {
		return err
	}
	return s.refreshResources(context.Background())
}

func (s *ClusterService) AddWorker(document string) error {
	var input struct {
		ID            string              `json:"id"`
		Name          string              `json:"name"`
		Address       string              `json:"address"`
		CACert        string              `json:"caCert"`
		ClientCert    string              `json:"clientCert"`
		ClientKey     string              `json:"clientKey"`
		TLSServerName string              `json:"tlsServerName"`
		Role          domain.ResourceRole `json:"role"`
	}
	if err := json.Unmarshal([]byte(document), &input); err != nil {
		return err
	}
	if input.ID == "" || input.Address == "" || input.ClientKey == "" {
		return fmt.Errorf("id, address and clientKey are required")
	}
	if input.Role == "" {
		input.Role = domain.RolePrimary
	}
	node := domain.WorkerNode{
		ID:            input.ID,
		Name:          input.Name,
		Address:       input.Address,
		Type:          domain.WorkerTypeRemote,
		Role:          input.Role,
		Enabled:       true,
		TLSServerName: input.TLSServerName,
	}
	tlsConfig := domain.WorkerTLSConfig{
		CACertPEM:     []byte(input.CACert),
		ClientCertPEM: []byte(input.ClientCert),
		ClientKeyPEM:  []byte(input.ClientKey),
		ServerName:    input.TLSServerName,
	}
	if err := s.client.SetTLSFromConfig(node.ID, tlsConfig); err != nil {
		return fmt.Errorf("invalid TLS config: %w", err)
	}
	ctx := context.Background()
	if err := s.repository.PutWorker(ctx, node); err != nil {
		return err
	}
	if err := s.repository.PutWorkerTLS(ctx, node.ID, tlsConfig); err != nil {
		return err
	}
	if err := s.refreshResources(ctx); err != nil {
		return err
	}

	// Synchronously dial the new worker so connection errors surface
	// immediately instead of waiting for the async health check.
	healthCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := s.client.Health(healthCtx, node); err != nil {
		log.Printf("[cluster] health check for new worker %s (%s): %v", node.ID, node.Address, err)
		return fmt.Errorf("worker saved but unreachable: %w", err)
	}
	log.Printf("[cluster] worker %s connected (%s)", node.ID, node.Address)
	return nil
}

// UpdateWorker updates the connection settings (address, TLS, role) for an
// existing worker. The worker must not have active attempts. Accepts the
// same JSON document shape as AddWorker.
func (s *ClusterService) UpdateWorker(document string) error {
	var input struct {
		ID            string              `json:"id"`
		Name          string              `json:"name"`
		Address       string              `json:"address"`
		CACert        string              `json:"caCert"`
		ClientCert    string              `json:"clientCert"`
		ClientKey     string              `json:"clientKey"`
		TLSServerName string              `json:"tlsServerName"`
		Role          domain.ResourceRole `json:"role"`
	}
	if err := json.Unmarshal([]byte(document), &input); err != nil {
		return err
	}
	if input.ID == "" || input.Address == "" {
		return fmt.Errorf("id and address are required")
	}
	if input.ID == "local" {
		return fmt.Errorf("the automatically managed local worker cannot be edited")
	}
	if input.Role == "" {
		input.Role = domain.RolePrimary
	}
	// Block if the worker is executing an active attempt.
	for _, attempt := range s.dispatcher.Attempts() {
		if attempt.WorkerID == input.ID && !attempt.State.Terminal() {
			return fmt.Errorf("worker is used by active attempt %s", attempt.ID)
		}
	}
	node := domain.WorkerNode{
		ID:            input.ID,
		Name:          input.Name,
		Address:       input.Address,
		Type:          domain.WorkerTypeRemote,
		Role:          input.Role,
		Enabled:       true,
		TLSServerName: input.TLSServerName,
	}
	ctx := context.Background()
	tlsConfig := domain.WorkerTLSConfig{
		CACertPEM:     []byte(input.CACert),
		ClientCertPEM: []byte(input.ClientCert),
		ClientKeyPEM:  []byte(input.ClientKey),
		ServerName:    input.TLSServerName,
	}
	// If no TLS credentials were provided, retain the existing TLS config.
	if input.ClientKey == "" {
		existingTLS, err := s.repository.WorkerTLS(ctx, input.ID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("fetch existing TLS config: %w", err)
		}
		if err == nil && len(existingTLS.ClientKeyPEM) > 0 {
			tlsConfig = existingTLS
		}
	}
	// Close existing connection before applying new TLS config.
	s.client.RemoveTLS(input.ID)
	if err := s.client.SetTLSFromConfig(node.ID, tlsConfig); err != nil {
		return fmt.Errorf("invalid TLS config: %w", err)
	}
	if err := s.repository.PutWorker(ctx, node); err != nil {
		return err
	}
	if err := s.repository.PutWorkerTLS(ctx, node.ID, tlsConfig); err != nil {
		return err
	}
	if err := s.refreshResources(ctx); err != nil {
		return err
	}

	// Synchronously dial the updated worker so connection errors surface
	// immediately instead of waiting for the async health check.
	healthCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := s.client.Health(healthCtx, node); err != nil {
		log.Printf("[cluster] health check for updated worker %s (%s): %v", node.ID, node.Address, err)
		return fmt.Errorf("worker updated but unreachable: %w", err)
	}
	log.Printf("[cluster] worker %s reconnected (%s)", node.ID, node.Address)
	return nil
}

// AddLocalWorker creates and starts a new in-process local worker with
// the given ID, name and listen address. If id is empty, one is generated.
func (s *ClusterService) AddLocalWorker(id, name, listen string) error {
	ctx := context.Background()
	pluginName := ""
	if _, statErr := os.Stat("plugins/captcha-plugin"); statErr == nil {
		pluginName = "captcha-plugin"
	}
	node, err := s.local.AddWorker(ctx, s.client, id, name, listen, employer.LocalWorkerOptions{
		PluginDir:     "plugins",
		CaptchaPlugin: pluginName,
		Version:       global.GitCommit,
	})
	if err != nil {
		return err
	}
	if err := s.repository.PutWorker(ctx, node); err != nil {
		return err
	}
	tlsBundle, _, tlsErr := clusterworker.LoadOrGenerateLocalTLS("data/" + node.ID)
	if tlsErr == nil {
		_ = s.repository.PutWorkerTLS(ctx, node.ID, domain.WorkerTLSConfig{
			CACertPEM:     tlsBundle.CAPEM,
			ClientCertPEM: tlsBundle.CertPEM,
			ClientKeyPEM:  tlsBundle.KeyPEM,
			ServerName:    "localhost",
		})
	}
	return s.refreshResources(ctx)
}

// StartLocalWorker starts (or restarts) an existing local worker by ID.
func (s *ClusterService) StartLocalWorker(workerID string) error {
	ctx := context.Background()
	pluginName := ""
	if _, statErr := os.Stat("plugins/captcha-plugin"); statErr == nil {
		pluginName = "captcha-plugin"
	}
	node, err := s.local.StartWorker(ctx, s.client, workerID, employer.LocalWorkerOptions{
		PluginDir:     "plugins",
		CaptchaPlugin: pluginName,
		Version:       global.GitCommit,
	})
	if err != nil {
		return err
	}
	if err := s.repository.PutWorker(ctx, node); err != nil {
		return err
	}
	tlsBundle, _, tlsErr := clusterworker.LoadOrGenerateLocalTLS("data/" + workerID)
	if tlsErr == nil {
		_ = s.repository.PutWorkerTLS(ctx, node.ID, domain.WorkerTLSConfig{
			CACertPEM:     tlsBundle.CAPEM,
			ClientCertPEM: tlsBundle.CertPEM,
			ClientKeyPEM:  tlsBundle.KeyPEM,
			ServerName:    "localhost",
		})
	}
	return s.refreshResources(ctx)
}

// StopLocalWorker stops a local in-process worker.
func (s *ClusterService) StopLocalWorker(workerID string) error {
	if err := s.local.StopWorker(workerID); err != nil {
		return err
	}
	// Mark the worker as disabled in the repository so the dispatcher
	// stops assigning work to it.
	node, err := s.repository.Worker(context.Background(), workerID)
	if err != nil {
		return err
	}
	node.Enabled = false
	if err := s.repository.PutWorker(context.Background(), node); err != nil {
		return err
	}
	return s.refreshResources(context.Background())
}

// WorkerConfigResponse returns the full configuration for a worker, suitable
// for pre-filling an edit form.
type WorkerConfigResponse struct {
	ID            string              `json:"id"`
	Name          string              `json:"name"`
	Address       string              `json:"address"`
	Role          domain.ResourceRole `json:"role"`
	CACert        string              `json:"caCert"`
	ClientCert    string              `json:"clientCert"`
	ClientKey     string              `json:"clientKey"`
	TLSServerName string              `json:"tlsServerName"`
}

// GetWorkerConfig reads the full connection settings for a worker (node info
// plus TLS PEM material) so that the frontend can pre-fill the edit form.
func (s *ClusterService) GetWorkerConfig(workerID string) (WorkerConfigResponse, error) {
	ctx := context.Background()
	node, err := s.repository.Worker(ctx, workerID)
	if err != nil {
		return WorkerConfigResponse{}, fmt.Errorf("worker %s not found: %w", workerID, err)
	}
	tlsConfig, err := s.repository.WorkerTLS(ctx, workerID)
	if err != nil {
		return WorkerConfigResponse{}, fmt.Errorf("TLS config for worker %s not found: %w", workerID, err)
	}
	return WorkerConfigResponse{
		ID:            node.ID,
		Name:          node.Name,
		Address:       node.Address,
		Role:          node.Role,
		CACert:        string(tlsConfig.CACertPEM),
		ClientCert:    string(tlsConfig.ClientCertPEM),
		ClientKey:     string(tlsConfig.ClientKeyPEM),
		TLSServerName: node.TLSServerName,
	}, nil
}

func (s *ClusterService) DeleteWorker(workerID string) error {
	if workerID == "local" {
		return fmt.Errorf("the automatically managed local worker cannot be deleted")
	}
	for _, attempt := range s.dispatcher.Attempts() {
		if attempt.WorkerID == workerID && !attempt.State.Terminal() {
			return fmt.Errorf("worker is used by active attempt %s", attempt.ID)
		}
	}
	// If this is a local in-process worker, stop it first.
	_ = s.local.RemoveWorker(workerID)
	if err := s.repository.DeleteWorker(context.Background(), workerID); err != nil {
		return err
	}
	s.client.RemoveTLS(workerID)
	return s.refreshResources(context.Background())
}

// DisconnectWorker closes the gRPC connection to a worker (keeping the TLS
// config so it can be reconnected later).
func (s *ClusterService) DisconnectWorker(workerID string) error {
	if workerID == "local" {
		return fmt.Errorf("the local worker cannot be disconnected")
	}
	s.client.Disconnect(workerID)
	return s.refreshResources(context.Background())
}

// ReconnectWorker re-establishes the gRPC connection to a worker and verifies
// it with a health check.  Retries up to 5 times with 5s intervals.
func (s *ClusterService) ReconnectWorker(workerID string) error {
	if workerID == "local" {
		return fmt.Errorf("the local worker is auto-managed")
	}
	ctx := context.Background()
	node, err := s.repository.Worker(ctx, workerID)
	if err != nil {
		return fmt.Errorf("worker %s not found: %w", workerID, err)
	}
	// Close any stale connection first.
	s.client.Disconnect(workerID)

	const maxRetries = 5
	const retryInterval = 5 * time.Second
	var lastErr error
	for i := 0; i < maxRetries; i++ {
		healthCtx, cancel := context.WithTimeout(ctx, retryInterval)
		_, err := s.client.Health(healthCtx, node)
		cancel()
		if err == nil {
			log.Printf("[cluster] worker %s reconnected (attempt %d)", workerID, i+1)
			return s.refreshResources(ctx)
		}
		lastErr = err
		log.Printf("[cluster] reconnect worker %s attempt %d/%d failed: %v", workerID, i+1, maxRetries, err)
		if i < maxRetries-1 {
			time.Sleep(retryInterval)
		}
	}
	return fmt.Errorf("reconnect failed after %d attempts: %w", maxRetries, lastErr)
}

// GenerateRemoteWorkerConfigResponse is returned by GenerateRemoteWorkerConfig.
type GenerateRemoteWorkerConfigResponse struct {
	EncodedConfig string `json:"encodedConfig"` // Base4096 string for the worker
	WorkerID      string `json:"workerId"`
	Listen        string `json:"listen"`
}

// GenerateRemoteWorkerConfig creates TLS material and a complete configuration
// for a remote worker, then encodes it as a Base4096 string for copy-paste
// distribution.  Only the employer-side TLS credentials are stored so that
// mTLS works once the worker comes online; the worker is **not** added to
// the repository list (the user must add it manually via AddWorker after
// the worker is deployed).
//
// Parameters:
//   - workerID: unique identifier for the worker (e.g. "home-server")
//   - listen: address the worker will listen on (e.g. "0.0.0.0:18080")
//   - hosts: comma-separated list of DNS names / IPs for the server TLS cert
//     (e.g. "myworker.example.com,192.168.1.100")
func (s *ClusterService) GenerateRemoteWorkerConfig(workerID, listen, hosts string) (GenerateRemoteWorkerConfigResponse, error) {
	if workerID == "" {
		return GenerateRemoteWorkerConfigResponse{}, fmt.Errorf("workerId is required")
	}
	if listen == "" {
		listen = "0.0.0.0:18080"
	}
	hostList := strings.Split(hosts, ",")
	filtered := hostList[:0]
	for _, h := range hostList {
		h = strings.TrimSpace(h)
		if h != "" {
			filtered = append(filtered, h)
		}
	}
	hostList = filtered
	if len(hostList) == 0 {
		hostList = []string{"localhost", "127.0.0.1"}
	}

	// Load the employer's persistent CA and client certificate from disk.
	// These are auto-generated by LoadOrGenerateLocalTLS on startup and
	// stored in data/local-worker/.  Reusing them gives the employer a
	// single identity across all remote workers.
	employerTLSBundle, _, tlsLoadErr := clusterworker.LoadOrGenerateLocalTLS("data/local-worker")
	if tlsLoadErr != nil {
		return GenerateRemoteWorkerConfigResponse{}, fmt.Errorf("load employer TLS: %w", tlsLoadErr)
	}
	caCertPEM := employerTLSBundle.CAPEM
	clientCertPEM := employerTLSBundle.CertPEM
	clientKeyPEM := employerTLSBundle.KeyPEM

	// Load the CA private key (needed for signing server certs).
	caKeyPEM, caKeyErr := os.ReadFile("data/local-worker/ca-key.pem")
	if caKeyErr != nil {
		return GenerateRemoteWorkerConfigResponse{}, fmt.Errorf("read employer CA key: %w", caKeyErr)
	}

	rc, bundle, err := clusterworker.GenerateRemoteWorkerConfig(
		caCertPEM, caKeyPEM, clientCertPEM, clientKeyPEM,
		hostList, workerID,
		clusterworker.RemoteWorkerOptions{
			Listen:          listen,
			WorkerID:        workerID,
			DataDir:         "data/worker",
			PollIntervalSec: 15,
			CalibrateClock:  true,
		},
	)
	if err != nil {
		return GenerateRemoteWorkerConfigResponse{}, fmt.Errorf("generate remote worker config: %w", err)
	}

	encoded, err := rc.Encode()
	if err != nil {
		return GenerateRemoteWorkerConfigResponse{}, fmt.Errorf("encode config: %w", err)
	}

	// Register the employer-side TLS credentials both in-memory and in the
	// repository so the worker can be added later via AddWorkerFromEncodedConfig.
	tlsConfig := domain.WorkerTLSConfig{
		CACertPEM:     bundle.CAPEM,
		ClientCertPEM: bundle.CertPEM,
		ClientKeyPEM:  bundle.KeyPEM,
		ServerName:    bundle.ServerName,
	}
	// Keep TLS in memory only — the worker row does not exist yet in the
	// workers table, so PutWorkerTLS would fail with an FK constraint.
	// Persistence happens later when the worker is actually added via
	// AddWorker or AddWorkerFromEncodedConfig.
	if err := s.client.SetTLSFromConfig(workerID, tlsConfig); err != nil {
		return GenerateRemoteWorkerConfigResponse{}, fmt.Errorf("set worker TLS: %w", err)
	}

	return GenerateRemoteWorkerConfigResponse{
		EncodedConfig: encoded,
		WorkerID:      workerID,
		Listen:        listen,
	}, nil
}

// AddWorkerFromEncodedConfig decodes a Base4096-encoded worker configuration
// and adds the worker to the repository.  The encoded string carries both
// the worker-side and employer-side TLS material, so no prior TLS setup
// is required — it is extracted directly from the config.
//
// overrideAddress, when non-empty, overrides the dial address (rc.Listen)
// embedded in the encoded config.  This allows the employer to specify the
// real reachable IP:port of the worker, which may differ from the listen
// address the worker was configured with (e.g. 0.0.0.0:18080 vs the actual
// public IP).
func (s *ClusterService) AddWorkerFromEncodedConfig(encodedConfig string, overrideAddress string) error {
	rc, err := clusterworker.DecodeRemoteWorkerConfig(encodedConfig)
	if err != nil {
		return fmt.Errorf("decode worker config: %w", err)
	}
	if rc.WorkerID == "" || rc.Listen == "" {
		return fmt.Errorf("worker config missing required fields (workerId, listen)")
	}
	if rc.WorkerID == "local" {
		return fmt.Errorf("cannot import the local worker")
	}
	ctx := context.Background()

	// Determine the dial address.  Prefer the override supplied by the
	// employer (the real IP:port of the employee's machine); fall back to
	// the listen address embedded in the config.
	address := rc.Listen
	if overrideAddress != "" {
		address = overrideAddress
	}

	// Build TLS config.  Prefer the employer fields embedded in the encoded
	// string (populated by GenerateRemoteWorkerConfig).  Fall back to the
	// repository for configs generated before employer fields were added.
	var tlsConfig domain.WorkerTLSConfig
	if rc.EmployerCertPEM != "" && rc.EmployerKeyPEM != "" {
		tlsConfig = domain.WorkerTLSConfig{
			CACertPEM:     []byte(rc.CACertPEM),
			ClientCertPEM: []byte(rc.EmployerCertPEM),
			ClientKeyPEM:  []byte(rc.EmployerKeyPEM),
		}
	} else {
		stored, tlsErr := s.repository.WorkerTLS(ctx, rc.WorkerID)
		if tlsErr != nil {
			return fmt.Errorf("no TLS credentials found for worker %q — the encoded config is too old (missing employer fields); run 'Generate Remote Worker Config' first to create a new config, or add the worker manually with CA/client cert/key", rc.WorkerID)
		}
		tlsConfig = stored
	}

	// Set TLS server name from the worker node or derive from config.
	if tlsConfig.ServerName == "" {
		// Use the worker ID as a fallback SNI hostname.
		tlsConfig.ServerName = rc.WorkerID
	}

	if err := s.client.SetTLSFromConfig(rc.WorkerID, tlsConfig); err != nil {
		return fmt.Errorf("apply TLS config: %w", err)
	}

	node := domain.WorkerNode{
		ID:            rc.WorkerID,
		Address:       address,
		Type:          domain.WorkerTypeRemote,
		Role:          domain.RolePrimary,
		Enabled:       true,
		TLSServerName: tlsConfig.ServerName,
	}
	if err := s.repository.PutWorker(ctx, node); err != nil {
		return err
	}
	if err := s.repository.PutWorkerTLS(ctx, rc.WorkerID, tlsConfig); err != nil {
		return err
	}
	if err := s.refreshResources(ctx); err != nil {
		return err
	}

	// Synchronously dial the imported worker so connection errors surface
	// immediately instead of waiting for the async health check.
	healthCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := s.client.Health(healthCtx, node); err != nil {
		log.Printf("[cluster] health check for imported worker %s (%s): %v", node.ID, node.Address, err)
		return fmt.Errorf("worker imported but unreachable: %w", err)
	}
	log.Printf("[cluster] worker %s connected (%s)", node.ID, node.Address)
	return nil
}

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
	if value.DesiredReplicas <= 0 {
		value.DesiredReplicas = 1
	}
	if value.HardConcurrency <= 0 {
		value.HardConcurrency = value.DesiredReplicas
	}
	if value.DesiredReplicas > value.HardConcurrency {
		return fmt.Errorf("desired replicas cannot exceed hard concurrency")
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
		return err
	}
	return s.dispatcher.RemoveMacro(macroID)
}

func (s *ClusterService) StartMacro(macroID string) error {
	if err := s.refreshResources(context.Background()); err != nil {
		return err
	}
	return s.planAndStart(context.Background(), macroID, domain.PhasePunctual, true)
}

func (s *ClusterService) StartTaskGroup(taskGroupID string) error {
	ctx := context.Background()
	if err := s.refreshResources(ctx); err != nil {
		return err
	}
	macros, err := s.repository.ListMacroTasks(ctx)
	if err != nil {
		return err
	}
	selected := make([]domain.MacroTask, 0)
	for _, macro := range macros {
		if macro.TaskGroupID == taskGroupID {
			selected = append(selected, macro)
		}
	}
	if len(selected) == 0 {
		return fmt.Errorf("task group has no macro tasks")
	}
	sort.SliceStable(selected, func(i, j int) bool {
		if selected[i].Priority == selected[j].Priority {
			return selected[i].ID < selected[j].ID
		}
		return selected[i].Priority > selected[j].Priority
	})
	var failures []string
	started := 0
	for _, macro := range selected {
		if err := s.planAndStart(ctx, macro.ID, domain.PhasePunctual, false); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", macro.ID, err))
			continue
		}
		started++
	}
	if started == 0 {
		if len(failures) > 0 {
			return fmt.Errorf("no macro task started: %s", strings.Join(failures, "; "))
		}
		return fmt.Errorf("no macro task started")
	}
	if len(failures) > 0 {
		return fmt.Errorf("started %d macro task(s), but some failed: %s", started, strings.Join(failures, "; "))
	}
	return nil
}

func (s *ClusterService) AttemptLogs(attemptID string) ([]clusterworker.LogEntry, error) {
	var selected *domain.ExecutionAttempt
	for _, attempt := range s.dispatcher.Attempts() {
		if attempt.ID == attemptID {
			copy := attempt
			selected = &copy
			break
		}
	}
	if selected == nil {
		return nil, fmt.Errorf("attempt not found")
	}
	workers, err := s.repository.ListWorkers(context.Background())
	if err != nil {
		return nil, err
	}
	for _, node := range workers {
		if node.ID == selected.WorkerID {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			return s.client.Logs(ctx, node, attemptID)
		}
	}
	return nil, fmt.Errorf("worker %s not found", selected.WorkerID)
}

func (s *ClusterService) planAndStart(ctx context.Context, macroID string, phase domain.Phase, requireActive bool) error {
	s.dispatcher.ResumePhase(phase)
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
	groups, err := s.repository.ListPurchaseGroups(ctx, macroID)
	if err != nil {
		return err
	}
	if len(groups) == 0 {
		return fmt.Errorf("macro task has no purchase groups")
	}
	intents, err := planner.Plan(*selected, groups, phase, time.Now())
	if err != nil {
		return err
	}
	if len(intents) == 0 {
		return fmt.Errorf("planner produced no order intents")
	}
	existing, err := s.repository.ListIntents(ctx)
	if err != nil {
		return err
	}
	existingByID := make(map[string]domain.LogicalOrderIntent, len(existing))
	for _, intent := range existing {
		existingByID[intent.ID] = intent
	}
	intentIDs := make(map[string]struct{}, len(intents))
	for _, intent := range intents {
		if previous, ok := existingByID[intent.ID]; ok && previous.Succeeded {
			return fmt.Errorf("order intent %s has already succeeded and cannot be restarted", intent.ID)
		}
		if err := s.repository.PutIntent(ctx, intent); err != nil {
			return err
		}
		s.dispatcher.Add(dispatcher.IntentPlan{Macro: *selected, Intent: intent})
		intentIDs[intent.ID] = struct{}{}
	}
	s.mu.Lock()
	s.phases[macroID] = phase
	s.mu.Unlock()
	if err := s.dispatcher.Reconcile(ctx); err != nil {
		return err
	}
	if requireActive && s.dispatcher.ActiveAttemptsFor(intentIDs) == 0 {
		return fmt.Errorf("task was planned but no attempt started: check deadline, healthy workers, enabled accounts, and buyer mappings")
	}
	return nil
}

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
		if err := s.planAndStart(ctx, macro.ID, domain.PhaseReflow, false); err != nil {
			return err
		}
	}
	return nil
}

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

// SyncBuyerToAccount provisions a logical buyer onto a target Bilibili
// account. If the buyer already exists on that account's real-name list the
// call is a no-op; otherwise a new buyer is created on the remote account.
func (s *ClusterService) SyncBuyerToAccount(logicalBuyerID, targetAccountID string) error {
	buyer, err := s.repository.LogicalBuyer(context.Background(), logicalBuyerID)
	if err != nil {
		return fmt.Errorf("logical buyer %s: %w", logicalBuyerID, err)
	}
	_, err = s.accounts.EnsureBuyer(context.Background(), targetAccountID, buyer, true)
	if err != nil {
		return err
	}
	return s.refreshResources(context.Background())
}

// SyncBuyerToAllAccounts provisions a logical buyer onto every enabled
// Bilibili account that does not already have it. Accounts that already
// contain the buyer are skipped without any remote calls.
func (s *ClusterService) SyncBuyerToAllAccounts(logicalBuyerID string) error {
	buyer, err := s.repository.LogicalBuyer(context.Background(), logicalBuyerID)
	if err != nil {
		return fmt.Errorf("logical buyer %s: %w", logicalBuyerID, err)
	}
	accounts, err := s.repository.ListAccounts(context.Background())
	if err != nil {
		return err
	}
	for _, account := range accounts {
		if !account.Enabled {
			continue
		}
		if _, err := s.repository.BuyerMapping(context.Background(), account.ID, logicalBuyerID); err == nil {
			// Already provisioned on this account — skip.
			continue
		}
		if _, err := s.accounts.EnsureBuyer(context.Background(), account.ID, buyer, true); err != nil {
			return err
		}
	}
	return s.refreshResources(context.Background())
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

type biliProvisioner struct{}

func (biliProvisioner) ListBuyers(_ context.Context, account domain.Account) ([]domain.Buyer, domain.Credentials, error) {
	client, jar, err := accountClient(account)
	if err != nil {
		return nil, account.Credentials, err
	}
	err, list := client.GetRealnameBuyerListNew()
	if err != nil {
		return nil, credentialsFrom(client, jar, account.Credentials), err
	}
	result := make([]domain.Buyer, len(list))
	for i, value := range list {
		buyer := domain.Buyer{BuyerID: value.Id, Name: value.Name, Tel: value.Tel, IDCard: value.IdCard, Type: value.IdType}
		// Fetch full sensitive data (unmasked ID card, phone, etc.) for each buyer.
		if value.Id > 0 {
			sensitiveErr, sensitive := client.GetTargetBuyerSensitiveData(value.Id)
			if sensitiveErr == nil && sensitive.PersonalId != "" {
				buyer.IDCard = sensitive.PersonalId
				if sensitive.Tel != "" {
					buyer.Tel = sensitive.Tel
				}
				if sensitive.Name != "" {
					buyer.Name = sensitive.Name
				}
				if sensitive.IdType != 0 {
					buyer.Type = sensitive.IdType
				}
			}
		}
		result[i] = buyer
	}
	return result, credentialsFrom(client, jar, account.Credentials), nil
}
func (biliProvisioner) CreateBuyer(ctx context.Context, account domain.Account, buyer domain.Buyer) (domain.Buyer, domain.Credentials, error) {
	client, jar, err := accountClient(account)
	if err != nil {
		return domain.Buyer{}, account.Credentials, err
	}
	if err := client.CreateBuyer(buyer.Name, buyer.Tel, buyer.Type, buyer.IDCard, false); err != nil {
		return domain.Buyer{}, credentialsFrom(client, jar, account.Credentials), err
	}
	list, credentials, err := (biliProvisioner{}).ListBuyers(ctx, account)
	if err != nil {
		return domain.Buyer{}, credentials, err
	}
	for _, value := range list {
		if value.Name == buyer.Name && value.Tel == buyer.Tel {
			buyer.BuyerID = value.BuyerID
			return buyer, credentials, nil
		}
	}
	return domain.Buyer{}, credentials, fmt.Errorf("created buyer was not returned by API")
}

func accountClient(account domain.Account) (*biliutils.BiliClient, *cookiejar.Jar, error) {
	jar := cookiejar.New(nil)
	for _, saved := range account.Credentials.CookieJar {
		host := strings.TrimPrefix(saved.Domain, ".")
		if host == "" {
			host = "www.bilibili.com"
		}
		u, _ := url.Parse("https://" + host + "/")
		cookie := &http.Cookie{Name: saved.Name, Value: saved.Value, Domain: saved.Domain, Path: saved.Path, Secure: saved.Secure, HttpOnly: saved.HTTPOnly}
		if saved.Expires > 0 {
			cookie.Expires = time.Unix(saved.Expires, 0)
		}
		jar.SetCookies(u, []*http.Cookie{cookie})
	}
	cookies := make([]*http.Cookie, 0, len(account.Credentials.Cookies))
	for name, value := range account.Credentials.Cookies {
		cookies = append(cookies, &http.Cookie{Name: name, Value: value, Path: "/"})
	}
	for _, raw := range []string{"https://www.bilibili.com/", "https://show.bilibili.com/", "https://passport.bilibili.com/"} {
		u, _ := url.Parse(raw)
		jar.SetCookies(u, cookies)
	}
	var client *biliutils.BiliClient
	var err error
	if len(account.Credentials.DeviceProfile) > 0 {
		var profile biliutils.DeviceProfile
		if decodeErr := json.Unmarshal(account.Credentials.DeviceProfile, &profile); decodeErr != nil {
			return nil, nil, decodeErr
		}
		client, err = biliutils.NewBiliClientWithDeviceProfile(jar, profile)
	} else {
		client, err = biliutils.NewBiliClientWithCookiejar(jar)
	}
	if err != nil {
		return nil, nil, err
	}
	client.SetRefreshToken(account.Credentials.RefreshToken)
	return client, jar, nil
}
func credentialsFrom(client *biliutils.BiliClient, jar *cookiejar.Jar, previous domain.Credentials) domain.Credentials {
	values := make(map[string]string)
	full := make([]domain.HTTPCookie, 0)
	for _, entry := range jar.AllEntries() {
		values[entry.Name] = entry.Value
		full = append(full, domain.HTTPCookie{Name: entry.Name, Value: entry.Value, Domain: entry.Domain, Path: entry.Path, Secure: entry.Secure, HTTPOnly: entry.HttpOnly, Expires: entry.Expires})
	}
	previous.CookieJar = full
	if len(values) > 0 {
		previous.Cookies = values
	}
	previous.RefreshToken = client.GetRefreshToken()
	if len(previous.DeviceProfile) == 0 {
		previous.DeviceProfile, _ = json.Marshal(client.ExportDeviceProfile())
	}
	return previous
}
