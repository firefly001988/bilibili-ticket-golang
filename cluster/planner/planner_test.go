package planner

import (
	"testing"
	"time"

	"bilibili-ticket-golang/cluster/domain"
)

func buyers(ids ...string) []domain.Buyer {
	result := make([]domain.Buyer, len(ids))
	for i, id := range ids {
		result[i] = domain.Buyer{LogicalID: id}
	}
	return result
}
func macro() domain.MacroTask {
	return domain.MacroTask{ID: "m", ProjectID: 1, ScreenID: 2, SKUID: 3, EventDay: "2026-07-01", EventDayConfirmed: true, SmartMerge: true, OrderCapacity: 4}
}

func TestPunctualBestFitDecreasingNeverSplitsOriginalGroups(t *testing.T) {
	now := time.Now()
	groups := []domain.PurchaseGroup{{ID: "three", MacroTaskID: "m", Buyers: buyers("a", "b", "c"), CreatedAt: now}, {ID: "one", MacroTaskID: "m", Buyers: buyers("d"), CreatedAt: now.Add(time.Second)}, {ID: "two", MacroTaskID: "m", Buyers: buyers("e", "f"), CreatedAt: now.Add(2 * time.Second)}}
	intents, err := Plan(macro(), groups, domain.PhasePunctual, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(intents) != 2 || len(intents[0].Buyers) != 4 || len(intents[1].Buyers) != 2 {
		t.Fatalf("unexpected bins: %#v", intents)
	}
	if intents[0].Buyers[0].LogicalID != "a" || intents[0].Buyers[3].LogicalID != "d" {
		t.Fatal("3+1 group merge was not deterministic")
	}
}

func TestReflowNeverMergesAndOnlySplitsOptedInGroups(t *testing.T) {
	now := time.Now()
	groups := []domain.PurchaseGroup{{ID: "fixed", MacroTaskID: "m", Buyers: buyers("a", "b")}, {ID: "split", MacroTaskID: "m", Buyers: buyers("c", "d"), AllowSplit: true, CreatedAt: now.Add(time.Second)}}
	intents, err := Plan(macro(), groups, domain.PhaseReflow, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(intents) != 3 || len(intents[0].Buyers) != 2 || len(intents[1].Buyers) != 1 || len(intents[2].Buyers) != 1 {
		t.Fatalf("unexpected reflow shapes: %#v", intents)
	}
}

func TestUnreviewedMacroCannotPlan(t *testing.T) {
	m := macro()
	m.NeedsReview = true
	if _, err := Plan(m, nil, domain.PhasePunctual, time.Now()); err == nil {
		t.Fatal("expected review gate")
	}
}
