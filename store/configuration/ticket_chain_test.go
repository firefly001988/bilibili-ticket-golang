package configuration

import (
	_return "bilibili-ticket-golang/models/bili/response"
	"testing"
	"time"
)

// makeTicket creates a TicketEntry for testing with sensible defaults.
// expire is set far in the future so Valid() is always true.
func makeTicket(buyerID int64, projectID int64, skuID int64, screenID int64, sortOrder int, stat int) TicketEntry {
	return TicketEntry{
		Expire:      time.Now().Add(24 * time.Hour).Unix(),
		Start:       time.Now().Unix(),
		ProjectID:   projectID,
		ProjectName: "test-project",
		SkuID:       skuID,
		SkuName:     "test-sku",
		ScreenID:    screenID,
		ScreenName:  "test-screen",
		SortOrder:   sortOrder,
		Stat:        stat,
		Buyer: _return.TicketBuyer{
			BuyerType: _return.ForceRealName,
			ID:        buyerID,
			Name:      "test-buyer",
		},
	}
}

// TestGetNextInChain_BasicForward verifies that the next ticket in chain
// order is returned when the current one terminates.
func TestGetNextInChain_BasicForward(t *testing.T) {
	td := NewTicketData()
	a := makeTicket(100, 1, 10, 20, 1, 0)
	b := makeTicket(100, 1, 11, 21, 2, 0)
	td.AddTicket(a)
	td.AddTicket(b)

	next, ok := td.GetNextInChain(a.Hash())
	if !ok {
		t.Fatal("expected next ticket, got none")
	}
	if next != b.Hash() {
		t.Errorf("expected ticket B, got %s", next)
	}
}

// TestGetNextInChain_SkipSuccessAndError verifies that Success and Error
// tickets are skipped, and the search continues to the next eligible one.
func TestGetNextInChain_SkipSuccessAndError(t *testing.T) {
	td := NewTicketData()
	a := makeTicket(100, 1, 10, 20, 1, 0) // Waiting (current)
	b := makeTicket(100, 1, 11, 21, 2, 2) // Success — skip
	c := makeTicket(100, 1, 12, 22, 3, 4) // Error — skip
	d := makeTicket(100, 1, 13, 23, 4, 0) // Waiting — eligible
	td.AddTicket(a)
	td.AddTicket(b)
	td.AddTicket(c)
	td.AddTicket(d)

	next, ok := td.GetNextInChain(a.Hash())
	if !ok {
		t.Fatal("expected next ticket D, got none")
	}
	if next != d.Hash() {
		t.Errorf("expected ticket D (skipping B=Success, C=Error), got %s", next)
	}
}

// TestGetNextInChain_FailedIsRetriable verifies that Failed tickets are
// NOT skipped — they are eligible for retry.
func TestGetNextInChain_FailedIsRetriable(t *testing.T) {
	td := NewTicketData()
	a := makeTicket(100, 1, 10, 20, 1, 0) // Waiting (current)
	b := makeTicket(100, 1, 11, 21, 2, 3) // Failed — should be eligible
	td.AddTicket(a)
	td.AddTicket(b)

	next, ok := td.GetNextInChain(a.Hash())
	if !ok {
		t.Fatal("expected next ticket B (Failed is retriable), got none")
	}
	if next != b.Hash() {
		t.Errorf("expected ticket B (Failed), got %s", next)
	}
}

// TestGetNextInChain_WrapAround verifies that when no eligible ticket exists
// after the current one, the search wraps around to the beginning.
func TestGetNextInChain_WrapAround(t *testing.T) {
	td := NewTicketData()
	a := makeTicket(100, 1, 10, 20, 1, 0) // Waiting (eligible on wrap)
	b := makeTicket(100, 1, 11, 21, 2, 2) // Success — skip
	c := makeTicket(100, 1, 12, 22, 3, 0) // Waiting (current)
	td.AddTicket(a)
	td.AddTicket(b)
	td.AddTicket(c)

	// C is current; after C there's nothing, so wrap to A.
	next, ok := td.GetNextInChain(c.Hash())
	if !ok {
		t.Fatal("expected wrap-around to ticket A, got none")
	}
	if next != a.Hash() {
		t.Errorf("expected ticket A (wrap-around), got %s", next)
	}
}

// TestGetNextInChain_WrapAroundSkipSuccess verifies that wrap-around
// correctly skips Success/Error tickets and finds the next eligible one.
func TestGetNextInChain_WrapAroundSkipSuccess(t *testing.T) {
	td := NewTicketData()
	a := makeTicket(100, 1, 10, 20, 1, 2) // Success — skip
	b := makeTicket(100, 1, 11, 21, 2, 0) // Waiting — eligible
	c := makeTicket(100, 1, 12, 22, 3, 0) // Waiting (current)
	td.AddTicket(a)
	td.AddTicket(b)
	td.AddTicket(c)

	// C is current; wrap to A (skip, Success), then B (eligible).
	next, ok := td.GetNextInChain(c.Hash())
	if !ok {
		t.Fatal("expected wrap-around to ticket B, got none")
	}
	if next != b.Hash() {
		t.Errorf("expected ticket B (wrap-around skipping A=Success), got %s", next)
	}
}

