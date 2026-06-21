package configuration

import (
	"bilibili-ticket-golang/lib/biliutils/notify"
	"fmt"
	"sync"
)

// NotifyChannel represents a single notification channel configuration.
// Params is a key-value store whose keys are defined by NotifyChannelTypeMeta.Fields.
type NotifyChannel struct {
	Type    string            `json:"type"`    // "gotify", etc.
	Name    string            `json:"name"`    // user-friendly label
	Enabled bool              `json:"enabled"` // whether this channel is currently active
	Params  map[string]string `json:"params"`  // type-specific parameters (endpoint, token, etc.)
}

// ToNotifier creates a notify.Notifier from this channel configuration.
func (nc *NotifyChannel) ToNotifier() (notify.Notifier, error) {
	params := nc.applyDefaults()
	switch notify.ConvertNotificationType(nc.Type) {
	case notify.Gotify:
		return notify.NewGotify(params), nil
	case notify.PushPlus:
		return notify.NewPushplus(params), nil
	case notify.Bark:
		return notify.NewBark(params), nil
	case notify.Ntfy:
		return notify.NewNtfy(params), nil
	default:
		return nil, fmt.Errorf("unsupported notification type: %s", nc.Type)
	}
}

// applyDefaults fills missing params with default values defined in the channel type metadata.
func (nc *NotifyChannel) applyDefaults() map[string]string {
	if nc.Params == nil {
		nc.Params = make(map[string]string)
	}
	// Copy to avoid mutating the original
	result := make(map[string]string, len(nc.Params))
	for k, v := range nc.Params {
		result[k] = v
	}
	// Apply defaults from metadata for missing keys
	for _, ct := range notify.GetNotifyChannelTypes() {
		if ct.Type == nc.Type {
			for _, f := range ct.Fields {
				if _, ok := result[f.Key]; !ok && f.Default != "" {
					result[f.Key] = f.Default
				}
			}
			break
		}
	}
	return result
}

// NotifyChannelData manages the list of notification channels with thread safety.
type NotifyChannelData struct {
	mu       sync.RWMutex
	Channels []NotifyChannel `json:"channels"`
}

// NewNotifyChannelData creates a new empty NotifyChannelData.
func NewNotifyChannelData() *NotifyChannelData {
	return &NotifyChannelData{
		Channels: make([]NotifyChannel, 0),
	}
}

// GetAll returns a deep copy of all channels (including their Params maps).
func (ncd *NotifyChannelData) GetAll() []NotifyChannel {
	ncd.mu.RLock()
	defer ncd.mu.RUnlock()
	result := make([]NotifyChannel, len(ncd.Channels))
	for i, ch := range ncd.Channels {
		result[i] = ch
		if ch.Params != nil {
			result[i].Params = make(map[string]string, len(ch.Params))
			for k, v := range ch.Params {
				result[i].Params[k] = v
			}
		}
	}
	return result
}

// Add adds a new channel and returns its index.
func (ncd *NotifyChannelData) Add(ch NotifyChannel) int {
	ncd.mu.Lock()
	defer ncd.mu.Unlock()
	ncd.Channels = append(ncd.Channels, ch)
	return len(ncd.Channels) - 1
}

// Remove removes a channel at the given index. Returns false if index is out of bounds.
func (ncd *NotifyChannelData) Remove(index int) bool {
	ncd.mu.Lock()
	defer ncd.mu.Unlock()
	if index < 0 || index >= len(ncd.Channels) {
		return false
	}
	ncd.Channels = append(ncd.Channels[:index], ncd.Channels[index+1:]...)
	return true
}

// Update updates a channel at the given index. Returns false if index is out of bounds.
func (ncd *NotifyChannelData) Update(index int, ch NotifyChannel) bool {
	ncd.mu.Lock()
	defer ncd.mu.Unlock()
	if index < 0 || index >= len(ncd.Channels) {
		return false
	}
	ncd.Channels[index] = ch
	return true
}

// Count returns the number of channels.
func (ncd *NotifyChannelData) Count() int {
	ncd.mu.RLock()
	defer ncd.mu.RUnlock()
	return len(ncd.Channels)
}
