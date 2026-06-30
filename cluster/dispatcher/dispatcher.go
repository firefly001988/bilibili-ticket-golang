package dispatcher

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	"bilibili-ticket-golang/cluster/domain"
)

const rpcTimeout = 5 * time.Second

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
	accountReservations map[string]string // accountID → taskGroupID
	failedWorkers       map[string]time.Time
	quarantinedAccounts map[string]time.Time
	workerCooldown      map[string]time.Time // 412 → 5-min cooldown
	degraded            bool
	now                 func() time.Time
	next                uint64
	onSuccess           func(domain.LogicalOrderIntent, domain.ExecutionResult)
	stoppedPhases       map[domain.Phase]bool
	workerReservations  map[string]string // workerID → taskGroupID
	workerRoles         map[string]domain.ResourceRole
	activeTaskGroup     string // current active task group ID (only one at a time)
	retryIntervalMs     int64  // global retry interval (0 = use default 500ms)
	startDelayMs        int64  // global start delay (0 = no early start)
}

func (d *Dispatcher) SetSuccessHandler(handler func(domain.LogicalOrderIntent, domain.ExecutionResult)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.onSuccess = handler
}

func New(client WorkerClient, repository Repository, resolver MappingResolver) *Dispatcher {
	return &Dispatcher{client: client, repository: repository, resolver: resolver, plans: make(map[string]*IntentPlan), attempts: make(map[string]*attempt), accounts: make(map[string]domain.Account), workers: make(map[string]domain.WorkerNode), accountBusy: make(map[string]string), workerBusy: make(map[string]string), accountReservations: make(map[string]string), failedWorkers: make(map[string]time.Time), quarantinedAccounts: make(map[string]time.Time), workerCooldown: make(map[string]time.Time), stoppedPhases: make(map[domain.Phase]bool), workerReservations: make(map[string]string), workerRoles: make(map[string]domain.ResourceRole), now: time.Now}
}

// SetGlobalConfig updates the dispatcher's runtime configuration. Values
// of 0 mean "use the built-in default".
func (d *Dispatcher) SetGlobalConfig(retryIntervalMs, startDelayMs int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.retryIntervalMs = retryIntervalMs
	d.startDelayMs = startDelayMs
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

// ReserveWorkers locks a set of workers for a task group.  Only the active
// task group's workers may be picked during Reconcile.  Passing an empty
// set releases all reservations.
func (d *Dispatcher) ReserveWorkers(taskGroupID string, workerIDs []string) {
	d.ReserveWorkerPools(taskGroupID, workerIDs, nil)
}

// ReserveAccounts locks a set of accounts for a task group. Only the active
// task group's accounts may be picked during Reconcile.
func (d *Dispatcher) ReserveAccounts(taskGroupID string, accountIDs []string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.accountReservations = make(map[string]string, len(accountIDs))
	for _, id := range accountIDs {
		if id == "" {
			continue
		}
		d.accountReservations[id] = taskGroupID
	}
	d.activeTaskGroup = taskGroupID
}

// ReserveWorkerPools locks primary and standby worker pools for a task group.
// Standby workers are reserved by the task group immediately, but are only
// selected to replace failed primary workers.  If no primary pool is configured,
// standby workers act as the only available pool.
func (d *Dispatcher) ReserveWorkerPools(taskGroupID string, primaryWorkerIDs, standbyWorkerIDs []string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.workerReservations = make(map[string]string, len(primaryWorkerIDs)+len(standbyWorkerIDs))
	d.workerRoles = make(map[string]domain.ResourceRole, len(primaryWorkerIDs)+len(standbyWorkerIDs))
	for _, id := range primaryWorkerIDs {
		d.workerReservations[id] = taskGroupID
		d.workerRoles[id] = domain.RolePrimary
	}
	for _, id := range standbyWorkerIDs {
		if _, exists := d.workerReservations[id]; exists {
			continue
		}
		d.workerReservations[id] = taskGroupID
		d.workerRoles[id] = domain.RoleStandby
	}
	d.activeTaskGroup = taskGroupID
}

// ReleaseWorkers clears the worker reservations.
func (d *Dispatcher) ReleaseWorkers() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.accountReservations = make(map[string]string)
	d.workerReservations = make(map[string]string)
	d.workerRoles = make(map[string]domain.ResourceRole)
	d.activeTaskGroup = ""
}

