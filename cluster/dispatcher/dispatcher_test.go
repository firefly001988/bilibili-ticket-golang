package dispatcher

import (
	"context"
	"fmt"
	"testing"
	"time"

	"bilibili-ticket-golang/cluster/domain"
)

type client struct {
	submitted         []domain.ExecutionSpec
	submitWorkers     []string
	states            map[string]WorkerStatus
	stopped           []string
	failWorker        string
	statusHadDeadline bool
	submitHadDeadline bool
	stopHadDeadline   bool
}

type selectiveResolver struct{ unavailable map[string]bool }

func (r selectiveResolver) Resolve(_ context.Context, accountID string, buyers []domain.Buyer) ([]domain.Buyer, error) {
	if r.unavailable[accountID] {
		return nil, ErrBuyerUnavailable
	}
	return buyers, nil
}

func (c *client) Submit(ctx context.Context, worker domain.WorkerNode, spec domain.ExecutionSpec) error {
	c.submitted = append(c.submitted, spec)
	c.submitWorkers = append(c.submitWorkers, worker.ID)
	if _, ok := ctx.Deadline(); ok {
		c.submitHadDeadline = true
	}
	if worker.ID == c.failWorker {
		return context.DeadlineExceeded
	}
	return nil
}
func (c *client) Status(ctx context.Context, _ domain.WorkerNode, id string) (WorkerStatus, error) {
	if _, ok := ctx.Deadline(); ok {
		c.statusHadDeadline = true
	}
	if status, ok := c.states[id]; ok {
		return status, nil
	}
	return WorkerStatus{State: domain.AttemptRunning}, nil
}
func (c *client) Stop(ctx context.Context, _ domain.WorkerNode, id string) error {
	c.stopped = append(c.stopped, id)
	if _, ok := ctx.Deadline(); ok {
		c.stopHadDeadline = true
	}
	return nil
}

type repo struct {
	intents  []domain.LogicalOrderIntent
	attempts []domain.ExecutionAttempt
}

func (r *repo) PutAttempt(_ context.Context, attempt domain.ExecutionAttempt) error {
	r.attempts = append(r.attempts, attempt)
	return nil
}
func (r *repo) PutIntent(_ context.Context, intent domain.LogicalOrderIntent) error {
	r.intents = append(r.intents, intent)
	return nil
}
func (r *repo) PutAccount(context.Context, domain.Account, *int64) error { return nil }
func (r *repo) MarkIntentSucceeded(context.Context, domain.LogicalOrderIntent, domain.ExecutionResult) error {
	return nil
}

func dispatchMacro(id string, priority int) domain.MacroTask {
	return domain.MacroTask{ID: id, ProjectID: 1, ScreenID: 2, SKUID: 3, EventDay: "2026-07-01", EventDayConfirmed: true, Priority: priority, Deadline: time.Now().Add(time.Hour)}
}
func dispatchIntent(id, macro string, weight int, buyers ...string) domain.LogicalOrderIntent {
	list := make([]domain.Buyer, len(buyers))
	for i, buyer := range buyers {
		list[i] = domain.Buyer{LogicalID: buyer, BuyerID: int64(i + 1)}
	}
	intent, _ := domain.NewIntent(id, dispatchMacro(macro, 0), domain.PhasePunctual, list, time.Now())
	intent.Weight = weight
	return intent
}
func resources() ([]domain.Account, []domain.WorkerNode) {
	return []domain.Account{{ID: "a1", Enabled: true}, {ID: "a2", Enabled: true}, {ID: "spare", Enabled: true}}, []domain.WorkerNode{{ID: "w1", Enabled: true}, {ID: "w2", Enabled: true}, {ID: "wspare", Enabled: true}}
}

func resourcesN(n int) ([]domain.Account, []domain.WorkerNode) {
	accounts := make([]domain.Account, n)
	workers := make([]domain.WorkerNode, n)
	for i := 0; i < n; i++ {
		accounts[i] = domain.Account{ID: fmt.Sprintf("a%d", i+1), Enabled: true}
		workers[i] = domain.WorkerNode{ID: fmt.Sprintf("w%d", i+1), Enabled: true}
	}
	return accounts, workers
}

