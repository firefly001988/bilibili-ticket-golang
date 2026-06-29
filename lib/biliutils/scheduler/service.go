package scheduler

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"bilibili-ticket-golang/cmd/gui/i18n"
	"bilibili-ticket-golang/cmd/gui/store/configuration"
	"bilibili-ticket-golang/lib/biliutils"
	"bilibili-ticket-golang/lib/biliutils/notify"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// FrontendBWSEntry mirrors BWSEntry for Wails serialization.
type FrontendBWSEntry struct {
	Hash          string `json:"hash"`
	ActivityID    int    `json:"activityId"`
	TicketNo      string `json:"ticketNo"`
	ActivityTitle string `json:"activityTitle"`
	ReserveTime   int64  `json:"reserveTime"`
	ReserveDate   string `json:"reserveDate"`
	Expire        int64  `json:"expire"`
	StartDelayMs  int    `json:"startDelayMs"`
	LoopDelayMs   int    `json:"loopDelayMs"`
	Stat          int    `json:"stat"`
}

// FrontendNotifyChannel mirrors NotifyChannel for Wails serialization.
type FrontendNotifyChannel struct {
	Index   int               `json:"index"`
	Type    string            `json:"type"`
	Name    string            `json:"name"`
	Enabled bool              `json:"enabled"`
	Params  map[string]string `json:"params"`
}

// SchedulerService houses BWS (Bilibili World) reservations and
// notification‑channel management.  Membership‑ticket execution
// moved to ClusterService.
type SchedulerService struct {
	app          *application.App
	scheduler    *DynamicScheduler
	client       *biliutils.BiliClient
	logBroker    *LogBroker
	bwsData      *configuration.BWSScheduler
	notifier     *notify.MultiNotifier
	notifyChData *configuration.NotifyChannelData
	store        *configuration.DataStorage
	notifyMu     sync.Mutex
}

// SetApp stores the Wails v3 application reference.
func (svc *SchedulerService) SetApp(app *application.App) {
	svc.app = app
}

// NewSchedulerService creates a new SchedulerService for BWS
// reservations and notification channels.
func NewSchedulerService(
	client *biliutils.BiliClient,
	logBroker *LogBroker,
	bwsData *configuration.BWSScheduler,
	notifier *notify.MultiNotifier,
	notifyChData *configuration.NotifyChannelData,
	store *configuration.DataStorage,
) *SchedulerService {
	return &SchedulerService{
		scheduler:    NewDynamicScheduler(),
		client:       client,
		logBroker:    logBroker,
		bwsData:      bwsData,
		notifier:     notifier,
		notifyChData: notifyChData,
		store:        store,
	}
}

// ─────────────────────────────────────────────────────────────────
// BWS (Bilibili World) reservation management
// ─────────────────────────────────────────────────────────────────

// AddBWSEntry persists a BWS entry and returns its hash.
func (svc *SchedulerService) AddBWSEntry(entry FrontendBWSEntry) (string, error) {
	e := configuration.BWSEntry{
		ActivityID:    entry.ActivityID,
		TicketNo:      entry.TicketNo,
		ActivityTitle: entry.ActivityTitle,
		ReserveTime:   entry.ReserveTime,
		ReserveDate:   entry.ReserveDate,
		Expire:        entry.Expire,
		StartDelayMs:  entry.StartDelayMs,
		LoopDelayMs:   entry.LoopDelayMs,
	}
	hash := e.Hash()

	if !e.Valid() {
		return "", fmt.Errorf("BWS entry is invalid or expired")
	}

	if !svc.bwsData.AddEntry(e) {
		return "", errors.New(i18n.T("bws.error.duplicate", nil))
	}

	if svc.store != nil {
		if err := svc.store.Save(); err != nil {
			println("Failed to persist BWS entry:", err.Error())
		}
	}

	return hash, nil
}

// AddBWSTask creates a BWSTask from a persisted BWS entry hash and
// starts it.
func (svc *SchedulerService) AddBWSTask(hash string) error {
	entries := svc.bwsData.GetEntriesNoMutate()

	var entry *configuration.BWSEntry
	for i := range entries {
		if entries[i].Hash() == hash {
			entry = &entries[i]
			break
		}
	}
	if entry == nil {
		return fmt.Errorf("BWS entry not found: %s", hash)
	}

	if !entry.Valid() {
		return fmt.Errorf("BWS entry is expired or invalid")
	}

	if svc.scheduler.HasTask(hash) {
		return fmt.Errorf("BWS task already exists")
	}

	targetTime := time.Unix(entry.ReserveTime, 0)
	logCh := svc.logBroker.CreateStream(hash)

	var notifyFn func(string)
	if svc.notifier != nil && svc.notifier.Count() > 0 {
		notifyFn = func(msg string) {
			svc.notifier.Notify(msg)
		}
	}

	task, err := NewBWSTask(svc.client, *entry, notifyFn, logCh, func(stat RunningStat, userStopped bool) {
		svc.bwsData.UpdateEntryStat(hash, int(stat))
	})
	if err != nil {
		return fmt.Errorf("create BWS task: %w", err)
	}
	task.ID = hash
	task.TargetTime = targetTime

	if !svc.scheduler.AddTask(task) {
		return fmt.Errorf("BWS task already exists")
	}
	return nil
}

// RemoveBWSEntry removes a BWS entry from storage and its associated
// task.
func (svc *SchedulerService) RemoveBWSEntry(hash string) {
	svc.scheduler.RemoveTaskAndStream(hash, func() {
		svc.logBroker.CloseStream(hash)
	})
	svc.bwsData.RemoveEntryByHash(hash)
	if svc.store != nil {
		if err := svc.store.Save(); err != nil {
			println("Failed to persist BWS entry removal:", err.Error())
		}
	}
}