// ActiveTaskGroup returns the currently reserved task group ID (empty if none).
func (d *Dispatcher) ActiveTaskGroup() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.activeTaskGroup
}

func (d *Dispatcher) MarkWorkerHealthy(workerID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.failedWorkers, workerID)
	delete(d.workerCooldown, workerID)
	if len(d.failedWorkers) == 0 {
		d.degraded = false
	}
}

// WorkerCooldownInfo describes why and until when a worker is cooled down.
type WorkerCooldownInfo struct {
	CooledDown      bool
	CooldownEnd     time.Time
	StartedAt       time.Time
	Reason          string
	TotalDurationMs int64 // total duration of the cooldown in milliseconds
}

// WorkerCooldown returns cooldown info for a worker. When cooled down,
// Reason describes why and CooldownEnd is when the cooldown expires.
func (d *Dispatcher) WorkerCooldown(workerID string) WorkerCooldownInfo {
	d.mu.Lock()
	defer d.mu.Unlock()
	if endAt, ok := d.workerCooldown[workerID]; ok && endAt.After(d.now()) {
		startedAt := d.failedWorkers[workerID]
		reason := "412 限流"
		return WorkerCooldownInfo{
			CooledDown:      true,
			CooldownEnd:     endAt,
			StartedAt:       startedAt,
			Reason:          reason,
			TotalDurationMs: endAt.Sub(startedAt).Milliseconds(),
		}
	}
	return WorkerCooldownInfo{}
}

// HasCooldownWorkersWithDeficit reports whether there is at least one
// worker under 412 cooldown AND at least one armed intent has a
// deficit (needs more attempts).  The caller can use this to shorten the
// Reconcile interval from 15s to 5s for faster worker switching.
func (d *Dispatcher) HasCooldownWorkersWithDeficit() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.workerCooldown) == 0 {
		return false
	}
	// Check if any armed intent has a deficit
	for _, plan := range d.plans {
		if !plan.Intent.Armed || plan.Intent.Succeeded || plan.Intent.Terminal {
			continue
		}
		if d.stoppedPhases[plan.Intent.Phase] {
			continue
		}
		if d.activeCount(plan.Intent.ID) < effectiveWeight(plan.Intent) {
			return true
		}
	}
	return false
}

func (d *Dispatcher) ActiveAttemptsFor(intentIDs map[string]struct{}) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	count := 0
	for _, current := range d.attempts {
		if _, ok := intentIDs[current.value.IntentID]; ok && !current.value.State.Terminal() {
			count++
		}
	}
	return count
}

func (d *Dispatcher) MacroActive(macroID string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, current := range d.attempts {
		plan := d.plans[current.planID]
		if plan != nil && plan.Macro.ID == macroID && !current.value.State.Terminal() {
			return true
		}
	}
	return false
}

// MacroAttempts returns all attempts (terminal and active) belonging to a macro.
func (d *Dispatcher) MacroAttempts(macroID string) []domain.ExecutionAttempt {
	d.mu.Lock()
	defer d.mu.Unlock()
	var result []domain.ExecutionAttempt
	for _, current := range d.attempts {
		plan := d.plans[current.planID]
		if plan != nil && plan.Macro.ID == macroID {
			result = append(result, current.value)
		}
	}
	return result
}

