package cluster_service

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"bilibili-ticket-golang/cluster/domain"
	clusterstorage "bilibili-ticket-golang/cluster/storage"
)

func testClusterService(t *testing.T) *ClusterService {
	t.Helper()
	repository, err := clusterstorage.Open(filepath.Join(t.TempDir(), "employer.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	return NewClusterService(repository)
}

func document(t *testing.T, value any) string {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestClusterServiceValidatesRunnableMacroAndPurchaseShape(t *testing.T) {
	service := testClusterService(t)
	if err := service.SaveTaskGroup(`{"id":"group","name":"test"}`); err != nil {
		t.Fatal(err)
	}
	macro := domain.MacroTask{ID: "macro", TaskGroupID: "group", ProjectID: 1, ScreenID: 2, SKUID: 3, EventDay: "2026-07-01", EventDayConfirmed: true, OrderCapacity: 2}
	if err := service.SaveMacro(document(t, macro)); err == nil {
		t.Fatal("expected missing execution window to be rejected")
	}
	macro.StartAt = time.Now().Add(time.Minute)
	macro.Deadline = macro.StartAt.Add(time.Hour)
	if err := service.SaveMacro(document(t, macro)); err != nil {
		t.Fatal(err)
	}
	if err := service.StartMacro(macro.ID); err == nil {
		t.Fatal("starting a macro without purchase groups must fail")
	}
	tooLarge := domain.PurchaseGroup{MacroTaskID: macro.ID, Buyers: []domain.Buyer{{LogicalID: "a"}, {LogicalID: "b"}, {LogicalID: "c"}}}
	if err := service.SavePurchaseGroup(document(t, tooLarge)); err == nil {
		t.Fatal("expected oversized purchase group to be rejected")
	}
	duplicate := domain.PurchaseGroup{MacroTaskID: macro.ID, Buyers: []domain.Buyer{{LogicalID: "a"}, {LogicalID: "a"}}}
	if err := service.SavePurchaseGroup(document(t, duplicate)); err == nil {
		t.Fatal("expected duplicate logical buyer to be rejected")
	}
	valid := domain.PurchaseGroup{MacroTaskID: macro.ID, Buyers: []domain.Buyer{{LogicalID: "a"}, {LogicalID: "b"}}, AllowSplit: true}
	if err := service.SavePurchaseGroup(document(t, valid)); err != nil {
		t.Fatal(err)
	}
	if err := service.StartMacro(macro.ID); err == nil {
		t.Fatal("starting without an eligible account and worker must not silently succeed")
	}
	snapshot, err := service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Macros) != 1 || len(snapshot.Macros[0].PurchaseGroups) != 1 || len(snapshot.Macros[0].PurchaseGroups[0].Buyers) != 2 {
		t.Fatalf("purchase groups missing from macro summary: %#v", snapshot.Macros)
	}
	macro.Priority = 9
	if err := service.SaveMacro(document(t, macro)); err != nil {
		t.Fatal(err)
	}
	if err := service.DeleteMacro(macro.ID); err != nil {
		t.Fatal(err)
	}
	snapshot, err = service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Macros) != 0 || len(snapshot.Attempts) != 0 {
		t.Fatalf("macro cascade was not removed: %#v", snapshot)
	}
}

func TestClusterSnapshotUsesEmptyBuyerArray(t *testing.T) {
	service := testClusterService(t)
	snapshot, err := service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Buyers == nil {
		t.Fatal("empty buyers must be represented as an empty array, not null")
	}
}

func TestClusterServiceEditsAndDeletesPurchaseGroups(t *testing.T) {
	service := testClusterService(t)
	if err := service.SaveTaskGroup(`{"id":"group","name":"test"}`); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	macro := domain.MacroTask{ID: "macro", TaskGroupID: "group", ProjectID: 1, ScreenID: 2, SKUID: 3, EventDay: "2026-07-01", EventDayConfirmed: true, OrderCapacity: 4, StartAt: now.Add(time.Minute), Deadline: now.Add(time.Hour)}
	if err := service.SaveMacro(document(t, macro)); err != nil {
		t.Fatal(err)
	}
	createdAt := now.Add(-time.Minute)
	group := domain.PurchaseGroup{ID: "purchase", MacroTaskID: macro.ID, Buyers: []domain.Buyer{{LogicalID: "a", Name: "A"}}, CreatedAt: createdAt}
	if err := service.SavePurchaseGroup(document(t, group)); err != nil {
		t.Fatal(err)
	}
	if err := service.SavePurchaseGroup(`{"id":"purchase","macroTaskId":"macro","buyers":[{"logicalId":"b","name":"B"}],"allowSplit":true,"weight":"3","priority":"2"}`); err != nil {
		t.Fatal(err)
	}
	snapshot, err := service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	saved := snapshot.Macros[0].PurchaseGroups
	if len(saved) != 1 || saved[0].Buyers[0].LogicalID != "b" || !saved[0].AllowSplit || saved[0].Weight != 3 || saved[0].Priority != 2 || !saved[0].CreatedAt.Equal(createdAt) {
		t.Fatalf("purchase group was not updated in place: %#v", saved)
	}
	if err := service.DeletePurchaseGroup(macro.ID, group.ID); err != nil {
		t.Fatal(err)
	}
	snapshot, err = service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Macros[0].PurchaseGroups) != 0 {
		t.Fatalf("purchase group was not deleted: %#v", snapshot.Macros[0].PurchaseGroups)
	}
}

