package main

import (
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
}