func TestWeightedAllocationUsesLargestRemainder(t *testing.T) {
	c := &client{states: make(map[string]WorkerStatus)}
	d := New(c, nil, nil)
	accounts, workers := resourcesN(10)
	d.SetResources(accounts, workers)
	weights := map[string]int{"i10": 10, "i4a": 4, "i4b": 4, "i2": 2}
	for id, weight := range weights {
		intent := dispatchIntent(id, "m"+id, weight, "buyer-"+id)
		d.Add(IntentPlan{Macro: dispatchMacro("m"+id, 0), Intent: intent})
	}
	if err := d.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"i10": 5, "i4a": 2, "i4b": 2, "i2": 1}
	for id, expected := range want {
		if got := d.activeCount(id); got != expected {
			t.Fatalf("intent %s active=%d want=%d", id, got, expected)
		}
	}
}

func TestPriorityBreaksEqualWeightRemaindersAscending(t *testing.T) {
	c := &client{states: make(map[string]WorkerStatus)}
	d := New(c, nil, nil)
	accounts, workers := resourcesN(15)
	d.SetResources(accounts, workers)
	for priority := 0; priority < 4; priority++ {
		id := fmt.Sprintf("i%d", priority)
		intent := dispatchIntent(id, "m"+id, 1, "buyer-"+id)
		intent.Priority = priority
		d.Add(IntentPlan{Macro: dispatchMacro("m"+id, 0), Intent: intent})
	}
	if err := d.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"i0": 4, "i1": 4, "i2": 4, "i3": 3}
	for id, expected := range want {
		if got := d.activeCount(id); got != expected {
			t.Fatalf("intent %s active=%d want=%d", id, got, expected)
		}
	}
}

func TestTaskGroupAccountScope(t *testing.T) {
	c := &client{states: make(map[string]WorkerStatus)}
	d := New(c, nil, nil)
	accounts, workers := resourcesN(3)
	d.SetResources(accounts, workers)
	d.ReserveAccounts("g", []string{"a2"})
	d.ReserveWorkerPools("g", []string{"w1", "w2", "w3"}, nil)
	macro := dispatchMacro("m", 0)
	d.Add(IntentPlan{Macro: macro, Intent: dispatchIntent("i", "m", 10, "buyer")})
	if err := d.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	attempts := d.Attempts()
	if len(attempts) != 1 {
		t.Fatalf("expected exactly one attempt from reserved account, got %#v", attempts)
	}
	if attempts[0].AccountID != "a2" {
		t.Fatalf("unexpected account selected: %#v", attempts[0])
	}
}

func TestTaskGroupStandbyWorkersStayIdleUntilPrimaryFails(t *testing.T) {
	c := &client{states: make(map[string]WorkerStatus)}
	d := New(c, nil, nil)
	accounts, workers := resourcesN(4)
	d.SetResources(accounts, workers)
	d.ReserveAccounts("g", []string{"a1", "a2", "a3", "a4"})
	d.ReserveWorkerPools("g", []string{"w2"}, []string{"w4"})
	macro := dispatchMacro("m", 0)
	d.Add(IntentPlan{Macro: macro, Intent: dispatchIntent("i", "m", 10, "buyer")})
	if err := d.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(c.submitWorkers) != 1 {
		t.Fatalf("expected exactly primary worker, got %#v", c.submitWorkers)
	}
	if c.submitWorkers[0] != "w2" {
		t.Fatalf("worker scope/order mismatch: %#v", c.submitWorkers)
	}
}

func TestTaskGroupStandbyWorkersReplaceFailedPrimaryWorkers(t *testing.T) {
	c := &client{states: make(map[string]WorkerStatus)}
	d := New(c, nil, nil)
	accounts, workers := resourcesN(5)
	d.SetResources(accounts, workers)
	d.ReserveAccounts("g", []string{"a1", "a2", "a3", "a4", "a5"})
	d.ReserveWorkerPools("g", []string{"w1", "w2"}, []string{"w3", "w4", "w5"})
	d.failedWorkers["w1"] = d.now()
	macro := dispatchMacro("m", 0)
	d.Add(IntentPlan{Macro: macro, Intent: dispatchIntent("i", "m", 10, "buyer")})
	if err := d.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(c.submitWorkers) != 2 {
		t.Fatalf("expected one healthy primary plus one standby replacement, got %#v", c.submitWorkers)
	}
	if c.submitWorkers[0] != "w2" || c.submitWorkers[1] != "w3" {
		t.Fatalf("worker scope/order mismatch: %#v", c.submitWorkers)
	}
}