// DisarmMacro marks all intents of a macro as stopped, clears their attempts,
// and releases busy accounts/workers. Call this after sending Stop to workers.
func (d *Dispatcher) DisarmMacro(macroID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for intentID, plan := range d.plans {
		if plan.Macro.ID != macroID {
			continue
		}
		if !plan.Intent.Succeeded {
			plan.Intent.Armed = false
			plan.Intent.Terminal = true
			plan.Intent.FailureReason = domain.FailureStopped
			if d.repository != nil {
				_ = d.repository.PutIntent(context.Background(), plan.Intent)
			}
		}
		// Clear attempts for this intent
		for attemptID, current := range d.attempts {
			if current.planID == intentID {
				if !current.value.State.Terminal() {
					current.value.State = domain.AttemptStopped
					current.value.Result = domain.ExecutionResult{
						AttemptID: current.value.ID,
						IntentID:  current.value.IntentID,
						SpecHash:  current.value.SpecHash,
						State:     domain.AttemptStopped,
						Reason:    domain.FailureStopped,
						Message:   "macro disarmed by employer",
					}
				}
				if d.repository != nil {
					_ = d.repository.PutAttempt(context.Background(), current.value)
				}
				delete(d.accountBusy, current.value.AccountID)
				delete(d.workerBusy, current.value.WorkerID)
				delete(d.attempts, attemptID)
			}
		}
		delete(d.plans, intentID)
	}
}

func (d *Dispatcher) RemoveMacro(macroID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, current := range d.attempts {
		plan := d.plans[current.planID]
		if plan != nil && plan.Macro.ID == macroID && !current.value.State.Terminal() {
			return fmt.Errorf("macro is used by active attempt %s", current.value.ID)
		}
	}
	for intentID, plan := range d.plans {
		if plan.Macro.ID != macroID {
			continue
		}
		delete(d.plans, intentID)
		for attemptID, current := range d.attempts {
			if current.planID == intentID {
				delete(d.attempts, attemptID)
			}
		}
	}
	return nil
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

// RemoveTerminalAttempts removes terminal attempts from the dispatcher's
// in-memory history. Non-terminal attempts are intentionally kept so the UI
// cannot hide work that is still reserving resources or can still receive a
// worker callback.
func (d *Dispatcher) RemoveTerminalAttempts(ids []string) []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(ids) == 0 {
		return nil
	}
	removed := make([]string, 0, len(ids))
	for _, id := range ids {
		current, ok := d.attempts[id]
		if !ok || !current.value.State.Terminal() {
			continue
		}
		delete(d.attempts, id)
		delete(d.accountBusy, current.value.AccountID)
		delete(d.workerBusy, current.value.WorkerID)
		removed = append(removed, id)
	}
	return removed
}

// Plans returns all currently armed IntentPlans, including their macro
// metadata so callers can map intents to macros for UI display.
func (d *Dispatcher) Plans() []IntentPlan {
	d.mu.Lock()
	defer d.mu.Unlock()
	result := make([]IntentPlan, 0, len(d.plans))
	for _, plan := range d.plans {
		result = append(result, *plan)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Intent.CreatedAt.Before(result[j].Intent.CreatedAt) })
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

// Reconcile first observes every active attempt, then fills each intent up to
// its current task-group-wide proportional allocation target.
func (d *Dispatcher) Reconcile(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.poll(ctx); err != nil {
		return err
	}
	for {
		ordered := d.dispatchablePlans(ctx)
		targets := d.allocationTargets(ordered, d.totalSlotCount(ordered))
		dispatched := false
		sort.SliceStable(ordered, func(i, j int) bool {
			di := targets[ordered[i].Intent.ID] - d.activeCount(ordered[i].Intent.ID)
			dj := targets[ordered[j].Intent.ID] - d.activeCount(ordered[j].Intent.ID)
			if di == dj {
				return planOrderLess(ordered[i], ordered[j])
			}
			return di > dj
		})
		for _, plan := range ordered {
			if targets[plan.Intent.ID] <= d.activeCount(plan.Intent.ID) {
				continue
			}
			account, worker, ok, err := d.pickResources(ctx, plan)
			if err != nil {
				return err
			}
			if !ok {
				continue
			}
			if err := d.dispatch(ctx, plan, account, worker); err != nil {
				return err
			}
			dispatched = true
			break // re-sort before next dispatch
		}
		if !dispatched {
			break
		}
	}
	return nil
}

