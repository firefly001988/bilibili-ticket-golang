package domain

import (
	"testing"
	"time"
)

func TestIntentNormalizesShapeAndConflictsByBuyerDay(t *testing.T) {
	m := MacroTask{ID: "m", EventDay: "2026-07-10", EventDayConfirmed: true, OrderCapacity: 4}
	a, err := NewIntent("a", m, PhasePunctual, []Buyer{{LogicalID: "b"}, {LogicalID: "a"}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	b, _ := NewIntent("b", m, PhasePunctual, []Buyer{{LogicalID: "a"}}, time.Now())
	if a.Buyers[0].LogicalID != "a" || !Conflicts(a, b) {
		t.Fatalf("intent not normalized or conflict missed: %#v", a)
	}
	c, _ := NewIntent("c", MacroTask{ID: "other", EventDay: "2026-07-11", EventDayConfirmed: true}, PhasePunctual, []Buyer{{LogicalID: "a"}}, time.Now())
	if Conflicts(a, c) {
		t.Fatal("different event days must not conflict")
	}
}

func TestIntentRejectsUnconfirmedDayAndCapacityOverflow(t *testing.T) {
	_, err := NewIntent("x", MacroTask{ID: "m"}, PhasePunctual, []Buyer{{LogicalID: "a"}}, time.Now())
	if err == nil {
		t.Fatal("expected unconfirmed event day error")
	}
	_, err = NewIntent("x", MacroTask{ID: "m", EventDay: "2026-01-01", EventDayConfirmed: true, OrderCapacity: 1}, PhasePunctual, []Buyer{{LogicalID: "a"}, {LogicalID: "b"}}, time.Now())
	if err == nil {
		t.Fatal("expected capacity error")
	}
}

func TestSpecHashExcludesCredentials(t *testing.T) {
	s := ExecutionSpec{AttemptID: "a", IntentID: "i", ProjectID: 1, ScreenID: 2, SKUID: 3, Buyers: []Buyer{{LogicalID: "b"}}, StartMode: StartImmediate, Deadline: time.Now()}
	s.Credentials.Cookies = map[string]string{"SESSDATA": "one"}
	h1 := s.Hash()
	s.Credentials.Cookies["SESSDATA"] = "two"
	if h1 != s.Hash() {
		t.Fatal("credential refresh must not change immutable spec hash")
	}
}