func TestDisarmMacroPersistsStoppedState(t *testing.T) {
	c := &client{states: make(map[string]WorkerStatus)}
	r := &repo{}
	d := New(c, r, nil)
	accounts, workers := resourcesN(1)
	d.SetResources(accounts, workers)
	d.Add(IntentPlan{Macro: dispatchMacro("m", 0), Intent: dispatchIntent("i", "m", 1, "buyer")})
	if err := d.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(c.submitted) != 1 {
		t.Fatalf("expected one attempt, got %#v", c.submitted)
	}

	d.DisarmMacro("m")

	if len(d.Plans()) != 0 {
		t.Fatalf("plans were not removed after disarm: %#v", d.Plans())
	}
	if len(r.intents) == 0 {
		t.Fatal("disarm did not persist stopped intent")
	}
	intent := r.intents[len(r.intents)-1]
	if intent.Armed || !intent.Terminal || intent.FailureReason != domain.FailureStopped {
		t.Fatalf("unexpected persisted intent state: %#v", intent)
	}
	if len(r.attempts) == 0 {
		t.Fatal("disarm did not persist stopped attempt")
	}
	attempt := r.attempts[len(r.attempts)-1]
	if attempt.State != domain.AttemptStopped || attempt.Result.Reason != domain.FailureStopped {
		t.Fatalf("unexpected persisted attempt state: %#v", attempt)
	}
}

func TestReplicasUseDistinctAccountsAndWorkers(t *testing.T) {
	c := &client{states: make(map[string]WorkerStatus)}
	d := New(c, nil, nil)
	accounts, workers := resources()
	d.SetResources(accounts, workers)
	m := dispatchMacro("m", 1)
	d.Add(IntentPlan{Macro: m, Intent: dispatchIntent("i", "m", 2, "buyer")})
	if err := d.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Relative-share allocation: a single intent receives all available slots.
	if len(c.submitted) != 3 {
		t.Fatalf("expected 3 attempts, got %d: %#v", len(c.submitted), c.submitted)
	}
	// Verify distinct accounts.
	seen := make(map[string]bool)
	for _, spec := range c.submitted {
		att := d.attempts[spec.AttemptID]
		if seen[att.value.AccountID] {
			t.Fatal("account reused concurrently")
		}
		seen[att.value.AccountID] = true
	}
}

func TestConflictingShapesSerializeByPriorityAndWinnerStopsSibling(t *testing.T) {
	c := &client{states: make(map[string]WorkerStatus)}
	d := New(c, nil, nil)
	// 2 workers, 2 accounts. Conflicting intents serialize; lower numeric
	// priority wins and receives the whole runnable allocation.
	accounts := []domain.Account{{ID: "a1", Enabled: true}, {ID: "a2", Enabled: true}}
	workers := []domain.WorkerNode{{ID: "w1", Enabled: true}, {ID: "w2", Enabled: true}}
	d.SetResources(accounts, workers)
	high := dispatchMacro("high", 10)
	low := dispatchMacro("low", 1)
	// Same buyer -> same ShapeHash -> conflict.
	lowIntent := dispatchIntent("low-i", "low", 2, "buyer")
	highIntent := dispatchIntent("high-i", "high", 2, "buyer")
	lowIntent.Priority = 10
	highIntent.Priority = 0
	d.Add(IntentPlan{Macro: low, Intent: lowIntent})
	d.Add(IntentPlan{Macro: high, Intent: highIntent})
	if err := d.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Both workers go to high (low is conflicted).
	if len(c.submitted) != 2 || c.submitted[0].IntentID != "high-i" || c.submitted[1].IntentID != "high-i" {
		t.Fatalf("priority/conflict failed: %#v", c.submitted)
	}
	c.states[c.submitted[0].AttemptID] = WorkerStatus{State: domain.AttemptSucceeded, Result: domain.ExecutionResult{AttemptID: c.submitted[0].AttemptID, Success: true, State: domain.AttemptSucceeded}}
	if err := d.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Winning terminates all conflicting intents (low).  Low never had an
	// active attempt, but its intent is now terminal.
	lowIntent = d.plans["low-i"].Intent
	if !lowIntent.Terminal || lowIntent.FailureReason != domain.FailureUnrecoverable {
		t.Fatalf("conflicting low intent was not terminated: terminal=%v reason=%s", lowIntent.Terminal, lowIntent.FailureReason)
	}
}

