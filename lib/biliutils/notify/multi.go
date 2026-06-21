package notify

import (
	"sync"
)

// MultiNotifier aggregates multiple Notifier instances and broadcasts
// notification messages to all of them.
type MultiNotifier struct {
	mu        sync.RWMutex
	notifiers []Notifier
}

// NewMultiNotifier creates a new MultiNotifier with the given notifiers.
func NewMultiNotifier(notifiers ...Notifier) *MultiNotifier {
	if notifiers == nil {
		notifiers = make([]Notifier, 0)
	}
	return &MultiNotifier{
		notifiers: notifiers,
	}
}

// Add adds a notifier to the broadcast list.
func (m *MultiNotifier) Add(n Notifier) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.notifiers = append(m.notifiers, n)
}

// Remove removes a notifier at the given index. Returns false if index is out of bounds.
func (m *MultiNotifier) Remove(index int) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if index < 0 || index >= len(m.notifiers) {
		return false
	}
	m.notifiers = append(m.notifiers[:index], m.notifiers[index+1:]...)
	return true
}

// Clear removes all notifiers.
func (m *MultiNotifier) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.notifiers = make([]Notifier, 0)
}

// Count returns the number of registered notifiers.
func (m *MultiNotifier) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.notifiers)
}

// Notify sends the message to all registered notifiers concurrently.
// Returns true if at least one notifier reported success.
func (m *MultiNotifier) Notify(message string) bool {
	m.mu.RLock()
	notifiers := make([]Notifier, len(m.notifiers))
	copy(notifiers, m.notifiers)
	m.mu.RUnlock()

	if len(notifiers) == 0 {
		return false
	}

	var wg sync.WaitGroup
	results := make([]bool, len(notifiers))

	for i, n := range notifiers {
		wg.Add(1)
		go func(idx int, notifier Notifier) {
			defer wg.Done()
			results[idx], _ = notifier.Notify(message)
		}(i, n)
	}

	wg.Wait()

	success := false
	for _, ok := range results {
		if ok {
			success = true
		}
	}
	return success
}

// Test sends a test message to all registered notifiers.
func (m *MultiNotifier) Test() bool {
	return m.Notify("This is a test message from Bili-Ticket-Go.")
}