// AllocationTargets returns the current proportional active-attempt target for
// each armed intent. It is intentionally best-effort for UI display; Reconcile
// recomputes targets again before dispatching.
func (d *Dispatcher) AllocationTargets() map[string]int {
	d.mu.Lock()
	defer d.mu.Unlock()
	plans := d.dispatchablePlans(context.Background())
	return d.allocationTargets(plans, d.totalSlotCount(plans))
}

func (d *Dispatcher) dispatchablePlans(ctx context.Context) []*IntentPlan {
	ordered := make([]*IntentPlan, 0, len(d.plans))
	for _, plan := range d.plans {
		if !plan.Intent.Armed || plan.Intent.Succeeded || plan.Intent.Terminal {
			continue
		}
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
		ordered = append(ordered, plan)
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		return planOrderLess(ordered[i], ordered[j])
	})
	return ordered
}

func (d *Dispatcher) totalSlotCount(plans []*IntentPlan) int {
	if len(plans) == 0 {
		return 0
	}
	active := 0
	planIDs := make(map[string]struct{}, len(plans))
	for _, plan := range plans {
		planIDs[plan.Intent.ID] = struct{}{}
	}
	for _, current := range d.attempts {
		if _, ok := planIDs[current.planID]; ok && !current.value.State.Terminal() {
			active++
		}
	}
	idleAccounts := len(d.availableAccounts())
	idleWorkers := d.availableWorkerCount(plans)
	if idleAccounts < idleWorkers {
		return active + idleAccounts
	}
	return active + idleWorkers
}

func (d *Dispatcher) allocationTargets(plans []*IntentPlan, totalSlots int) map[string]int {
	targets := make(map[string]int, len(plans))
	if totalSlots <= 0 || len(plans) == 0 {
		return targets
	}
	totalWeight := 0
	for _, plan := range plans {
		totalWeight += effectiveWeight(plan.Intent)
	}
	if totalWeight <= 0 {
		return targets
	}
	type share struct {
		plan      *IntentPlan
		remainder int
	}
	shares := make([]share, 0, len(plans))
	assigned := 0
	for _, plan := range plans {
		weightedSlots := totalSlots * effectiveWeight(plan.Intent)
		base := weightedSlots / totalWeight
		targets[plan.Intent.ID] = base
		assigned += base
		shares = append(shares, share{plan: plan, remainder: weightedSlots % totalWeight})
	}
	sort.SliceStable(shares, func(i, j int) bool {
		if shares[i].remainder == shares[j].remainder {
			return planOrderLess(shares[i].plan, shares[j].plan)
		}
		return shares[i].remainder > shares[j].remainder
	})
	for remaining, index := totalSlots-assigned, 0; remaining > 0 && index < len(shares); remaining, index = remaining-1, index+1 {
		targets[shares[index].plan.Intent.ID]++
	}
	return targets
}

