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
	Expire      int64               `json:"expire"`
	Start       int64               `json:"start"`
	ProjectID   int64               `json:"projectId"`
	ProjectName string              `json:"projectName"`
	SkuID       int64               `json:"skuId"`
	SkuName     string              `json:"skuName"`
	ScreenID    int64               `json:"screenId"`
	ScreenName  string              `json:"screenName"`
	Buyer       _return.TicketBuyer `json:"buyer"`

	// Stat persists the task execution result.
	// 0=Waiting, 1=Pending, 2=Success, 3=Failed, 4=Error.
	// On app restart, completed tasks (Success/Failed/Error) are NOT re-scheduled.
	Stat int `json:"stat"`
}

func (t TicketEntry) String() string {
	return fmt.Sprintf("%s - %s - %s - %s (Expire: %s; Start: %s)",
		t.ProjectName, t.ScreenName, t.SkuName, t.Buyer.String(),
		time.Unix(t.Expire, 0).Format("2006-01-02 15:04:05"),
		time.Unix(t.Start, 0).Format("2006-01-02 15:04:05"),
	)
}

// Hash returns a SHA256-based unique identifier for this ticket.
func (t TicketEntry) Hash() string {
	str := fmt.Sprintf(
		"Buyer:BuyerType:%d,ID:%d,Name:%s,Tel:%s|Expire:%d|Start:%d|ProjectID:%d|ScreenID:%d|SkuID:%d",
		t.Buyer.BuyerType, t.Buyer.ID, t.Buyer.Name, t.Buyer.Tel, t.Expire, t.Start, t.ProjectID, t.ScreenID, t.SkuID,
	)
	hash := sha256.Sum256([]byte(str))
	return hex.EncodeToString(hash[:])
}

// Valid checks whether the ticket is still valid (not expired).
func (t TicketEntry) Valid() bool {
	return t.Expire > time.Now().Unix() && t.ProjectID > 0 && t.SkuID > 0 && t.ScreenID > 0 && t.Buyer.Valid()
}

// TicketData wraps a thread-safe ticket list with a change callback.
type TicketData struct {
	Tickets              []TicketEntry `json:"tickets"`
	mutex                sync.Mutex
	ticketChangeCallback *func(data *TicketData, ticket TicketEntry)
}

// NewTicketData creates a new TicketData instance.
func NewTicketData() *TicketData {
	return &TicketData{
		Tickets: make([]TicketEntry, 0),
	}
}

// SetChangeCallback sets a callback invoked when tickets are added or removed.
func (td *TicketData) SetChangeCallback(cb func(data *TicketData, ticket TicketEntry)) {
	td.mutex.Lock()
	defer td.mutex.Unlock()
	td.ticketChangeCallback = &cb
}

// AddTicket adds a ticket (deduplicated by buyer + project + sku + screen).
// Returns true if the ticket was actually added, false if a duplicate exists.
// Expired duplicates are silently replaced.
func (td *TicketData) AddTicket(data TicketEntry) bool {
	td.mutex.Lock()
	defer td.mutex.Unlock()

	now := time.Now()

	// Replace expired duplicates and filter out expired items at the same time
	n := 0
	for _, t := range td.Tickets {
		if time.Unix(t.Expire, 0).Before(now) {
			// drop expired ticket
			continue
		}
		if t.Buyer.Compare(data.Buyer) && t.ProjectID == data.ProjectID && t.SkuID == data.SkuID && t.ScreenID == data.ScreenID {
			return false // active duplicate found
		}
		td.Tickets[n] = t
		n++
	}
	td.Tickets = td.Tickets[:n]

	td.Tickets = append(td.Tickets, data)
	if td.ticketChangeCallback != nil {
		go (*td.ticketChangeCallback)(td, data)
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
	defer td.mutex.Unlock()

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

	if found && td.ticketChangeCallback != nil {
		go (*td.ticketChangeCallback)(td, removed)
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
	defer td.mutex.Unlock()
	if index < 0 || index >= int64(len(td.Tickets)) {
		return false
	}
	old := td.Tickets[index]
	td.Tickets = append(td.Tickets[:index], td.Tickets[index+1:]...)
	if td.ticketChangeCallback != nil {
		go (*td.ticketChangeCallback)(td, old)
	}
	return true
}
