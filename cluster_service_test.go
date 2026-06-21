package main

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
	macro := domain.MacroTask{ID: "macro", TaskGroupID: "group", ProjectID: 1, ScreenID: 2, SKUID: 3, EventDay: "2026-07-01", EventDayConfirmed: true, OrderCapacity: 2, DesiredReplicas: 2, HardConcurrency: 2}
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

func TestClusterServiceDeletesIdleResources(t *testing.T) {
	service := testClusterService(t)
	ctx := context.Background()
	if err := service.repository.PutAccount(ctx, domain.Account{ID: "account", Enabled: true}, nil); err != nil {
		t.Fatal(err)
	}
	if err := service.repository.PutWorker(ctx, domain.WorkerNode{ID: "remote", BaseURL: "http://worker", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := service.repository.PutWorkerKey(ctx, "remote", "secret"); err != nil {
		t.Fatal(err)
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