func TestStandbyIsIdleUntilMachineFailure(t *testing.T) {
	c := &client{states: make(map[string]WorkerStatus)}
	d := New(c, nil, nil)
	accounts, workers := resources()
	// Two intents A and B with equal weight=2 and 3 workers. Largest remainder
	// gives the extra slot to the lower numeric priority.
	d.SetResources(accounts, workers)
	aIntent := dispatchIntent("a", "ma", 2, "buyer-a")
	bIntent := dispatchIntent("b", "mb", 2, "buyer-b")
	aIntent.Priority = 0
	bIntent.Priority = 1
	d.Add(IntentPlan{Macro: dispatchMacro("ma", 0), Intent: aIntent})
	d.Add(IntentPlan{Macro: dispatchMacro("mb", 0), Intent: bIntent})
	_ = d.Reconcile(context.Background())
	aCount := d.activeCount(aIntent.ID)
	bCount := d.activeCount(bIntent.ID)
	if aCount != 2 || bCount != 1 {
		t.Fatalf("allocation mismatch: A=%d B=%d (submitted=%d)", aCount, bCount, len(c.submitted))
	}
	// Find A's first attempt and mark it failed.
	var firstAID string
	for _, spec := range c.submitted {
		if d.attempts[spec.AttemptID].planID == aIntent.ID {
			firstAID = spec.AttemptID
			break
		}
	}
	// A's first attempt "fails" (generic failure — worker remains healthy).
	c.states[firstAID] = WorkerStatus{State: domain.AttemptFailed, Result: domain.ExecutionResult{Reason: domain.FailureDeadline, State: domain.AttemptFailed}}
	_ = d.Reconcile(context.Background())
	// Replacement should have been dispatched (A back to 2 active).
	if d.activeCount(aIntent.ID) != 2 {
		t.Fatalf("replacement not dispatched for A: active=%d, submitted=%d", d.activeCount(aIntent.ID), len(c.submitted))
	}
}

func TestSwitchToReflowDoesNotReplaceStoppedPunctualAttempt(t *testing.T) {
	c := &client{states: make(map[string]WorkerStatus)}
	d := New(c, nil, nil)
	// Single worker so exactly 1 attempt is dispatched.
	accounts := []domain.Account{{ID: "a1", Enabled: true}}
	workers := []domain.WorkerNode{{ID: "w1", Enabled: true}}
	d.SetResources(accounts, workers)
	d.Add(IntentPlan{Macro: dispatchMacro("m", 1), Intent: dispatchIntent("i", "m", 1, "buyer")})
	if err := d.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	id := c.submitted[0].AttemptID
	if err := d.SwitchToReflow(context.Background()); err != nil {
		t.Fatal(err)
	}
	c.states[id] = WorkerStatus{State: domain.AttemptStopped, Result: domain.ExecutionResult{State: domain.AttemptStopped}}
	if err := d.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(c.submitted) != 1 {
		t.Fatalf("punctual attempt was recreated after phase switch: %#v", c.submitted)
	}
	if !d.PunctualStopped() {
		t.Fatal("punctual phase should be fully stopped")
	}
}