func TestClusterServicePlansTaskGroupAndRequiresHealthyWorker(t *testing.T) {
	service := testClusterService(t)
	ctx := context.Background()
	// Add a local worker so StartTaskGroup has something to dispatch to.
	worker := domain.WorkerNode{ID: "w", Name: "test-worker", Type: domain.WorkerTypeLocal, Enabled: true}
	if err := service.repository.PutWorker(ctx, worker); err != nil {
		t.Fatal(err)
	}
	// Add an account mapped to buyer "a".
	account := domain.Account{ID: "acct", Enabled: true, Credentials: domain.Credentials{Version: 1}}
	if err := service.repository.PutAccount(ctx, account, nil); err != nil {
		t.Fatal(err)
	}
	// Map buyer "a" to account "acct".
	mapping := domain.AccountBuyerMapping{AccountID: "acct", LogicalBuyerID: "a", BuyerID: 1}
	if err := service.repository.PutBuyerMapping(ctx, mapping); err != nil {
		t.Fatal(err)
	}
	if err := service.SaveTaskGroup(`{"id":"group","name":"test","accountIds":["acct"]}`); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	macro := domain.MacroTask{ID: "macro", TaskGroupID: "group", ProjectID: 1, ScreenID: 2, SKUID: 3, EventDay: "2026-07-01", EventDayConfirmed: true, OrderCapacity: 4, StartAt: now.Add(time.Minute), Deadline: now.Add(time.Hour)}
	if err := service.SaveMacro(document(t, macro)); err != nil {
		t.Fatal(err)
	}
	group := domain.PurchaseGroup{ID: "purchase", MacroTaskID: macro.ID, Buyers: []domain.Buyer{{LogicalID: "a", Name: "A"}}, CreatedAt: now}
	if err := service.SavePurchaseGroup(document(t, group)); err != nil {
		t.Fatal(err)
	}
	if err := service.StartTaskGroup("group", `["w"]`); err == nil {
		t.Fatal("task group start must not silently succeed without a healthy worker client")
	}
	intents, err := service.repository.ListIntents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(intents) != 1 || intents[0].MacroTaskID != macro.ID || intents[0].Armed || !intents[0].Terminal || intents[0].FailureReason != domain.FailureStopped {
		t.Fatalf("task group start rollback did not persist a stopped intent: %#v", intents)
	}
}

func TestClusterServiceRejectsStartingAnotherActiveTaskGroup(t *testing.T) {
	service := testClusterService(t)
	ctx := context.Background()
	if err := service.SaveTaskGroup(`{"id":"group-a","name":"A"}`); err != nil {
		t.Fatal(err)
	}
	if err := service.SaveTaskGroup(`{"id":"group-b","name":"B","accountIds":["account"],"primaryWorkerIds":["w"]}`); err != nil {
		t.Fatal(err)
	}
	if err := service.repository.PutWorker(ctx, domain.WorkerNode{ID: "w", Name: "worker", Type: domain.WorkerTypeLocal, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	service.dispatcher.ReserveWorkerPools("group-a", []string{"w"}, nil)
	if err := service.StartTaskGroup("group-b", ""); err == nil {
		t.Fatal("starting another task group while one is active must be rejected")
	}
}

func TestClusterServiceDeletesIdleResources(t *testing.T) {
	service := testClusterService(t)
	ctx := context.Background()
	if err := service.repository.PutAccount(ctx, domain.Account{ID: "account", Enabled: true}, nil); err != nil {
		t.Fatal(err)
	}
	if err := service.repository.PutWorker(ctx, domain.WorkerNode{ID: "remote", Address: "worker:18080", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := service.repository.PutWorkerTLS(ctx, "remote", domain.WorkerTLSConfig{
		CACertPEM:     []byte("test-ca"),
		ClientCertPEM: []byte("test-cert"),
		ClientKeyPEM:  []byte("test-key"),
	}); err != nil {
		t.Fatal(err)
	}
	beforeDelete, err := service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(beforeDelete.Accounts) != 1 || beforeDelete.Accounts[0].CooldownUntil != nil {
		t.Fatalf("zero cooldown must be omitted: %#v", beforeDelete.Accounts)
	}
	if err := service.DeleteAccount("account"); err != nil {
		t.Fatal(err)
	}
	if err := service.DeleteWorker("remote"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Accounts) != 0 || len(snapshot.Workers) != 0 {
		t.Fatalf("resources were not deleted: %#v", snapshot)
	}
	if err := service.DeleteWorker("local"); err == nil {
		t.Fatal("local worker deletion must be rejected")
	}
}
