package dispatcher

import (
	"context"
	"testing"
	"time"

	"bilibili-ticket-golang/cluster/domain"
)

type client struct {
	submitted  []domain.ExecutionSpec
	states     map[string]WorkerStatus
	stopped    []string
	failWorker string
}

type selectiveResolver struct{ unavailable map[string]bool }

func (r selectiveResolver) Resolve(_ context.Context, accountID string, buyers []domain.Buyer) ([]domain.Buyer, error) {
	if r.unavailable[accountID] {
		return nil, ErrBuyerUnavailable
	}
	return buyers, nil
}

func (c *client) Submit(_ context.Context, worker domain.WorkerNode, spec domain.ExecutionSpec) error {
	c.submitted = append(c.submitted, spec)
	if worker.ID == c.failWorker {
		return context.DeadlineExceeded
	}
	return nil
}
func (c *client) Status(_ context.Context, _ domain.WorkerNode, id string) (WorkerStatus, error) {
	if status, ok := c.states[id]; ok {
		return status, nil
	}
	return WorkerStatus{State: domain.AttemptRunning}, nil
}
func (c *client) Stop(_ context.Context, _ domain.WorkerNode, id string) error {
	c.stopped = append(c.stopped, id)
	return nil
}

func dispatchMacro(id string, priority, replicas int) domain.MacroTask {
	return domain.MacroTask{ID: id, ProjectID: 1, ScreenID: 2, SKUID: 3, EventDay: "2026-07-01", EventDayConfirmed: true, Priority: priority, DesiredReplicas: replicas, HardConcurrency: replicas, Deadline: time.Now().Add(time.Hour)}
}
func dispatchIntent(id, macro string, buyers ...string) domain.LogicalOrderIntent {
	list := make([]domain.Buyer, len(buyers))
	for i, buyer := range buyers {
		list[i] = domain.Buyer{LogicalID: buyer, BuyerID: int64(i + 1)}
	}
	intent, _ := domain.NewIntent(id, dispatchMacro(macro, 0, 1), domain.PhasePunctual, list, time.Now())
	return intent
}
func resources() ([]domain.Account, []domain.WorkerNode) {
	return []domain.Account{{ID: "a1", Enabled: true, Role: domain.RolePrimary}, {ID: "a2", Enabled: true, Role: domain.RolePrimary}, {ID: "spare", Enabled: true, Role: domain.RoleStandby}}, []domain.WorkerNode{{ID: "w1", Enabled: true, Role: domain.RolePrimary}, {ID: "w2", Enabled: true, Role: domain.RolePrimary}, {ID: "wspare", Enabled: true, Role: domain.RoleStandby}}
}

func TestReplicasUseDistinctAccountsAndWorkers(t *testing.T) {
	c := &client{states: make(map[string]WorkerStatus)}
	d := New(c, nil, nil)
	accounts, workers := resources()
	d.SetResources(accounts, workers)
	m := dispatchMacro("m", 1, 2)
	d.Add(IntentPlan{Macro: m, Intent: dispatchIntent("i", "m", "buyer")})
	if err := d.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(c.submitted) != 2 || c.submitted[0].AttemptID == c.submitted[1].AttemptID {
		t.Fatalf("replicas not dispatched: %#v", c.submitted)
	}
	if d.attempts[c.submitted[0].AttemptID].value.AccountID == d.attempts[c.submitted[1].AttemptID].value.AccountID {
		t.Fatal("account reused concurrently")
	}
}