func TestRestoreAttemptReservesResourcesAndKeepsWorkerResult(t *testing.T) {
	c := &client{states: make(map[string]WorkerStatus)}
	d := New(c, nil, nil)
	// 1 worker so only 1 attempt can be active.
	accounts := []domain.Account{{ID: "a1", Enabled: true}}
	workers := []domain.WorkerNode{{ID: "w1", Enabled: true}}
	d.SetResources(accounts, workers)
	plan := IntentPlan{Macro: dispatchMacro("m", 1), Intent: dispatchIntent("i", "m", 1, "buyer")}
	d.Add(plan)
	restored := domain.ExecutionAttempt{ID: "restored", IntentID: plan.Intent.ID, AccountID: "a1", WorkerID: "w1", State: domain.AttemptRunning, CreatedAt: time.Now()}
	if err := d.RestoreAttempt(restored); err != nil {
		t.Fatal(err)
	}
	c.states[restored.ID] = WorkerStatus{State: domain.AttemptFailed, Result: domain.ExecutionResult{State: domain.AttemptFailed, Reason: domain.FailureDeadline, Message: "expired", Credentials: domain.Credentials{Version: 9}}}
	if err := d.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := d.attempts[restored.ID].value.Result
	if got.Reason != domain.FailureDeadline || got.Credentials.Version != 0 {
		t.Fatalf("unexpected persisted result: %#v", got)
	}
}

func TestAmbiguousSubmitFailureIsolatesAccountAndUsesStandby(t *testing.T) {
	c := &client{states: make(map[string]WorkerStatus), failWorker: "w1"}
	d := New(c, nil, nil)
	accounts, workers := resources()
	// 2 intents A (weight=2) and B (weight=2) with 2 workers: each receives 1 slot.
	workers2 := []domain.WorkerNode{workers[0], workers[2]} // w1, wspare
	d.SetResources(accounts, workers2)
	aIntent := dispatchIntent("a", "ma", 2, "buyer-a")
	bIntent := dispatchIntent("b", "mb", 2, "buyer-b")
	d.Add(IntentPlan{Macro: dispatchMacro("ma", 1), Intent: aIntent})
	d.Add(IntentPlan{Macro: dispatchMacro("mb", 1), Intent: bIntent})
	if err := d.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	// w1 failed; both intents dispatched.
	if len(c.submitted) < 2 {
		t.Fatalf("expected at least 2 attempts, got %#v", c.submitted)
	}
	first := d.attempts[c.submitted[0].AttemptID]
	if first.value.State != domain.AttemptFailed || first.value.Result.Reason != domain.FailureWorkerLost {
		t.Fatalf("ambiguous attempt was not retained: %#v", first.value)
	}
	if first.value.AccountID == d.attempts[c.submitted[1].AttemptID].value.AccountID {
		t.Fatalf("unsafe failover resources: first=%#v second=%#v", first.value, d.attempts[c.submitted[1].AttemptID].value)
	}
}

func TestAccountWithoutBuyerMappingIsSkipped(t *testing.T) {
	c := &client{states: make(map[string]WorkerStatus)}
	d := New(c, nil, selectiveResolver{unavailable: map[string]bool{"a1": true}})
	accounts, workers := resources()
	d.SetResources(accounts, workers)
	d.Add(IntentPlan{Macro: dispatchMacro("m", 1), Intent: dispatchIntent("i", "m", 1, "buyer")})
	if err := d.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Dispatches only to accounts that have the requested buyer mapping.
	if len(c.submitted) < 1 {
		t.Fatalf("expected at least one attempt, got %#v", c.submitted)
	}
	for _, spec := range c.submitted {
		att := d.attempts[spec.AttemptID]
		if att.value.AccountID == "a1" {
			t.Fatalf("unavailable account a1 was selected: %#v", att.value)
		}
	}
}

func TestHealthyIdleWorkerReturnsToResourcePool(t *testing.T) {
	d := New(&client{states: make(map[string]WorkerStatus)}, nil, nil)
	d.failedWorkers["worker"] = time.Now()
	d.degraded = true
	d.MarkWorkerHealthy("worker")
	if _, failed := d.failedWorkers["worker"]; failed || d.degraded {
		t.Fatalf("worker was not rehabilitated: failed=%v degraded=%v", failed, d.degraded)
	}
}

