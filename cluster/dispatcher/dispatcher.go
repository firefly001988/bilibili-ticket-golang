package dispatcher

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"bilibili-ticket-golang/cluster/domain"
)

type WorkerStatus struct {
	State  domain.AttemptState
	Result domain.ExecutionResult
}

type WorkerClient interface {
	Submit(context.Context, domain.WorkerNode, domain.ExecutionSpec) error
	Status(context.Context, domain.WorkerNode, string) (WorkerStatus, error)
	Stop(context.Context, domain.WorkerNode, string) error
}

type Repository interface {
	PutAttempt(context.Context, domain.ExecutionAttempt) error
	PutIntent(context.Context, domain.LogicalOrderIntent) error
	PutAccount(context.Context, domain.Account, *int64) error
	MarkIntentSucceeded(context.Context, domain.LogicalOrderIntent, domain.ExecutionResult) error
}

type MappingResolver interface {
	Resolve(context.Context, string, []domain.Buyer) ([]domain.Buyer, error)
}

var ErrBuyerUnavailable = errors.New("buyer is unavailable on account")

type IntentPlan struct {
	Macro  domain.MacroTask
	Intent domain.LogicalOrderIntent
}

type attempt struct {
	value         domain.ExecutionAttempt
	planID        string
	isolatedUntil time.Time
}

type Dispatcher struct {
	mu                  sync.Mutex
	client              WorkerClient
	repository          Repository
	resolver            MappingResolver
	plans               map[string]*IntentPlan
	attempts            map[string]*attempt
	accounts            map[string]domain.Account
	workers             map[string]domain.WorkerNode
	accountBusy         map[string]string
	workerBusy          map[string]string
	failedWorkers       map[string]time.Time
	quarantinedAccounts map[string]time.Time
	degraded            bool
	now                 func() time.Time
	next                uint64
	onSuccess           func(domain.LogicalOrderIntent, domain.ExecutionResult)
	stoppedPhases       map[domain.Phase]bool
}

func (d *Dispatcher) SetSuccessHandler(handler func(domain.LogicalOrderIntent, domain.ExecutionResult)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.onSuccess = handler
}

func New(client WorkerClient, repository Repository, resolver MappingResolver) *Dispatcher {
	return &Dispatcher{client: client, repository: repository, resolver: resolver, plans: make(map[string]*IntentPlan), attempts: make(map[string]*attempt), accounts: make(map[string]domain.Account), workers: make(map[string]domain.WorkerNode), accountBusy: make(map[string]string), workerBusy: make(map[string]string), failedWorkers: make(map[string]time.Time), quarantinedAccounts: make(map[string]time.Time), stoppedPhases: make(map[domain.Phase]bool), now: time.Now}
}

func (d *Dispatcher) SetResources(accounts []domain.Account, workers []domain.WorkerNode) {
	d.mu.Lock()
	defer d.mu.Unlock()
	nextAccounts := make(map[string]domain.Account, len(accounts))
	for _, account := range accounts {
		nextAccounts[account.ID] = account
	}
	nextWorkers := make(map[string]domain.WorkerNode, len(workers))
	for _, worker := range workers {
		nextWorkers[worker.ID] = worker
	}
	d.accounts, d.workers = nextAccounts, nextWorkers
}

func (d *Dispatcher) Add(plan IntentPlan) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.plans[plan.Intent.ID] = &plan
}

func (d *Dispatcher) RestoreAttempt(value domain.ExecutionAttempt) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.plans[value.IntentID]; !ok {
		return fmt.Errorf("cannot restore attempt %s without intent %s", value.ID, value.IntentID)
	}
	d.attempts[value.ID] = &attempt{value: value, planID: value.IntentID}
	if !value.State.Terminal() {
		d.accountBusy[value.AccountID] = value.ID
		d.workerBusy[value.WorkerID] = value.ID
	}
	return nil
}

func (d *Dispatcher) Attempts() []domain.ExecutionAttempt {
	d.mu.Lock()
	defer d.mu.Unlock()
	result := make([]domain.ExecutionAttempt, 0, len(d.attempts))
	for _, current := range d.attempts {
		result = append(result, current.value)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.Before(result[j].CreatedAt) })
	return result
}

