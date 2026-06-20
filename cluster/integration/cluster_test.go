package integration

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"bilibili-ticket-golang/cluster/dispatcher"
	"bilibili-ticket-golang/cluster/domain"
	"bilibili-ticket-golang/cluster/employer"
	"bilibili-ticket-golang/cluster/executor"
	"bilibili-ticket-golang/cluster/planner"
	"bilibili-ticket-golang/cluster/storage"
	"bilibili-ticket-golang/cluster/worker"
)

type successfulBackend struct{}

func (successfulBackend) Attempt(context.Context, domain.ExecutionSpec) executor.Outcome {
	return executor.Outcome{Code: 0, OrderID: "order-1"}
}
func (successfulBackend) Credentials() domain.Credentials { return domain.Credentials{Version: 2} }

type resolver struct{}

func (resolver) Resolve(_ context.Context, _ string, buyers []domain.Buyer) ([]domain.Buyer, error) {
	result := append([]domain.Buyer(nil), buyers...)
	for i := range result {
		result[i].BuyerID = int64(i + 1)
	}
	return result, nil
}

func TestEmployerWorkerPlanningDispatchAndSuccessCommit(t *testing.T) {
	ctx := context.Background()
	repository, err := storage.Open(filepath.Join(t.TempDir(), "employer.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	workerServer, err := worker.NewServer(worker.Config{BearerKey: "secret", DataDir: t.TempDir(), PollInterval: 10 * time.Second}, func(domain.ExecutionSpec) (executor.Backend, error) { return successfulBackend{}, nil })
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(workerServer.Handler())
	defer httpServer.Close()

	client := employer.NewWorkerClient()
	node := domain.WorkerNode{ID: "w", BaseURL: httpServer.URL, Role: domain.RolePrimary, Enabled: true}
	client.SetKey(node.ID, "secret")
	account := domain.Account{ID: "a", Role: domain.RolePrimary, Enabled: true, Credentials: domain.Credentials{Version: 1}}
	if err := repository.PutAccount(ctx, account, nil); err != nil {
		t.Fatal(err)
	}
	if err := repository.PutWorker(ctx, node); err != nil {
		t.Fatal(err)
	}
	d := dispatcher.New(client, repository, resolver{})
	d.SetResources([]domain.Account{account}, []domain.WorkerNode{node})

	group := domain.TaskGroup{ID: "g"}
	if err := repository.PutTaskGroup(ctx, group); err != nil {
		t.Fatal(err)
	}
	macro := domain.MacroTask{ID: "m", TaskGroupID: group.ID, ProjectID: 1, ScreenID: 2, SKUID: 3, EventDay: "2026-07-01", EventDayConfirmed: true, OrderCapacity: 4, DesiredReplicas: 1, HardConcurrency: 1, Deadline: time.Now().Add(time.Minute)}
	if err := repository.PutMacroTask(ctx, macro); err != nil {
		t.Fatal(err)
	}
	purchase := domain.PurchaseGroup{ID: "p", MacroTaskID: macro.ID, Buyers: []domain.Buyer{{LogicalID: "buyer"}}}
	intents, err := planner.Plan(macro, []domain.PurchaseGroup{purchase}, domain.PhasePunctual, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.PutIntent(ctx, intents[0]); err != nil {
		t.Fatal(err)
	}
	d.Add(dispatcher.IntentPlan{Macro: macro, Intent: intents[0]})
	if err := d.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := d.Reconcile(ctx); err != nil {
			t.Fatal(err)
		}
		stored, err := repository.ListIntents(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(stored) == 1 && stored[0].Succeeded {
			attempts, listErr := repository.ListAttempts(ctx)
			if listErr != nil {
				t.Fatal(listErr)
			}
			if len(attempts) != 1 || attempts[0].Result.OrderID != "order-1" || attempts[0].Result.Credentials.Version != 0 {
				t.Fatalf("unexpected persisted attempt result: %#v", attempts)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("successful worker result was not committed by employer")
}