func TestUnarmedRestoredIntentIsNotDispatched(t *testing.T) {
	c := &client{states: make(map[string]WorkerStatus)}
	d := New(c, nil, nil)
	accounts, workers := resources()
	d.SetResources(accounts, workers)
	intent := dispatchIntent("legacy", "m", 1, "buyer")
	intent.Armed = false
	d.Add(IntentPlan{Macro: dispatchMacro("m", 1), Intent: intent})
	if err := d.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(c.submitted) != 0 {
		t.Fatalf("unarmed intent was dispatched: %#v", c.submitted)
	}
}

func TestWorkerRPCsUseShortDeadline(t *testing.T) {
	c := &client{states: make(map[string]WorkerStatus)}
	d := New(c, nil, nil)
	// 1 worker so exactly 1 attempt is dispatched.
	accounts := []domain.Account{{ID: "a1", Enabled: true}}
	workers := []domain.WorkerNode{{ID: "w1", Enabled: true}}
	d.SetResources(accounts, workers)
	plan := IntentPlan{Macro: dispatchMacro("m", 1), Intent: dispatchIntent("i", "m", 1, "buyer")}
	d.Add(plan)
	if err := d.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !c.submitHadDeadline {
		t.Fatal("submit RPC did not receive a deadline")
	}
	id := c.submitted[0].AttemptID
	c.states[id] = WorkerStatus{State: domain.AttemptSucceeded, Result: domain.ExecutionResult{AttemptID: id, Success: true, State: domain.AttemptSucceeded}}
	if err := d.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !c.statusHadDeadline {
		t.Fatal("status RPC did not receive a deadline")
	}
}

func TestFailedWorkerIsNotPickedForNewAttempt(t *testing.T) {
	c := &client{states: make(map[string]WorkerStatus)}
	d := New(c, nil, nil)
	accounts, workers := resources()
	d.SetResources(accounts, workers)
	d.failedWorkers["w1"] = time.Now()
	d.Add(IntentPlan{Macro: dispatchMacro("m", 1), Intent: dispatchIntent("i", "m", 1, "buyer")})
	if err := d.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Failed worker is skipped and must never appear.
	for _, wid := range c.submitWorkers {
		if wid == "w1" {
			t.Fatalf("failed worker was selected: %#v", c.submitWorkers)
		}
	}
	if len(c.submitWorkers) == 0 {
		t.Fatal("no workers dispatched at all")
	}
}

func TestWinnerTerminalsAllConflictingIntents(t *testing.T) {
	c := &client{states: make(map[string]WorkerStatus)}
	r := &repo{}
	d := New(c, r, nil)
	// 1 worker — lower numeric priority receives the single slot.
	accounts := []domain.Account{{ID: "a1", Enabled: true}}
	workers := []domain.WorkerNode{{ID: "w1", Enabled: true}}
	d.SetResources(accounts, workers)
	high := dispatchMacro("high", 10)
	low := dispatchMacro("low", 1)
	highIntent := dispatchIntent("high-i", "high", 1, "buyer")
	lowIntent := dispatchIntent("low-i", "low", 1, "buyer")
	highIntent.Priority = 0
	lowIntent.Priority = 10
	d.Add(IntentPlan{Macro: low, Intent: lowIntent})
	d.Add(IntentPlan{Macro: high, Intent: highIntent})
	if err := d.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(c.submitted) != 1 || c.submitted[0].IntentID != "high-i" {
		t.Fatalf("expected only high priority attempt, got %#v", c.submitted)
	}
	c.states[c.submitted[0].AttemptID] = WorkerStatus{State: domain.AttemptSucceeded, Result: domain.ExecutionResult{AttemptID: c.submitted[0].AttemptID, Success: true, State: domain.AttemptSucceeded}}
	if err := d.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := d.plans["low-i"].Intent
	if !got.Terminal || got.FailureReason != domain.FailureUnrecoverable {
		t.Fatalf("conflicting intent was not terminal: %#v", got)
	}
	if len(r.intents) != 1 || r.intents[0].ID != "low-i" || !r.intents[0].Terminal {
		t.Fatalf("terminal conflict intent was not persisted: %#v", r.intents)
	}
}