func (d *Dispatcher) PunctualStopped() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, current := range d.attempts {
		if current.isolatedUntil.After(d.now()) && d.plans[current.planID].Intent.Phase == domain.PhasePunctual {
			return false
		}
		if d.plans[current.planID].Intent.Phase == domain.PhasePunctual && !current.value.State.Terminal() {
			return false
		}
	}
	return true
}

// Reconcile first observes every active attempt, then fills replica deficits.
func (d *Dispatcher) Reconcile(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.poll(ctx); err != nil {
		return err
	}
	ordered := make([]*IntentPlan, 0, len(d.plans))
	for _, plan := range d.plans {
		if !plan.Intent.Succeeded && !plan.Intent.Terminal {
			ordered = append(ordered, plan)
		}
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Macro.Priority == ordered[j].Macro.Priority {
			return ordered[i].Intent.CreatedAt.Before(ordered[j].Intent.CreatedAt)
		}
		return ordered[i].Macro.Priority > ordered[j].Macro.Priority
	})
	for _, plan := range ordered {
		if d.stoppedPhases[plan.Intent.Phase] {
			continue
		}
		if d.now().After(plan.Macro.Deadline) {
			plan.Intent.Terminal, plan.Intent.FailureReason = true, domain.FailureDeadline
			if d.repository != nil {
				_ = d.repository.PutIntent(ctx, plan.Intent)
			}
			continue
		}
		if d.conflicted(plan) {
			continue
		}
		desired := plan.Macro.DesiredReplicas
		if desired <= 0 {
			desired = 1
		}
		hard := plan.Macro.HardConcurrency
		if hard <= 0 {
			hard = desired
		}
		if desired > hard {
			desired = hard
		}
		for d.activeCount(plan.Intent.ID) < desired {
			account, worker, ok, err := d.pickResources(ctx, plan)
			if err != nil {
				return err
			}
			if !ok {
				break
			}
			if err := d.dispatch(ctx, plan, account, worker); err != nil {
				return err
			}
		}
	}
	return nil
}

func (d *Dispatcher) poll(ctx context.Context) error {
	for _, current := range d.attempts {
		if current.value.State.Terminal() {
			continue
		}
		worker := d.workers[current.value.WorkerID]
		status, err := d.client.Status(ctx, worker, current.value.ID)
		if err != nil {
			now := d.now()
			d.failedWorkers[worker.ID] = now
			d.quarantinedAccounts[current.value.AccountID] = now.Add(195 * time.Second)
			current.isolatedUntil = now.Add(195 * time.Second)
			d.degraded = true
			current.value.State, current.value.UpdatedAt = domain.AttemptFailed, now
			delete(d.accountBusy, current.value.AccountID)
			delete(d.workerBusy, current.value.WorkerID)
			if d.repository != nil {
				_ = d.repository.PutAttempt(ctx, current.value)
			}
			continue
		}
		current.value.State, current.value.UpdatedAt = status.State, d.now()
		current.value.Result = status.Result
		current.value.Result.Credentials = domain.Credentials{}
		if d.repository != nil {
			_ = d.repository.PutAttempt(ctx, current.value)
		}
		if !status.State.Terminal() {
			continue
		}
		delete(d.accountBusy, current.value.AccountID)
		delete(d.workerBusy, current.value.WorkerID)
		if status.Result.Credentials.Version > 0 {
			account := d.accounts[current.value.AccountID]
			oldVersion := account.Credentials.Version
			if status.Result.Credentials.Version > oldVersion {
				account.Credentials = status.Result.Credentials
				d.accounts[account.ID] = account
				if d.repository != nil {
					_ = d.repository.PutAccount(ctx, account, &oldVersion)
				}
			}
		}
		if status.Result.Success {
			if err := d.win(ctx, current, status.Result); err != nil {
				return err
			}
			continue
		}
		d.applyFailure(ctx, current, status.Result)
		if d.repository != nil {
			_ = d.repository.PutAttempt(ctx, current.value)
		}
	}
	return nil
}

