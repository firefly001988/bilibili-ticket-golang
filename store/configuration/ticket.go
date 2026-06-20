package configuration

import (
	_return "bilibili-ticket-golang/models/bili/response"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// TicketEntry represents a ticket to be purchased.
type TicketEntry struct {
	Expire      int64                 `json:"expire"`
	Start       int64                 `json:"start"`
	ProjectID   int64                 `json:"projectId"`
	ProjectName string                `json:"projectName"`
	SkuID       int64                 `json:"skuId"`
	SkuName     string                `json:"skuName"`
	ScreenID    int64                 `json:"screenId"`
	ScreenName  string                `json:"screenName"`
	Buyers      []_return.TicketBuyer `json:"buyers"`

	// Stat persists the task execution result.
	// 0=Waiting, 1=Pending, 2=Success, 3=Failed, 4=Error.
	// On app restart, completed tasks (Success/Failed/Error) are NOT re-scheduled.
	Stat int `json:"stat"`

	// SortOrder is the chain order within the same buyer group.
	// Tickets of the same buyer are chained by ascending SortOrder: when the
	// task at order N terminates (per ChainTrigger), the scheduler starts the
	// next valid ticket with order > N. New tickets are appended with
	// max(group)+1. Zero/missing values are treated as the smallest order.
	SortOrder int `json:"sortOrder"`
}

// Count returns the number of buyers (tickets to purchase in one order).
func (t TicketEntry) Count() int {
	return len(t.Buyers)
}

// FirstBuyer returns the first buyer, or an empty TicketBuyer if none.
func (t TicketEntry) FirstBuyer() _return.TicketBuyer {
	if len(t.Buyers) == 0 {
		return _return.TicketBuyer{}
	}
	return t.Buyers[0]
}

func (t TicketEntry) String() string {
	buyerStr := ""
	for i, b := range t.Buyers {
		if i > 0 {
			buyerStr += ", "
		}
		buyerStr += b.String()
	}
	return fmt.Sprintf("%s - %s - %s - [%s] (Expire: %s; Start: %s)",
		t.ProjectName, t.ScreenName, t.SkuName, buyerStr,
		time.Unix(t.Expire, 0).Format("2006-01-02 15:04:05"),
		time.Unix(t.Start, 0).Format("2006-01-02 15:04:05"),
	)
}

// ChainGroupKey returns the chain-group key used for ticket chaining.
//
// Chaining is restricted to tickets with exactly one real-name buyer within
// the same project. Multi-buyer tickets are not chainable (they purchase all
// at once). Ordinary (non-real-name) tickets return "" and are never chained.
func (t TicketEntry) ChainGroupKey() string {
	if len(t.Buyers) != 1 {
		return ""
	}
	if t.Buyers[0].BuyerType != _return.ForceRealName {
		return ""
	}
	return fmt.Sprintf("r:%d:p:%d", t.Buyers[0].ID, t.ProjectID)
}

// Hash returns a SHA256-based unique identifier for this ticket.
func (t TicketEntry) Hash() string {
	buyerPart := ""
	for i, b := range t.Buyers {
		if i > 0 {
			buyerPart += "|"
		}
		buyerPart += fmt.Sprintf("Buyer:BuyerType:%d,ID:%d,Name:%s,Tel:%s", b.BuyerType, b.ID, b.Name, b.Tel)
	}
	str := fmt.Sprintf(
		"%s|Expire:%d|Start:%d|ProjectID:%d|ScreenID:%d|SkuID:%d",
		buyerPart, t.Expire, t.Start, t.ProjectID, t.ScreenID, t.SkuID,
	)
	hash := sha256.Sum256([]byte(str))
	return hex.EncodeToString(hash[:])
}

// Valid checks whether the ticket is still valid (not expired) and has at
// least one valid buyer.
func (t TicketEntry) Valid() bool {
	if t.Expire <= time.Now().Unix() || t.ProjectID <= 0 || t.SkuID <= 0 || t.ScreenID <= 0 {
		return false
	}
	if len(t.Buyers) == 0 {
		return false
	}
	for _, b := range t.Buyers {
		if !b.Valid() {
			return false
		}
	}
	return true
}

// TicketData wraps a thread-safe ticket list with a change callback.
type TicketData struct {
	Tickets              []TicketEntry `json:"tickets"`
	mutex                sync.Mutex
	ticketChangeCallback *func(ticket TicketEntry)
}

// NewTicketData creates a new TicketData instance.
func NewTicketData() *TicketData {
	return &TicketData{
		Tickets: make([]TicketEntry, 0),
	}
}

// SetChangeCallback sets a callback invoked when tickets are added or removed.
func (td *TicketData) SetChangeCallback(cb func(ticket TicketEntry)) {
	td.mutex.Lock()
	defer td.mutex.Unlock()
	td.ticketChangeCallback = &cb
}

// AddTicket adds a ticket (deduplicated by all buyers + project + sku + screen).
// Returns true if the ticket was actually added, false if a duplicate exists.
// Expired duplicates are silently replaced.
// SortOrder is auto-assigned as max(existing same-group order)+1 when the
// incoming ticket has a non-positive SortOrder.
func (td *TicketData) AddTicket(data TicketEntry) bool {
	td.mutex.Lock()

	now := time.Now()

	// Replace expired duplicates and filter out expired items at the same time
	n := 0
	// hasSameBuyers checks whether two tickets have the exact same set of buyers
	// (same count, same buyer keys in same order).
	hasSameBuyers := func(a, b []_return.TicketBuyer) bool {
		if len(a) != len(b) {
			return false
		}
		for i := range a {
			if !a[i].Compare(b[i]) {
				return false
			}
		}
		return true
	}

	for _, t := range td.Tickets {
		if time.Unix(t.Expire, 0).Before(now) {
			// drop expired ticket
			continue
		}
		if hasSameBuyers(t.Buyers, data.Buyers) && t.ProjectID == data.ProjectID && t.SkuID == data.SkuID && t.ScreenID == data.ScreenID {
			td.mutex.Unlock()
			return false // active duplicate found
		}
		td.Tickets[n] = t
		n++
	}
	td.Tickets = td.Tickets[:n]

	// Auto-assign chain order within the buyer+project group (single real-name only).
	if data.SortOrder <= 0 {
		groupKey := data.ChainGroupKey()
		maxOrder := 0
		if groupKey != "" {
			for _, t := range td.Tickets {
				if t.ChainGroupKey() == groupKey && t.SortOrder > maxOrder {
					maxOrder = t.SortOrder
				}
			}
		}
		data.SortOrder = maxOrder + 1
	}

	td.Tickets = append(td.Tickets, data)
	cb := td.ticketChangeCallback
	td.mutex.Unlock()
	if cb != nil {
		go (*cb)(data)
	}
	return true
}

// GetTickets returns all valid (non-expired) tickets.
// NOTE: callers that mutate td.Tickets must hold td.mutex.
func (td *TicketData) GetTickets() []TicketEntry {
	td.mutex.Lock()
	defer td.mutex.Unlock()

	now := time.Now()
	validTickets := make([]TicketEntry, 0, len(td.Tickets))
	for _, t := range td.Tickets {
		if time.Unix(t.Expire, 0).After(now) {
			validTickets = append(validTickets, t)
		}
	}
	td.Tickets = validTickets
	return validTickets
}

// GetTicketsNoMutate returns all valid (non-expired) tickets without mutating
// the internal slice. Safe for concurrent reads.
func (td *TicketData) GetTicketsNoMutate() []TicketEntry {
	td.mutex.Lock()
	defer td.mutex.Unlock()

	now := time.Now()
	validTickets := make([]TicketEntry, 0, len(td.Tickets))
	for _, t := range td.Tickets {
		if time.Unix(t.Expire, 0).After(now) {
			validTickets = append(validTickets, t)
		}
	}
	return validTickets
}

// RemoveTicketByHash removes a ticket by its SHA256 hash.
func (td *TicketData) RemoveTicketByHash(hash string) bool {
	td.mutex.Lock()

	now := time.Now()
	n := 0
	found := false
	var removed TicketEntry
	for i, t := range td.Tickets {
		// prune expired as we go
		if time.Unix(t.Expire, 0).Before(now) {
			continue
		}
		if !found && t.Hash() == hash {
			found = true
			removed = t
			continue
		}
		td.Tickets[n] = td.Tickets[i]
		n++
	}
	td.Tickets = td.Tickets[:n]

	cb := td.ticketChangeCallback
	td.mutex.Unlock()
	if found && cb != nil {
		go (*cb)(removed)
	}
	return found
}

// UpdateTicketStat updates the persisted stat of a ticket by hash.
// Returns true if the ticket was found and updated.
func (td *TicketData) UpdateTicketStat(hash string, stat int) bool {
	td.mutex.Lock()
	defer td.mutex.Unlock()

	for i := range td.Tickets {
		if td.Tickets[i].Hash() == hash {
			td.Tickets[i].Stat = stat
			return true
		}
	}
	return false
}

// RemoveTicketByIndex removes a ticket at the given index.
func (td *TicketData) RemoveTicketByIndex(index int64) bool {
	td.mutex.Lock()
	if index < 0 || index >= int64(len(td.Tickets)) {
		td.mutex.Unlock()
		return false
	}
	old := td.Tickets[index]
	td.Tickets = append(td.Tickets[:index], td.Tickets[index+1:]...)
	cb := td.ticketChangeCallback
	td.mutex.Unlock()
	if cb != nil {
		go (*cb)(old)
	}
	return true
}

// ReorderInGroup rewrites SortOrder for the tickets identified by
// orderedHashes so that they form an ascending chain (1,2,3,...) within the
// buyer group of the first hash. Hashes not belonging to the same group are
// ignored. Returns an error if orderedHashes is empty or the first hash is
// not found.
//
// The caller is responsible for persisting the change (e.g. store.Save()).
func (td *TicketData) ReorderInGroup(orderedHashes []string) error {
	if len(orderedHashes) == 0 {
		return fmt.Errorf("orderedHashes is empty")
	}

	td.mutex.Lock()
	defer td.mutex.Unlock()

	// Locate the chain-group key from the first hash.
	var groupKey string
	foundFirst := false
	for _, t := range td.Tickets {
		if t.Hash() == orderedHashes[0] {
			groupKey = t.ChainGroupKey()
			foundFirst = true
			break
		}
	}
	if !foundFirst {
		return fmt.Errorf("ticket not found: %s", orderedHashes[0])
	}
	if groupKey == "" {
		return fmt.Errorf("ticket is not real-name, chaining is not applicable")
	}

	// Build hash → new order mapping (1-based).
	orderMap := make(map[string]int, len(orderedHashes))
	for i, h := range orderedHashes {
		orderMap[h] = i + 1
	}

	// Apply new order to same-chain-group tickets; reset others in the group
	// that are not in the mapping to a high value so they sort after the chain.
	for i := range td.Tickets {
		if td.Tickets[i].ChainGroupKey() != groupKey {
			continue
		}
		if newOrder, ok := orderMap[td.Tickets[i].Hash()]; ok {
			td.Tickets[i].SortOrder = newOrder
		}
		// Tickets in the group but not in orderedHashes keep their existing
		// SortOrder (they are simply not part of the reordered subset).
	}
	return nil
}

// GetNextInChain returns the hash of the next ticket to start after the ticket
// identified by `hash` terminates, according to the chain group.
//
// Rules:
//   - Chaining applies only to real-name buyers within the same project
//     (ChainGroupKey). Ordinary tickets are never chained.
//   - The next ticket must share the same ChainGroupKey as the current ticket.
//   - Tickets with Stat == StatSuccess or Stat == StatError are skipped
//     (already done or unrecoverable). Failed tickets are retried.
//   - The next ticket must be Valid() (not expired).
//   - Search starts from SortOrder > current, wrapping around to the
//     beginning if needed (circular). The search terminates after one full
//     cycle through the group without finding an eligible ticket.
//   - Among candidates, the one with the smallest SortOrder wins.
//
// Returns ("", false) when there is no eligible successor.
func (td *TicketData) GetNextInChain(hash string) (string, bool) {
	td.mutex.Lock()
	defer td.mutex.Unlock()

	var current *TicketEntry
	for i := range td.Tickets {
		if td.Tickets[i].Hash() == hash {
			current = &td.Tickets[i]
			break
		}
	}
	if current == nil {
		return "", false
	}

	groupKey := current.ChainGroupKey()
	if groupKey == "" {
		// Ordinary (non-real-name) tickets are never chained.
		return "", false
	}

	// Collect all same-group tickets sorted by SortOrder ascending.
	var groupTickets []*TicketEntry
	for i := range td.Tickets {
		if td.Tickets[i].ChainGroupKey() == groupKey {
			groupTickets = append(groupTickets, &td.Tickets[i])
		}
	}
	if len(groupTickets) == 0 {
		return "", false
	}
	// Sort by SortOrder (simple insertion sort — groups are small).
	for i := 1; i < len(groupTickets); i++ {
		for j := i; j > 0 && groupTickets[j].SortOrder < groupTickets[j-1].SortOrder; j-- {
			groupTickets[j], groupTickets[j-1] = groupTickets[j-1], groupTickets[j]
		}
	}

	// Find the index of the current ticket in the sorted slice.
	curIdx := -1
	for i, t := range groupTickets {
		if t.Hash() == hash {
			curIdx = i
			break
		}
	}
	if curIdx == -1 {
		return "", false
	}

	// isEligible checks whether a ticket can be (re)started: not successful,
	// not error, and not expired. The current ticket itself is never eligible
	// (we're looking for the NEXT one, not itself).
	isEligible := func(t *TicketEntry) bool {
		return t.Hash() != hash && // never return the current ticket itself
			t.Stat != int(2) /* StatSuccess */ && t.Stat != int(4) /* StatError */ && t.Valid()
	}

	// Search forward from curIdx+1, wrapping around circularly.
	// Stop after checking every ticket in the group exactly once.
	n := len(groupTickets)
	for offset := 1; offset <= n; offset++ {
		idx := (curIdx + offset) % n
		t := groupTickets[idx]
		if isEligible(t) {
			return t.Hash(), true
		}
	}

	return "", false
}