func TestConflictingShapesSerializeByPriorityAndWinnerStopsSibling(t *testing.T) {
	c := &client{states: make(map[string]WorkerStatus)}
	d := New(c, nil, nil)
	accounts, workers := resources()
	d.SetResources(accounts, workers)
	high := dispatchMacro("high", 10, 2)
	low := dispatchMacro("low", 1, 1)
	d.Add(IntentPlan{Macro: low, Intent: dispatchIntent("low-i", "low", "buyer")})
	d.Add(IntentPlan{Macro: high, Intent: dispatchIntent("high-i", "high", "buyer")})
	if err := d.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(c.submitted) != 2 || c.submitted[0].IntentID != "high-i" {
		t.Fatalf("priority/conflict failed: %#v", c.submitted)
	}
	c.states[c.submitted[0].AttemptID] = WorkerStatus{State: domain.AttemptSucceeded, Result: domain.ExecutionResult{AttemptID: c.submitted[0].AttemptID, Success: true, State: domain.AttemptSucceeded}}
	if err := d.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(c.stopped) != 1 {
		t.Fatalf("sibling not stopped: %#v", c.stopped)
	}
}

func TestStandbyIsIdleUntilMachineFailure(t *testing.T) {
	c := &client{states: make(map[string]WorkerStatus)}
	d := New(c, nil, nil)
	accounts, workers := resources()
	workers = []domain.WorkerNode{workers[0], workers[2]}
	d.SetResources(accounts, workers)
	m := dispatchMacro("m", 1, 1)
	d.Add(IntentPlan{Macro: m, Intent: dispatchIntent("i", "m", "buyer")})
	_ = d.Reconcile(context.Background())
	id := c.submitted[0].AttemptID
	c.states[id] = WorkerStatus{State: domain.AttemptFailed, Result: domain.ExecutionResult{Reason: domain.FailureHTTP412, State: domain.AttemptFailed}}
	_ = d.Reconcile(context.Background())
	if len(c.submitted) != 2 {
		t.Fatalf("replacement not dispatched: %#v", c.submitted)
	}
	second := d.attempts[c.submitted[1].AttemptID].value
	if second.WorkerID != "wspare" {
		t.Fatalf("standby worker not activated: %#v", second)
	}
}

func TestSwitchToReflowDoesNotReplaceStoppedPunctualAttempt(t *testing.T) {
	c := &client{states: make(map[string]WorkerStatus)}
	d := New(c, nil, nil)
	accounts, workers := resources()
	d.SetResources(accounts, workers)
	d.Add(IntentPlan{Macro: dispatchMacro("m", 1, 1), Intent: dispatchIntent("i", "m", "buyer")})
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
	accounts, workers := resources()
	d.SetResources(accounts, workers)
	plan := IntentPlan{Macro: dispatchMacro("m", 1, 1), Intent: dispatchIntent("i", "m", "buyer")}
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
	d.SetResources(accounts, []domain.WorkerNode{workers[0], workers[2]})
	d.Add(IntentPlan{Macro: dispatchMacro("m", 1, 1), Intent: dispatchIntent("i", "m", "buyer")})
	if err := d.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(c.submitted) != 2 {
		t.Fatalf("expected immediate standby retry, got %#v", c.submitted)
	}
	first := d.attempts[c.submitted[0].AttemptID]
	second := d.attempts[c.submitted[1].AttemptID]
	if first.value.State != domain.AttemptFailed || first.value.Result.Reason != domain.FailureWorkerLost {
		t.Fatalf("ambiguous attempt was not retained: %#v", first.value)
	}
	if first.value.AccountID == second.value.AccountID || second.value.WorkerID != "wspare" {
		t.Fatalf("unsafe failover resources: first=%#v second=%#v", first.value, second.value)
	}
}

func TestAccountWithoutBuyerMappingIsSkipped(t *testing.T) {
	c := &client{states: make(map[string]WorkerStatus)}
	d := New(c, nil, selectiveResolver{unavailable: map[string]bool{"a1": true}})
	accounts, workers := resources()
	d.SetResources(accounts, workers)
	d.Add(IntentPlan{Macro: dispatchMacro("m", 1, 1), Intent: dispatchIntent("i", "m", "buyer")})
	if err := d.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(c.submitted) != 1 {
		t.Fatalf("expected one attempt, got %#v", c.submitted)
	}
	attempt := d.attempts[c.submitted[0].AttemptID].value
	if attempt.AccountID != "a2" {
		t.Fatalf("account without buyer mapping was selected: %#v", attempt)
	}
}