func (d *Dispatcher) win(ctx context.Context, winner *attempt, result domain.ExecutionResult) error {
	plan := d.plans[winner.planID]
	if plan.Intent.Succeeded {
		return nil
	}
	plan.Intent.Succeeded, plan.Intent.Terminal = true, true
	if d.repository != nil {
		if err := d.repository.MarkIntentSucceeded(ctx, plan.Intent, result); err != nil {
			return err
		}
	}
	if d.onSuccess != nil {
		go d.onSuccess(plan.Intent, result)
	}
	for _, sibling := range d.attempts {
		if sibling.value.ID == winner.value.ID || sibling.value.State.Terminal() {
			continue
		}
		other := d.plans[sibling.planID]
		if sibling.planID == winner.planID || domain.Conflicts(plan.Intent, other.Intent) {
			_ = d.client.Stop(ctx, d.workers[sibling.value.WorkerID], sibling.value.ID)
			sibling.value.State = domain.AttemptStopping
		}
	}
	return nil
}

func (d *Dispatcher) applyFailure(ctx context.Context, current *attempt, result domain.ExecutionResult) {
	if result.Reason == domain.FailureUnrecoverable {
		plan := d.plans[current.planID]
		plan.Intent.Terminal, plan.Intent.FailureReason = true, result.Reason
		if d.repository != nil {
			_ = d.repository.PutIntent(ctx, plan.Intent)
		}
	}
	switch result.Reason {
	case domain.FailureCookieInvalid:
		account := d.accounts[current.value.AccountID]
		old := account.Credentials.Version
		account.Enabled = false
		d.accounts[account.ID] = account
		if d.repository != nil {
			_ = d.repository.PutAccount(ctx, account, &old)
		}
	case domain.FailureAccountRisk:
		account := d.accounts[current.value.AccountID]
		old := account.Credentials.Version
		account.CooldownUntil = d.now().Add(5 * time.Minute)
		d.accounts[account.ID] = account
		if d.repository != nil {
			_ = d.repository.PutAccount(ctx, account, &old)
		}
	case domain.FailureHTTP412, domain.FailureCaptcha, domain.FailureWorkerLost:
		d.failedWorkers[current.value.WorkerID] = d.now()
		d.degraded = true
	}
}

func (d *Dispatcher) conflicted(candidate *IntentPlan) bool {
	for _, current := range d.attempts {
		if current.value.State.Terminal() || current.planID == candidate.Intent.ID {
			continue
		}
		if domain.Conflicts(candidate.Intent, d.plans[current.planID].Intent) {
			return true
		}
	}
	return false
}

func (d *Dispatcher) activeCount(intentID string) int {
	count := 0
	for _, current := range d.attempts {
		if current.planID == intentID && !current.value.State.Terminal() {
			count++
		}
	}
	return count
}

func (d *Dispatcher) pickResources(ctx context.Context, plan *IntentPlan) (domain.Account, domain.WorkerNode, bool, error) {
	accounts := make([]domain.Account, 0, len(d.accounts))
	recoveredAccounts := make([]domain.Account, 0)
	workers := make([]domain.WorkerNode, 0, len(d.workers))
	for _, value := range d.accounts {
		if until := d.quarantinedAccounts[value.ID]; until.After(d.now()) {
			continue
		} else if !until.IsZero() {
			delete(d.quarantinedAccounts, value.ID)
		}
		if !value.Enabled || d.accountBusy[value.ID] != "" || value.CooldownUntil.After(d.now()) {
			continue
		}
		if !value.CooldownUntil.IsZero() {
			recoveredAccounts = append(recoveredAccounts, value)
			continue
		}
		if !d.degraded && value.Role == domain.RoleStandby {
			continue
		}
		accounts = append(accounts, value)
	}
	for _, value := range d.workers {
		if !value.Enabled || d.workerBusy[value.ID] != "" {
			continue
		}
		if _, failed := d.failedWorkers[value.ID]; failed {
			continue
		}
		if !d.degraded && value.Role == domain.RoleStandby {
			continue
		}
		workers = append(workers, value)
	}
	sort.Slice(workers, func(i, j int) bool { return workers[i].ID < workers[j].ID })
	if len(accounts) == 0 && d.degraded {
		for _, value := range d.accounts {
			if value.Enabled && d.accountBusy[value.ID] == "" && !d.quarantinedAccounts[value.ID].After(d.now()) && value.Role == domain.RoleStandby {
				accounts = append(accounts, value)
			}
		}
	}
	if len(accounts) == 0 {
		accounts = append(accounts, recoveredAccounts...)
	}
	sort.Slice(accounts, func(i, j int) bool { return accounts[i].ID < accounts[j].ID })
	if len(accounts) == 0 || len(workers) == 0 {
		return domain.Account{}, domain.WorkerNode{}, false, nil
	}
	if d.resolver != nil {
		eligible := accounts[:0]
		for _, account := range accounts {
			if _, err := d.resolver.Resolve(ctx, account.ID, plan.Intent.Buyers); errors.Is(err, ErrBuyerUnavailable) {
				continue
			} else if err != nil {
				return domain.Account{}, domain.WorkerNode{}, false, err
			}
			eligible = append(eligible, account)
		}
		accounts = eligible
	}
	if len(accounts) == 0 {
		return domain.Account{}, domain.WorkerNode{}, false, nil
	}
	return accounts[0], workers[0], true, nil
}