// GetBWSEntries returns all saved BWS entries.
func (svc *SchedulerService) GetBWSEntries() []FrontendBWSEntry {
	entries := svc.bwsData.GetEntriesNoMutate()
	result := make([]FrontendBWSEntry, len(entries))
	for i, e := range entries {
		result[i] = FrontendBWSEntry{
			Hash:          e.Hash(),
			ActivityID:    e.ActivityID,
			TicketNo:      e.TicketNo,
			ActivityTitle: e.ActivityTitle,
			ReserveTime:   e.ReserveTime,
			ReserveDate:   e.ReserveDate,
			Expire:        e.Expire,
			StartDelayMs:  e.StartDelayMs,
			LoopDelayMs:   e.LoopDelayMs,
			Stat:          e.Stat,
		}
	}
	return result
}

// ReloadBWSTasks starts tasks for all valid BWS entries that are not
// yet scheduled.  Call this on startup to recover persisted entries.
func (svc *SchedulerService) ReloadBWSTasks() {
	entries := svc.bwsData.GetEntriesNoMutate()
	existingMap := svc.scheduler.GetTaskStatus()

	for i := range entries {
		hash := entries[i].Hash()
		if _, exists := existingMap[hash]; exists {
			continue
		}
		if !entries[i].Valid() {
			continue
		}
		if entries[i].Stat == int(StatSuccess) || entries[i].Stat == int(StatFailed) || entries[i].Stat == int(StatError) {
			continue
		}
		_ = svc.AddBWSTask(hash)
	}
}

// ── Notification channel management ─────────────────────────

// GetNotifyChannels returns all configured notification channels.
func (svc *SchedulerService) GetNotifyChannels() []FrontendNotifyChannel {
	channels := svc.notifyChData.GetAll()
	result := make([]FrontendNotifyChannel, len(channels))
	for i, ch := range channels {
		result[i] = FrontendNotifyChannel{
			Index:   i,
			Type:    ch.Type,
			Name:    ch.Name,
			Enabled: ch.Enabled,
			Params:  ch.Params,
		}
	}
	return result
}

// AddNotifyChannel adds a new notification channel and rebuilds the
// MultiNotifier.
func (svc *SchedulerService) AddNotifyChannel(ch FrontendNotifyChannel) (int, error) {
	svc.notifyMu.Lock()
	defer svc.notifyMu.Unlock()

	nc := configuration.NotifyChannel{
		Type:    ch.Type,
		Name:    ch.Name,
		Enabled: ch.Enabled,
		Params:  ch.Params,
	}

	n, err := nc.ToNotifier()
	if err != nil {
		return -1, fmt.Errorf("%s: %w", i18n.T("notify.error.create", nil), err)
	}

	index := svc.notifyChData.Add(nc)
	svc.notifier.Add(n)
	svc.persistNotifyLocked()
	return index, nil
}

// RemoveNotifyChannel removes a notification channel at the given
// index.
func (svc *SchedulerService) RemoveNotifyChannel(index int) error {
	svc.notifyMu.Lock()
	defer svc.notifyMu.Unlock()

	if !svc.notifyChData.Remove(index) {
		return errors.New(i18n.T("notify.error.index_not_found", map[string]interface{}{"Index": index}))
	}
	svc.rebuildNotifierLocked()
	svc.persistNotifyLocked()
	return nil
}

// UpdateNotifyChannel updates a notification channel at the given
// index.
func (svc *SchedulerService) UpdateNotifyChannel(index int, ch FrontendNotifyChannel) error {
	svc.notifyMu.Lock()
	defer svc.notifyMu.Unlock()

	nc := configuration.NotifyChannel{
		Type:    ch.Type,
		Name:    ch.Name,
		Enabled: ch.Enabled,
		Params:  ch.Params,
	}

	if _, err := nc.ToNotifier(); err != nil {
		return fmt.Errorf("%s: %w", i18n.T("notify.error.update", nil), err)
	}

	if !svc.notifyChData.Update(index, nc) {
		return errors.New(i18n.T("notify.error.index_not_found", map[string]interface{}{"Index": index}))
	}
	svc.rebuildNotifierLocked()
	svc.persistNotifyLocked()
	return nil
}

// TestNotifyChannel sends a test message through the specified
// channel.
func (svc *SchedulerService) TestNotifyChannel(index int) error {
	channels := svc.notifyChData.GetAll()
	if index < 0 || index >= len(channels) {
		return errors.New(i18n.T("notify.error.index_not_found", map[string]interface{}{"Index": index}))
	}

	n, err := channels[index].ToNotifier()
	if err != nil {
		return err
	}

	if b, err := n.Test(); !b {
		return errors.New(i18n.T("notify.error.test_failed", map[string]interface{}{"Error": err}))
	}
	return nil
}

// GetNotifyChannelTypes returns metadata for all supported
// notification channel types.
func (svc *SchedulerService) GetNotifyChannelTypes() []notify.NotifyChannelTypeMeta {
	return notify.GetNotifyChannelTypes()
}

func (svc *SchedulerService) rebuildNotifierLocked() {
	svc.notifier.Clear()
	for _, ch := range svc.notifyChData.GetAll() {
		if !ch.Enabled {
			continue
		}
		n, err := ch.ToNotifier()
		if err == nil {
			svc.notifier.Add(n)
		}
	}
}

func (svc *SchedulerService) persistNotifyLocked() {
	if svc.store == nil {
		return
	}
	if err := svc.store.Save(); err != nil {
		println("Failed to persist notify channels:", err.Error())
	}
}
