package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"bilibili-ticket-golang/biliutils"
	"bilibili-ticket-golang/cluster/accounts"
	"bilibili-ticket-golang/cluster/dispatcher"
	"bilibili-ticket-golang/cluster/domain"
	"bilibili-ticket-golang/cluster/employer"
	"bilibili-ticket-golang/cluster/planner"
	clusterstorage "bilibili-ticket-golang/cluster/storage"
	"bilibili-ticket-golang/store/cookiejar"
)

type ClusterSnapshot struct {
	Accounts []AccountSummary `json:"accounts"`
	Workers  []WorkerSummary  `json:"workers"`
	Macros   []MacroSummary   `json:"macros"`
	Attempts []AttemptSummary `json:"attempts"`
}
type AccountSummary struct {
	ID                string              `json:"id"`
	Name              string              `json:"name"`
	Role              domain.ResourceRole `json:"role"`
	Enabled           bool                `json:"enabled"`
	CooldownUntil     time.Time           `json:"cooldownUntil,omitempty"`
	CredentialVersion int64               `json:"credentialVersion"`
}
type WorkerSummary struct {
	ID              string              `json:"id"`
	Name            string              `json:"name"`
	BaseURL         string              `json:"baseUrl"`
	Role            domain.ResourceRole `json:"role"`
	Enabled         bool                `json:"enabled"`
	Healthy         bool                `json:"healthy"`
	ActiveAttemptID string              `json:"activeAttemptId,omitempty"`
	Version         string              `json:"version,omitempty"`
}
type MacroSummary struct {
	domain.MacroTask
	Phase domain.Phase `json:"phase"`
}
type AttemptSummary struct {
	ID        string               `json:"id"`
	IntentID  string               `json:"intentId"`
	AccountID string               `json:"accountId"`
	WorkerID  string               `json:"workerId"`
	State     domain.AttemptState  `json:"state"`
	OrderID   string               `json:"orderId,omitempty"`
	Reason    domain.FailureReason `json:"reason,omitempty"`
}

type ClusterService struct {
	repository *clusterstorage.Repository
	client     *employer.WorkerClient
	dispatcher *dispatcher.Dispatcher
	accounts   *accounts.Manager
	local      employer.LocalWorkerManager
	mu         sync.RWMutex
	phases     map[string]domain.Phase
	cancel     context.CancelFunc
}

func NewClusterService(repository *clusterstorage.Repository) *ClusterService {
	client := employer.NewWorkerClient()
	service := &ClusterService{repository: repository, client: client, phases: make(map[string]domain.Phase)}
	service.accounts = accounts.NewManager(repository, biliProvisioner{})
	service.dispatcher = dispatcher.New(client, repository, buyerResolver{repository: repository})
	return service
}

func (s *ClusterService) Start(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	s.cancel = cancel
	accountsList, _ := s.repository.ListAccounts(ctx)
	workers, _ := s.repository.ListWorkers(ctx)
	for _, node := range workers {
		if key, err := s.repository.WorkerKey(ctx, node.ID); err == nil {
			s.client.SetKey(node.ID, key)
		}
	}
	s.dispatcher.SetResources(accountsList, workers)
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
	go func() {
		node, err := s.local.Start(ctx, s.client, employer.LocalWorkerOptions{DataDir: "data/local-worker"})
		if err == nil {
			_ = s.repository.PutWorker(ctx, node)
			_ = s.refreshResources(ctx)
		}
	}()
}

func (s *ClusterService) Close() {
	if s.cancel != nil {
		s.cancel()
	}
	_ = s.local.Stop()
	_ = s.repository.Close()
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
	s.dispatcher.SetResources(accountList, workers)
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
	result := ClusterSnapshot{}
	for _, account := range accountList {
		result.Accounts = append(result.Accounts, AccountSummary{ID: account.ID, Name: account.Name, Role: account.Role, Enabled: account.Enabled, CooldownUntil: account.CooldownUntil, CredentialVersion: account.Credentials.Version})
	}
	for _, node := range workerList {
		summary := WorkerSummary{ID: node.ID, Name: node.Name, BaseURL: node.BaseURL, Role: node.Role, Enabled: node.Enabled}
		healthCtx, cancel := context.WithTimeout(ctx, 800*time.Millisecond)
		health, healthErr := s.client.Health(healthCtx, node)
		cancel()
		if healthErr == nil {
			summary.Healthy = true
			summary.ActiveAttemptID, _ = health["activeAttemptId"].(string)
			summary.Version, _ = health["version"].(string)
		}
		result.Workers = append(result.Workers, summary)
	}
	s.mu.RLock()
	for _, macro := range macros {
		phase := s.phases[macro.ID]
		if phase == "" {
			phase = domain.PhasePunctual
		}
		result.Macros = append(result.Macros, MacroSummary{MacroTask: macro, Phase: phase})
	}
	s.mu.RUnlock()
	for _, value := range s.dispatcher.Attempts() {
		result.Attempts = append(result.Attempts, AttemptSummary{ID: value.ID, IntentID: value.IntentID, AccountID: value.AccountID, WorkerID: value.WorkerID, State: value.State})
	}
	return result, nil
}