func (d *Dispatcher) dispatch(ctx context.Context, plan *IntentPlan, account domain.Account, worker domain.WorkerNode) error {
	buyers := append([]domain.Buyer(nil), plan.Intent.Buyers...)
	var err error
	if d.resolver != nil {
		buyers, err = d.resolver.Resolve(ctx, account.ID, buyers)
		if err != nil {
			return err
		}
	}
	d.next++
	id := fmt.Sprintf("attempt-%s-%d-%d", plan.Intent.ID, d.now().UnixNano(), d.next)
	mode := domain.StartImmediate
	if plan.Macro.StartAt.After(d.now()) {
		mode = domain.StartScheduled
	}
	spec := domain.ExecutionSpec{AttemptID: id, IntentID: plan.Intent.ID, ProjectID: plan.Macro.ProjectID, ScreenID: plan.Macro.ScreenID, SKUID: plan.Macro.SKUID, Buyers: buyers, StartMode: mode, StartAt: plan.Macro.StartAt, Deadline: plan.Macro.Deadline, IntervalMS: 500, Credentials: account.Credentials}
	now := d.now()
	value := domain.ExecutionAttempt{ID: id, IntentID: plan.Intent.ID, SpecHash: spec.Hash(), AccountID: account.ID, WorkerID: worker.ID, State: domain.AttemptWaiting, CreatedAt: now, UpdatedAt: now}
	if err := d.client.Submit(ctx, worker, spec); err != nil {
		// A transport error does not prove that the worker rejected the task.
		// Retain the attempt for audit and isolate its account until the old
		// worker lease plus safety margin has expired.
		value.State = domain.AttemptFailed
		value.Result = domain.ExecutionResult{AttemptID: id, IntentID: plan.Intent.ID, SpecHash: spec.Hash(), State: domain.AttemptFailed, Reason: domain.FailureWorkerLost, Message: err.Error(), FinishedAt: now}
		d.attempts[id] = &attempt{value: value, planID: plan.Intent.ID, isolatedUntil: now.Add(195 * time.Second)}
		d.failedWorkers[worker.ID] = now
		d.quarantinedAccounts[account.ID] = now.Add(195 * time.Second)
		d.degraded = true
		if d.repository != nil {
			return d.repository.PutAttempt(ctx, value)
		}
		return nil
	}
	d.attempts[id] = &attempt{value: value, planID: plan.Intent.ID}
	d.accountBusy[account.ID], d.workerBusy[worker.ID] = id, id
	if d.repository != nil {
		return d.repository.PutAttempt(ctx, value)
	}
	return nil
}

func (d *Dispatcher) SwitchToReflow(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stoppedPhases[domain.PhasePunctual] = true
	for _, plan := range d.plans {
		if plan.Intent.Phase != domain.PhasePunctual || plan.Intent.Succeeded || plan.Intent.Terminal {
			continue
		}
		plan.Intent.Terminal, plan.Intent.FailureReason = true, domain.FailureStopped
		if d.repository != nil {
			if err := d.repository.PutIntent(ctx, plan.Intent); err != nil {
				return err
			}
		}
	}
	for _, current := range d.attempts {
		plan := d.plans[current.planID]
		if plan.Intent.Phase != domain.PhasePunctual || current.value.State.Terminal() {
			continue
		}
		if err := d.client.Stop(ctx, d.workers[current.value.WorkerID], current.value.ID); err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
		current.value.State = domain.AttemptStopping
	}
	return nil
}

// ResumePhase enables explicit planning of a new run. Intents from an earlier
// stopped phase remain terminal and therefore cannot be restarted.
func (d *Dispatcher) ResumePhase(phase domain.Phase) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.stoppedPhases, phase)
}