func (d *Dispatcher) poll(ctx context.Context) error {
	for _, current := range d.attempts {
		if current.value.State.Terminal() {
			continue
		}
		worker := d.workers[current.value.WorkerID]
		rpcCtx, cancel := context.WithTimeout(ctx, rpcTimeout)
		status, err := d.client.Status(rpcCtx, worker, current.value.ID)
		cancel()
		if err != nil {
			now := d.now()
			d.failedWorkers[worker.ID] = now
			d.quarantinedAccounts[current.value.AccountID] = now.Add(195 * time.Second)
			current.isolatedUntil = now.Add(195 * time.Second)
			d.degraded = true
			current.value.State, current.value.UpdatedAt = domain.AttemptFailed, now
			current.value.Result = domain.ExecutionResult{AttemptID: current.value.ID, IntentID: current.value.IntentID, SpecHash: current.value.SpecHash, State: domain.AttemptFailed, Reason: domain.FailureWorkerLost, Message: err.Error(), FinishedAt: now}
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
			log.Printf("[dispatcher] poll detected success: attempt=%s intent=%s orderID=%s paymentURL=%q",
				current.value.ID, current.value.IntentID, status.Result.OrderID, status.Result.PaymentURL)
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
		log.Printf("[dispatcher] win SKIP: intent %s already succeeded", plan.Intent.ID)
		return nil
	}
	log.Printf("[dispatcher] win: intent=%s orderID=%s paymentURL=%q onSuccess=%v",
		plan.Intent.ID, result.OrderID, result.PaymentURL, d.onSuccess != nil)
	plan.Intent.Succeeded, plan.Intent.Terminal = true, true
	// Call onSuccess BEFORE MarkIntentSucceeded so that the payment
	// window opens even if the database write fails.
	if d.onSuccess != nil {
		go d.onSuccess(plan.Intent, result)
	}
	if d.repository != nil {
		if err := d.repository.MarkIntentSucceeded(ctx, plan.Intent, result); err != nil {
			log.Printf("[dispatcher] win: MarkIntentSucceeded failed for intent=%s: %v", plan.Intent.ID, err)
			return err
		}
	}
	for _, other := range d.plans {
		if other.Intent.ID == plan.Intent.ID || other.Intent.Succeeded || other.Intent.Terminal {
			continue
		}
		if domain.Conflicts(plan.Intent, other.Intent) {
			other.Intent.Terminal = true
			other.Intent.FailureReason = domain.FailureUnrecoverable
			if d.repository != nil {
				if err := d.repository.PutIntent(ctx, other.Intent); err != nil {
					return err
				}
			}
		}
	}
	for _, sibling := range d.attempts {
		if sibling.value.ID == winner.value.ID || sibling.value.State.Terminal() {
			continue
		}
		other := d.plans[sibling.planID]
		if sibling.planID == winner.planID || domain.Conflicts(plan.Intent, other.Intent) {
			rpcCtx, cancel := context.WithTimeout(ctx, rpcTimeout)
			_ = d.client.Stop(rpcCtx, d.workers[sibling.value.WorkerID], sibling.value.ID)
			cancel()
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
	case domain.FailureHTTP412:
		// 412: quarantine the worker for 5 minutes so Reconcile
		// can try a different worker if available.  Unlike transport-level
		// failures this is a recoverable rate-limit; the worker is healthy.
		d.workerCooldown[current.value.WorkerID] = d.now().Add(5 * time.Minute)
		d.failedWorkers[current.value.WorkerID] = d.now()
		d.degraded = true
	case domain.FailureCaptcha:
		// Captcha failures are expected and recoverable; do NOT cool down the worker.
		// The worker remains immediately available for the next attempt.
	case domain.FailureWorkerLost:
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

func planOrderLess(a, b *IntentPlan) bool {
	if a.Intent.Priority == b.Intent.Priority {
		if a.Intent.CreatedAt.Equal(b.Intent.CreatedAt) {
			return a.Intent.ID < b.Intent.ID
		}
		return a.Intent.CreatedAt.Before(b.Intent.CreatedAt)
	}
	return a.Intent.Priority < b.Intent.Priority
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

// effectiveWeight returns the relative weight of an intent, used for
// proportional allocation via D'Hondt method.  Weight comes from the
// PurchaseGroup that generated the intent (default=1).
func effectiveWeight(intent domain.LogicalOrderIntent) int {
	if intent.Weight > 0 {
		return intent.Weight
	}
	return 1
}

// ProcessCompletedTask handles a task result pushed by a worker via the
// bidirectional heartbeat stream.  It updates the attempt, releases
// resources, and triggers reconciliation immediately instead of waiting
// for the periodic poll cycle.
func (d *Dispatcher) ProcessCompletedTask(workerID string, result domain.ExecutionResult) {
	d.mu.Lock()
	defer d.mu.Unlock()

	attempt, ok := d.attempts[result.AttemptID]
	if !ok || attempt.value.State.Terminal() {
		if !ok {
			log.Printf("[dispatcher] ProcessCompletedTask SKIP: attempt %s not found (worker=%s, resultState=%s, success=%v)",
				result.AttemptID, workerID, result.State, result.Success)
		}
		return
	}

	log.Printf("[dispatcher] ProcessCompletedTask: attempt=%s worker=%s state=%s success=%v orderID=%s paymentURL=%q",
		result.AttemptID, workerID, result.State, result.Success, result.OrderID, result.PaymentURL)

	now := d.now()
	attempt.value.State, attempt.value.UpdatedAt = result.State, now
	attempt.value.Result = result
	attempt.value.Result.Credentials = domain.Credentials{}

	// Persist the update.
	if d.repository != nil {
		_ = d.repository.PutAttempt(context.Background(), attempt.value)
	}

	if !result.State.Terminal() {
		return
	}

	delete(d.accountBusy, attempt.value.AccountID)
	delete(d.workerBusy, attempt.value.WorkerID)

	if result.Credentials.Version > 0 {
		account := d.accounts[attempt.value.AccountID]
		oldVersion := account.Credentials.Version
		if result.Credentials.Version > oldVersion {
			account.Credentials = result.Credentials
			d.accounts[account.ID] = account
			if d.repository != nil {
				_ = d.repository.PutAccount(context.Background(), account, &oldVersion)
			}
		}
	}

	if result.Success {
		_ = d.win(context.Background(), attempt, result)
	} else {
		d.applyFailure(context.Background(), attempt, result)
		if d.repository != nil {
			_ = d.repository.PutAttempt(context.Background(), attempt.value)
		}
	}

	// Free resources immediately — trigger reconciliation in a separate
	// goroutine to avoid deadlock (ProcessCompletedTask holds d.mu, and
	// Reconcile also acquires d.mu).
	go d.Reconcile(context.Background())
}

func (d *Dispatcher) pickResources(ctx context.Context, plan *IntentPlan) (domain.Account, domain.WorkerNode, bool, error) {
	accounts := d.availableAccounts()
	primaryWorkers := make([]domain.WorkerNode, 0, len(d.workers))
	standbyWorkers := make([]domain.WorkerNode, 0, len(d.workers))
	standbySlots := d.availableStandbySlots()
	for _, value := range d.workers {
		if !d.workerAvailableForPlan(value, plan) {
			continue
		}
		if d.workerRoles[value.ID] == domain.RoleStandby {
			standbyWorkers = append(standbyWorkers, value)
		} else {
			primaryWorkers = append(primaryWorkers, value)
		}
	}
	sort.Slice(primaryWorkers, func(i, j int) bool { return primaryWorkers[i].ID < primaryWorkers[j].ID })
	sort.Slice(standbyWorkers, func(i, j int) bool { return standbyWorkers[i].ID < standbyWorkers[j].ID })
	if len(standbyWorkers) > standbySlots {
		standbyWorkers = standbyWorkers[:standbySlots]
	}
	workers := primaryWorkers
	if len(workers) == 0 {
		workers = standbyWorkers
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

func (d *Dispatcher) availableAccounts() []domain.Account {
	accounts := make([]domain.Account, 0, len(d.accounts))
	recoveredAccounts := make([]domain.Account, 0)
	for _, value := range d.accounts {
		if until := d.quarantinedAccounts[value.ID]; until.After(d.now()) {
			continue
		} else if !until.IsZero() {
			delete(d.quarantinedAccounts, value.ID)
		}
		if !value.Enabled || d.accountBusy[value.ID] != "" || value.CooldownUntil.After(d.now()) {
			continue
		}
		if d.activeTaskGroup != "" {
			if tg, ok := d.accountReservations[value.ID]; !ok || tg != d.activeTaskGroup {
				continue
			}
		}
		if !value.CooldownUntil.IsZero() {
			recoveredAccounts = append(recoveredAccounts, value)
			continue
		}
		accounts = append(accounts, value)
	}
	if len(accounts) == 0 {
		accounts = append(accounts, recoveredAccounts...)
	}
	return accounts
}

func (d *Dispatcher) availableWorkerCount(plans []*IntentPlan) int {
	count := 0
	standbySlots := d.availableStandbySlots()
	countedStandby := 0
	for _, worker := range d.workers {
		if d.workerRoles[worker.ID] == domain.RoleStandby {
			if countedStandby >= standbySlots {
				continue
			}
		}
		for _, plan := range plans {
			if d.workerAvailableForPlan(worker, plan) {
				count++
				if d.workerRoles[worker.ID] == domain.RoleStandby {
					countedStandby++
				}
				break
			}
		}
	}
	return count
}

func (d *Dispatcher) workerAvailableForPlan(worker domain.WorkerNode, plan *IntentPlan) bool {
	if !worker.Enabled || d.workerBusy[worker.ID] != "" {
		return false
	}
	if _, failed := d.failedWorkers[worker.ID]; failed {
		// Check 5-minute cooldown: if expired, clear the failure and allow.
		if until, ok := d.workerCooldown[worker.ID]; ok && !until.After(d.now()) {
			delete(d.failedWorkers, worker.ID)
			delete(d.workerCooldown, worker.ID)
			if len(d.failedWorkers) == 0 {
				d.degraded = false
			}
		} else {
			return false
		}
	}
	if d.activeTaskGroup != "" {
		if tg, ok := d.workerReservations[worker.ID]; !ok || tg != d.activeTaskGroup {
			return false
		}
	}
	if d.workerRoles[worker.ID] == domain.RoleStandby && d.availableStandbySlots() <= 0 {
		return false
	}
	return true
}

func (d *Dispatcher) availableStandbySlots() int {
	hasPrimary := false
	failedPrimary := 0
	busyStandby := 0
	now := d.now()
	for workerID, role := range d.workerRoles {
		switch role {
		case domain.RolePrimary:
			hasPrimary = true
			if _, failed := d.failedWorkers[workerID]; !failed {
				continue
			}
			if until, ok := d.workerCooldown[workerID]; ok && !until.After(now) {
				continue
			}
			failedPrimary++
		case domain.RoleStandby:
			if d.workerBusy[workerID] != "" {
				busyStandby++
			}
		}
	}
	if !hasPrimary {
		return len(d.workerRoles)
	}
	slots := failedPrimary - busyStandby
	if slots < 0 {
		return 0
	}
	return slots
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
	intervalMS := d.retryIntervalMs
	if intervalMS <= 0 {
		intervalMS = 500
	}
	spec := domain.ExecutionSpec{AttemptID: id, IntentID: plan.Intent.ID, ProjectID: plan.Macro.ProjectID, ScreenID: plan.Macro.ScreenID, SKUID: plan.Macro.SKUID, Buyers: buyers, StartMode: mode, StartAt: plan.Macro.StartAt, Deadline: plan.Macro.Deadline, IntervalMS: intervalMS, StartDelayMS: d.startDelayMs, Credentials: account.Credentials}
	now := d.now()
	value := domain.ExecutionAttempt{ID: id, IntentID: plan.Intent.ID, SpecHash: spec.Hash(), AccountID: account.ID, WorkerID: worker.ID, State: domain.AttemptWaiting, CreatedAt: now, UpdatedAt: now}
	rpcCtx, cancel := context.WithTimeout(ctx, rpcTimeout)
	err = d.client.Submit(rpcCtx, worker, spec)
	cancel()
	if err != nil {
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
		rpcCtx, cancel := context.WithTimeout(ctx, rpcTimeout)
		err := d.client.Stop(rpcCtx, d.workers[current.value.WorkerID], current.value.ID)
		cancel()
		if err != nil && !errors.Is(err, context.Canceled) {
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