func (s *ClusterService) ImportAccount(document string) error {
	_, err := s.accounts.Import(context.Background(), []byte(document))
	if err == nil {
		err = s.refreshResources(context.Background())
	}
	return err
}

func (s *ClusterService) AddWorker(document string) error {
	var input struct {
		ID      string              `json:"id"`
		Name    string              `json:"name"`
		BaseURL string              `json:"baseUrl"`
		Key     string              `json:"key"`
		Role    domain.ResourceRole `json:"role"`
	}
	if err := json.Unmarshal([]byte(document), &input); err != nil {
		return err
	}
	if input.ID == "" || input.BaseURL == "" || input.Key == "" {
		return fmt.Errorf("id, baseUrl and key are required")
	}
	if input.Role == "" {
		input.Role = domain.RolePrimary
	}
	node := domain.WorkerNode{ID: input.ID, Name: input.Name, BaseURL: input.BaseURL, Role: input.Role, Enabled: true}
	ctx := context.Background()
	if err := s.repository.PutWorker(ctx, node); err != nil {
		return err
	}
	if err := s.repository.PutWorkerKey(ctx, node.ID, input.Key); err != nil {
		return err
	}
	s.client.SetKey(node.ID, input.Key)
	return s.refreshResources(ctx)
}

func (s *ClusterService) SaveMacro(document string) error {
	var value domain.MacroTask
	if err := json.Unmarshal([]byte(document), &value); err != nil {
		return err
	}
	if value.ID == "" || value.TaskGroupID == "" {
		return fmt.Errorf("id and taskGroupId are required")
	}
	return s.repository.PutMacroTask(context.Background(), value)
}
func (s *ClusterService) SavePurchaseGroup(document string) error {
	var value domain.PurchaseGroup
	if err := json.Unmarshal([]byte(document), &value); err != nil {
		return err
	}
	if value.ID == "" || value.MacroTaskID == "" {
		return fmt.Errorf("id and macroTaskId are required")
	}
	if value.CreatedAt.IsZero() {
		value.CreatedAt = time.Now()
	}
	return s.repository.PutPurchaseGroup(context.Background(), value)
}

func (s *ClusterService) StartMacro(macroID string) error {
	return s.planAndStart(context.Background(), macroID, domain.PhasePunctual)
}

func (s *ClusterService) planAndStart(ctx context.Context, macroID string, phase domain.Phase) error {
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
	intents, err := planner.Plan(*selected, groups, phase, time.Now())
	if err != nil {
		return err
	}
	for _, intent := range intents {
		if err := s.repository.PutIntent(ctx, intent); err != nil {
			return err
		}
		s.dispatcher.Add(dispatcher.IntentPlan{Macro: *selected, Intent: intent})
	}
	s.mu.Lock()
	s.phases[macroID] = phase
	s.mu.Unlock()
	return s.dispatcher.Reconcile(ctx)
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
		if err := s.planAndStart(ctx, macro.ID, domain.PhaseReflow); err != nil {
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

type buyerResolver struct{ repository *clusterstorage.Repository }

func (r buyerResolver) Resolve(ctx context.Context, accountID string, buyers []domain.Buyer) ([]domain.Buyer, error) {
	result := append([]domain.Buyer(nil), buyers...)
	for i := range result {
		mapping, err := r.repository.BuyerMapping(ctx, accountID, result[i].LogicalID)
		if err != nil {
			return nil, fmt.Errorf("buyer %s is not provisioned on account %s: %w", result[i].LogicalID, accountID, err)
		}
		result[i].BuyerID = mapping.BuyerID
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
		result[i] = domain.Buyer{BuyerID: value.Id, Name: value.Name, Tel: value.Tel, IDCard: value.IdCard, Type: value.IdType}
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
	cookies := make([]*http.Cookie, 0, len(account.Credentials.Cookies))
	for name, value := range account.Credentials.Cookies {
		cookies = append(cookies, &http.Cookie{Name: name, Value: value, Path: "/"})
	}
	for _, raw := range []string{"https://www.bilibili.com/", "https://show.bilibili.com/", "https://passport.bilibili.com/"} {
		u, _ := url.Parse(raw)
		jar.SetCookies(u, cookies)
	}
	client, err := biliutils.NewBiliClientWithCookiejar(jar)
	if err != nil {
		return nil, nil, err
	}
	client.SetRefreshToken(account.Credentials.RefreshToken)
	return client, jar, nil
}
func credentialsFrom(client *biliutils.BiliClient, jar *cookiejar.Jar, previous domain.Credentials) domain.Credentials {
	values := make(map[string]string)
	for _, entry := range jar.AllPersistentEntries() {
		values[entry.Name] = entry.Value
	}
	if len(values) > 0 {
		previous.Cookies = values
	}
	previous.RefreshToken = client.GetRefreshToken()
	return previous
}
