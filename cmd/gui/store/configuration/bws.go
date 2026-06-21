package configuration

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// BWSEntry represents a single BWS activity reservation to be attempted.
type BWSEntry struct {
	ActivityID    int    `json:"activityId"`
	TicketNo      string `json:"ticketNo"`
	ActivityTitle string `json:"activityTitle"`
	ReserveTime   int64  `json:"reserveTime"`  // unix timestamp of reserve_begin_time
	ReserveDate   string `json:"reserveDate"`  // date key (e.g. "20250711")
	Expire        int64  `json:"expire"`       // unix timestamp after which this entry is stale
	StartDelayMs  int    `json:"startDelayMs"` // per-task delay (can be overridden by global setting)
	LoopDelayMs   int    `json:"loopDelayMs"`  // per-task loop delay (can be overridden by global setting)

	// Stat persists the task execution result.
	// 0=Waiting, 1=Pending, 2=Success, 3=Failed, 4=Error.
	Stat int `json:"stat"`
}

// Hash returns a SHA256-based unique identifier for this BWS entry.
func (e BWSEntry) Hash() string {
	str := fmt.Sprintf("BWS:%d:%s:%d", e.ActivityID, e.ReserveDate, e.ReserveTime)
	hash := sha256.Sum256([]byte(str))
	return hex.EncodeToString(hash[:])
}

// Valid checks whether the entry is still valid (not expired and has required fields).
func (e BWSEntry) Valid() bool {
	return e.Expire > time.Now().Unix() &&
		e.ActivityID > 0 &&
		e.TicketNo != "" &&
		e.ReserveTime > 0
}

// String returns a human-readable representation.
func (e BWSEntry) String() string {
	return fmt.Sprintf("%s (ID:%d) — 开抢时间: %s (票号: %s)",
		e.ActivityTitle, e.ActivityID,
		time.Unix(e.ReserveTime, 0).Format("2006-01-02 15:04:05"),
		e.TicketNo,
	)
}

// BWSScheduler wraps a thread-safe BWS entry list with a change callback.
type BWSScheduler struct {
	Entries           []BWSEntry `json:"entries"`
	mutex             sync.Mutex
	bwsChangeCallback *func(entry BWSEntry)
}

// NewBWSScheduler creates a new BWSScheduler instance.
func NewBWSScheduler() *BWSScheduler {
	return &BWSScheduler{
		Entries: make([]BWSEntry, 0),
	}
}

// SetChangeCallback sets a callback invoked when entries are added or removed.
func (bd *BWSScheduler) SetChangeCallback(cb func(entry BWSEntry)) {
	bd.mutex.Lock()
	defer bd.mutex.Unlock()
	bd.bwsChangeCallback = &cb
}

// AddEntry adds a BWS entry (deduplicated by activityID + date).
// Returns true if the entry was actually added.
func (bd *BWSScheduler) AddEntry(entry BWSEntry) bool {
	bd.mutex.Lock()

	now := time.Now()

	// Filter out expired entries and check duplicates
	n := 0
	for _, e := range bd.Entries {
		if time.Unix(e.Expire, 0).Before(now) {
			continue // drop expired
		}
		if e.ActivityID == entry.ActivityID && e.ReserveDate == entry.ReserveDate {
			bd.mutex.Unlock()
			return false // active duplicate
		}
		bd.Entries[n] = e
		n++
	}
	bd.Entries = bd.Entries[:n]

	bd.Entries = append(bd.Entries, entry)
	cb := bd.bwsChangeCallback
	bd.mutex.Unlock()
	if cb != nil {
		go (*cb)(entry)
	}
	return true
}

// GetEntries returns all non-expired entries.
func (bd *BWSScheduler) GetEntries() []BWSEntry {
	bd.mutex.Lock()
	defer bd.mutex.Unlock()

	now := time.Now()
	valid := make([]BWSEntry, 0, len(bd.Entries))
	for _, e := range bd.Entries {
		if time.Unix(e.Expire, 0).After(now) {
			valid = append(valid, e)
		}
	}
	bd.Entries = valid
	return valid
}

// GetEntriesNoMutate returns all non-expired entries without mutating the internal slice.
func (bd *BWSScheduler) GetEntriesNoMutate() []BWSEntry {
	bd.mutex.Lock()
	defer bd.mutex.Unlock()

	now := time.Now()
	valid := make([]BWSEntry, 0, len(bd.Entries))
	for _, e := range bd.Entries {
		if time.Unix(e.Expire, 0).After(now) {
			valid = append(valid, e)
		}
	}
	return valid
}

// RemoveEntryByHash removes an entry by its SHA256 hash.
func (bd *BWSScheduler) RemoveEntryByHash(hash string) bool {
	bd.mutex.Lock()

	now := time.Now()
	n := 0
	found := false
	var removed BWSEntry
	for _, e := range bd.Entries {
		if time.Unix(e.Expire, 0).Before(now) {
			continue
		}
		if !found && e.Hash() == hash {
			found = true
			removed = e
			continue
		}
		bd.Entries[n] = e
		n++
	}
	bd.Entries = bd.Entries[:n]

	cb := bd.bwsChangeCallback
	bd.mutex.Unlock()
	if found && cb != nil {
		go (*cb)(removed)
	}
	return found
}

// UpdateEntryStat updates the persisted stat of an entry by hash.
func (bd *BWSScheduler) UpdateEntryStat(hash string, stat int) bool {
	bd.mutex.Lock()
	defer bd.mutex.Unlock()

	for i := range bd.Entries {
		if bd.Entries[i].Hash() == hash {
			bd.Entries[i].Stat = stat
			return true
		}
	}
	return false
}