// TestGetNextInChain_AllDone verifies that when all tickets in the group are
// either Success or Error, the chain terminates (returns false).
func TestGetNextInChain_AllDone(t *testing.T) {
	td := NewTicketData()
	a := makeTicket(100, 1, 10, 20, 1, 2) // Success
	b := makeTicket(100, 1, 11, 21, 2, 4) // Error
	c := makeTicket(100, 1, 12, 22, 3, 2) // Success (current)
	td.AddTicket(a)
	td.AddTicket(b)
	td.AddTicket(c)

	_, ok := td.GetNextInChain(c.Hash())
	if ok {
		t.Fatal("expected chain to terminate (all Success/Error), but got a next ticket")
	}
}

// TestGetNextInChain_SingleTicket verifies that a single-ticket chain
// returns false (no successor).
func TestGetNextInChain_SingleTicket(t *testing.T) {
	td := NewTicketData()
	a := makeTicket(100, 1, 10, 20, 1, 0)
	td.AddTicket(a)

	_, ok := td.GetNextInChain(a.Hash())
	if ok {
		t.Fatal("expected no next ticket for single-ticket chain, but got one")
	}
}

// TestGetNextInChain_ExpiredSkipped verifies that expired tickets are
// skipped even if they are Waiting.
func TestGetNextInChain_ExpiredSkipped(t *testing.T) {
	td := NewTicketData()
	a := makeTicket(100, 1, 10, 20, 1, 0)            // Waiting (current)
	b := makeTicket(100, 1, 11, 21, 2, 0)            // Waiting but expired
	b.Expire = time.Now().Add(-1 * time.Hour).Unix() // expired
	c := makeTicket(100, 1, 12, 22, 3, 0)            // Waiting — eligible
	td.AddTicket(a)
	td.AddTicket(b)
	td.AddTicket(c)

	next, ok := td.GetNextInChain(a.Hash())
	if !ok {
		t.Fatal("expected next ticket C (B is expired), got none")
	}
	if next != c.Hash() {
		t.Errorf("expected ticket C (skipping expired B), got %s", next)
	}
}

// TestGetNextInChain_OrdinaryNotChained verifies that ordinary (non-real-name)
// tickets are never chained.
func TestGetNextInChain_OrdinaryNotChained(t *testing.T) {
	td := NewTicketData()
	a := TicketEntry{
		Expire:    time.Now().Add(24 * time.Hour).Unix(),
		Start:     time.Now().Unix(),
		ProjectID: 1,
		SkuID:     10,
		ScreenID:  20,
		SortOrder: 1,
		Stat:      0,
		Buyer: _return.TicketBuyer{
			BuyerType: _return.Ordinary,
			Name:      "ordinary-buyer",
			Tel:       "1234567890",
		},
	}
	td.AddTicket(a)

	_, ok := td.GetNextInChain(a.Hash())
	if ok {
		t.Fatal("expected no chain for ordinary ticket, but got a next ticket")
	}
}

// TestGetNextInChain_DifferentProjectsNotChained verifies that tickets
// with the same buyer but different project IDs are not chained together.
func TestGetNextInChain_DifferentProjectsNotChained(t *testing.T) {
	td := NewTicketData()
	a := makeTicket(100, 1, 10, 20, 1, 0) // project 1
	b := makeTicket(100, 2, 11, 21, 1, 0) // project 2, same buyer
	td.AddTicket(a)
	td.AddTicket(b)

	_, ok := td.GetNextInChain(a.Hash())
	if ok {
		t.Fatal("expected no chain across different projects, but got a next ticket")
	}
}

// TestGetNextInChain_FullCycle verifies the complete circular traversal:
// starting from the last ticket, wrapping through all, and terminating
// when no eligible ticket is found after a full cycle.
func TestGetNextInChain_FullCycle(t *testing.T) {
	td := NewTicketData()
	a := makeTicket(100, 1, 10, 20, 1, 3) // Failed — eligible
	b := makeTicket(100, 1, 11, 21, 2, 3) // Failed — eligible
	c := makeTicket(100, 1, 12, 22, 3, 0) // Waiting (current)
	td.AddTicket(a)
	td.AddTicket(b)
	td.AddTicket(c)

	// C is current; wrap to A (Failed, eligible).
	next, ok := td.GetNextInChain(c.Hash())
	if !ok {
		t.Fatal("expected wrap-around to A, got none")
	}
	if next != a.Hash() {
		t.Errorf("expected ticket A, got %s", next)
	}

	// Now from A: B is next (Failed, eligible).
	next, ok = td.GetNextInChain(a.Hash())
	if !ok {
		t.Fatal("expected next ticket B from A, got none")
	}
	if next != b.Hash() {
		t.Errorf("expected ticket B, got %s", next)
	}

	// Now from B: C is next (Waiting, eligible).
	next, ok = td.GetNextInChain(b.Hash())
	if !ok {
		t.Fatal("expected next ticket C from B, got none")
	}
	if next != c.Hash() {
		t.Errorf("expected ticket C, got %s", next)
	}
}
