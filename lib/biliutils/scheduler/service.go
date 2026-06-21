package scheduler

import (
	"bilibili-ticket-golang/lib/biliutils"
	"bilibili-ticket-golang/lib/biliutils/clock"
	"bilibili-ticket-golang/lib/biliutils/notify"
	"bilibili-ticket-golang/lib/global"
	"bilibili-ticket-golang/cmd/gui/i18n"
	r "bilibili-ticket-golang/lib/models/bili/response"
	"bilibili-ticket-golang/cmd/gui/store/configuration"
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// FrontendTaskStatus mirrors TaskStatus but serializes error to string.
type FrontendTaskStatus struct {
	TaskID       string `json:"taskID"`
	TargetTime   string `json:"targetTime"`
	AdjustedTime string `json:"adjustedTime"`
	RemainingMs  int64  `json:"remainingMs"`
	Stat         int    `json:"stat"`
	StatName     string `json:"statName"`
	Error        string `json:"error,omitempty"`
	// Ticket info for display
	ProjectName string `json:"projectName"`
	ScreenName  string `json:"screenName"`
	SkuName     string `json:"skuName"`
	BuyerName   string `json:"buyerName"`
}

// FrontendTicket mirrors TicketEntry for Wails serialization.
type FrontendTicket struct {
	Hash        string          `json:"hash"`
	Expire      int64           `json:"expire"`
	Start       int64           `json:"start"`
	ProjectID   int64           `json:"projectId"`
	ProjectName string          `json:"projectName"`
	SkuID       int64           `json:"skuId"`
	SkuName     string          `json:"skuName"`
	ScreenID    int64           `json:"screenId"`
	ScreenName  string          `json:"screenName"`
	Buyers      []FrontendBuyer `json:"buyers"`
	// Stat persists the task execution result (RunningStat).
	Stat int `json:"stat"`
	// SortOrder is the chain order within the same buyer group.
	SortOrder int `json:"sortOrder"`
}

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

// FrontendBuyer is a simplified buyer struct for the frontend real-name picker.
type FrontendBuyer struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Tel        string `json:"tel"`
	PersonalID string `json:"personalId"`
	IDType     int    `json:"idType"`
}

// FrontendNotifyChannel mirrors NotifyChannel for Wails serialization.
type FrontendNotifyChannel struct {
	Index   int               `json:"index"`
	Type    string            `json:"type"`
	Name    string            `json:"name"`
	Enabled bool              `json:"enabled"`
	Params  map[string]string `json:"params"`
}

// SchedulerService bridges the scheduler, BiliClient and LogBroker to the
// Wails frontend. It is bound as a Wails service.
type SchedulerService struct {
	scheduler    *DynamicScheduler
	client       *biliutils.BiliClient
	logBroker    *LogBroker
	tickets      *configuration.TicketData
	bwsData      *configuration.BWSScheduler
	notifier     *notify.MultiNotifier
	notifyChData *configuration.NotifyChannelData
	store        *configuration.DataStorage // for persisting notify channel changes

	calibMu     sync.Mutex
	calibCtx    context.Context
	calibCancel context.CancelFunc

	notifyOpsMu sync.Mutex

	// Last measured clock offsets (server − local); positive = local is behind.
	biliOffset time.Duration
	ntpOffset  time.Duration
	offsetMu   sync.RWMutex
}

// NewSchedulerService creates a new SchedulerService.
func NewSchedulerService(client *biliutils.BiliClient, logBroker *LogBroker, tickets *configuration.TicketData, bwsData *configuration.BWSScheduler, notifier *notify.MultiNotifier, notifyChData *configuration.NotifyChannelData, store *configuration.DataStorage) *SchedulerService {
	return &SchedulerService{
		scheduler:    NewDynamicScheduler(),
		client:       client,
		logBroker:    logBroker,
		tickets:      tickets,
		bwsData:      bwsData,
		notifier:     notifier,
		notifyChData: notifyChData,
		store:        store,
	}
}

// AddTicketTask is retained only for old generated frontend bindings.
// Membership-ticket execution moved to the employer ClusterService.
func (svc *SchedulerService) AddTicketTask(hash string) error {
	return fmt.Errorf("membership ticket %s must be started through ClusterService", hash)
}

// RemoveTask stops and removes a task by its ticket hash.
func (svc *SchedulerService) RemoveTask(hash string) {
	svc.scheduler.RemoveTask(hash)
}

// ForceStartTask instantly executes a task, bypassing its timer.
func (svc *SchedulerService) ForceStartTask(hash string) {
	svc.scheduler.ForceStartTask(hash)
}

// GetTaskStatuses returns the current status of all scheduled tasks,
// enriched with ticket display names.
func (svc *SchedulerService) GetTaskStatuses() []FrontendTaskStatus {
	raw := svc.scheduler.GetTaskStatus()
	tickets := svc.tickets.GetTicketsNoMutate()
	ticketMap := make(map[string]*configuration.TicketEntry)
	for i := range tickets {
		ticketMap[tickets[i].Hash()] = &tickets[i]
	}

	result := make([]FrontendTaskStatus, 0, len(raw))
	for id, status := range raw {
		fts := FrontendTaskStatus{
			TaskID:       id,
			TargetTime:   status.TargetTime.Format("2006-01-02 15:04:05"),
			AdjustedTime: status.AdjustedTime.Format("2006-01-02 15:04:05"),
			RemainingMs:  status.Remaining.Milliseconds(),
			Stat:         int(status.Stat),
			StatName:     statName(status.Stat),
		}
		if status.Error != nil {
			fts.Error = status.Error.Error()
		}
		if t, ok := ticketMap[id]; ok {
			fts.ProjectName = t.ProjectName
			fts.ScreenName = t.ScreenName
			fts.SkuName = t.SkuName
			fts.BuyerName = t.FirstBuyer().String()
		}
		result = append(result, fts)
	}
	return result
}

// GetAllTickets returns all saved ticket entries as FrontendTicket.
func (svc *SchedulerService) GetAllTickets() []FrontendTicket {
	tickets := svc.tickets.GetTicketsNoMutate()
	result := make([]FrontendTicket, len(tickets))
	for i, t := range tickets {
		ft := FrontendTicket{
			Hash:        t.Hash(),
			Expire:      t.Expire,
			Start:       t.Start,
			ProjectID:   t.ProjectID,
			ProjectName: t.ProjectName,
			SkuID:       t.SkuID,
			SkuName:     t.SkuName,
			ScreenID:    t.ScreenID,
			ScreenName:  t.ScreenName,
			Stat:        t.Stat,
			SortOrder:   t.SortOrder,
		}
		for _, b := range t.Buyers {
			ft.Buyers = append(ft.Buyers, FrontendBuyer{
				ID:   b.ID,
				Name: b.Name,
				Tel:  b.Tel,
			})
		}
		result[i] = ft
	}
	return result
}

// AddTicket adds a ticket to persistent storage and returns its hash.
func (svc *SchedulerService) AddTicket(ticket FrontendTicket) (string, error) {
	buyers := make([]r.TicketBuyer, len(ticket.Buyers))
	for i, b := range ticket.Buyers {
		buyers[i] = r.TicketBuyer{
			Name: b.Name,
			Tel:  b.Tel,
			ID:   b.ID,
		}
		if b.ID > 0 {
			buyers[i].BuyerType = r.ForceRealName
		} else {
			buyers[i].BuyerType = r.Ordinary
		}
	}
	entry := configuration.TicketEntry{
		Expire:      ticket.Expire,
		Start:       ticket.Start,
		ProjectID:   ticket.ProjectID,
		ProjectName: ticket.ProjectName,
		SkuID:       ticket.SkuID,
		SkuName:     ticket.SkuName,
		ScreenID:    ticket.ScreenID,
		ScreenName:  ticket.ScreenName,
		SortOrder:   ticket.SortOrder,
		Buyers:      buyers,
	}

	hash := entry.Hash()

	if !entry.Valid() {
		return "", errors.New("ticket is expired or invalid")
	}

	// Count existing tickets in the same chain group BEFORE adding.
	// When the group goes 0→1 (first ticket), skip autoStartChainIfIdle:
	// the frontend will call AddTicketTask explicitly, and auto-starting
	// from here would race with that call. When the group already has
	// tickets, auto-start is needed in case no task is running.
	groupKey := entry.ChainGroupKey()
	groupCountBefore := 0
	if groupKey != "" {
		for _, t := range svc.tickets.GetTicketsNoMutate() {
			if t.ChainGroupKey() == groupKey {
				groupCountBefore++
			}
		}
	}

	if !svc.tickets.AddTicket(entry) {
		return "", errors.New(i18n.T("ticket.error.duplicate", nil))
	}

	fmt.Printf("[DEBUG] AddTicket: hash=%s expire=%d start=%d buyers=%d firstBuyer=%+v\n",
		hash, entry.Expire, entry.Start, len(entry.Buyers), entry.FirstBuyer())

	// Auto-start: only when there were already other tickets in the chain
	// group (not the 0→1 case) and no task is currently running.
	if groupKey != "" && groupCountBefore > 0 {
		svc.autoStartChainIfIdle(hash)
	}

	return hash, nil
}

// autoStartChainIfIdle checks whether any task in the same chain group as
// the given hash is currently running. If none is running, starts the first
// eligible ticket (by SortOrder) in the group — which may be the given hash
// itself or an earlier one if the new ticket was inserted at a lower order.
func (svc *SchedulerService) autoStartChainIfIdle(hash string) {
	tickets := svc.tickets.GetTicketsNoMutate()

	// Find the chain group key and collect same-group tickets.
	var groupKey string
	var groupTickets []*configuration.TicketEntry
	for i := range tickets {
		if tickets[i].Hash() == hash {
			groupKey = tickets[i].ChainGroupKey()
		}
	}
	if groupKey == "" {
		return
	}

	for i := range tickets {
		if tickets[i].ChainGroupKey() == groupKey {
			groupTickets = append(groupTickets, &tickets[i])
		}
	}

	// If any task in the group is already running, do nothing — the chain
	// will proceed naturally via onComplete switching.
	for _, t := range groupTickets {
		if svc.scheduler.HasTask(t.Hash()) {
			return
		}
	}

	// No task is running. Find the first eligible ticket (smallest SortOrder,
	// not Success/Error, not expired) and start it.
	var best *configuration.TicketEntry
	for _, t := range groupTickets {
		if t.Stat == int(StatSuccess) || t.Stat == int(StatError) {
			continue
		}
		if !t.Valid() {
			continue
		}
		if best == nil || t.SortOrder < best.SortOrder {
			best = t
		}
	}
	if best == nil {
		return
	}

	go func() {
		if err := svc.AddTicketTask(best.Hash()); err != nil {
			fmt.Printf("[chain] auto-start failed for %s: %v\n", best.Hash(), err)
		} else {
			fmt.Printf("[chain] auto-started %s (group was idle)\n", best.Hash())
		}
	}()
}

// RemoveTicket removes a ticket from storage and its associated task (if any).
func (svc *SchedulerService) RemoveTicket(hash string) {
	svc.scheduler.RemoveTaskAndStream(hash, func() {
		svc.logBroker.CloseStream(hash)
	})
	svc.tickets.RemoveTicketByHash(hash)
}

// ReloadTickets starts tasks for all valid tickets that are not yet scheduled.
// Call this on startup to recover persisted tickets.
// Completed tasks (StatSuccess/StatFailed/StatError) are not re-scheduled.
//
// For chainable tickets (real-name, same project), only the first waiting
// ticket (smallest SortOrder) in each chain group is started. The rest are
// triggered automatically by chain switching when the previous one terminates.
// Non-chainable tickets (ordinary) are all started independently.
func (svc *SchedulerService) ReloadTickets() {
	tickets := svc.tickets.GetTicketsNoMutate()
	existingMap := svc.scheduler.GetTaskStatus()

	// Collect chainable candidates per chain group key.
	type chainCandidate struct {
		hash  string
		order int
	}
	chainGroups := make(map[string][]chainCandidate)

	for i := range tickets {
		hash := tickets[i].Hash()
		if _, exists := existingMap[hash]; exists {
			continue
		}
		if !tickets[i].Valid() {
			continue
		}
		// Skip only successful tasks; failed/error/waiting are all eligible
		// for restart as long as the ticket hasn't expired.
		if tickets[i].Stat == int(StatSuccess) {
			continue
		}

		key := tickets[i].ChainGroupKey()
		if key == "" {
			// Non-chainable: start immediately.
			_ = svc.AddTicketTask(hash)
		} else {
			chainGroups[key] = append(chainGroups[key], chainCandidate{
				hash:  hash,
				order: tickets[i].SortOrder,
			})
		}
	}

	// For each chain group, start only the first (smallest SortOrder) ticket.
	// The rest will be started by chain switching when this one terminates.
	for _, candidates := range chainGroups {
		best := candidates[0]
		for _, c := range candidates[1:] {
			if c.order < best.order {
				best = c
			}
		}
		_ = svc.AddTicketTask(best.hash)
	}
}

// FetchRealNameBuyers obtains the real-name buyer list for a given project/sku/screen.
// Returns a list of verified buyers the user can choose from.
// Works for both hot and normal projects.
func (svc *SchedulerService) FetchRealNameBuyers() ([]FrontendBuyer, error) {
	err, buyers := svc.client.GetRealnameBuyerListNew()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", i18n.T("task.error.fetch_buyer", nil), err)
	}

	result := make([]FrontendBuyer, len(buyers))
	for i, b := range buyers {
		result[i] = FrontendBuyer{
			ID:         b.Id,
			Name:       b.Name,
			Tel:        b.Tel,
			PersonalID: b.IdCard,
			IDType:     b.IdType,
		}
	}
	return result, nil
}

// ReorderTickets rewrites the chain order (SortOrder) of the tickets
// identified by orderedHashes so they form an ascending chain within the
// buyer group of the first hash. The change is persisted to disk.
// Pass the full ordered list of hashes for the group (in the desired order).
//
// If the reorder changes which task should be currently running (the first
// waiting ticket in the new order differs from the currently running one),
// the previously running task is stopped and the new first one is started.
func (svc *SchedulerService) ReorderTickets(orderedHashes []string) error {
	if len(orderedHashes) == 0 {
		return errors.New("orderedHashes is empty")
	}
	if err := svc.tickets.ReorderInGroup(orderedHashes); err != nil {
		return fmt.Errorf("reorder: %w", err)
	}
	if err := svc.store.Save(); err != nil {
		return fmt.Errorf("persist: %w", err)
	}

	// Determine which task should be running now: the first ticket in the
	// new order that is not already successful and is still valid (not expired).
	// This includes Waiting, Failed, and Error stats — any non-success ticket
	// that hasn't expired can be (re)started.
	tickets := svc.tickets.GetTicketsNoMutate()
	ticketMap := make(map[string]*configuration.TicketEntry, len(tickets))
	for i := range tickets {
		ticketMap[tickets[i].Hash()] = &tickets[i]
	}

	var shouldRunHash string
	for _, h := range orderedHashes {
		t, ok := ticketMap[h]
		if !ok {
			continue
		}
		// Skip only successful tickets; failed/error/waiting are all eligible
		// as long as the ticket hasn't expired.
		if t.Stat != int(StatSuccess) && t.Valid() {
			shouldRunHash = h
			break
		}
	}

	// Find any currently running task in this chain group.
	var runningHash string
	for _, h := range orderedHashes {
		if svc.scheduler.HasTask(h) {
			runningHash = h
			break
		}
	}

	// If the running task is already the one that should run, nothing to do.
	if runningHash == shouldRunHash {
		return nil
	}

	// Stop the previously running task silently — don't mark it as Failed
	// in persistent storage, since this is a reorder swap, not a real failure.
	// Reset its persisted Stat to Waiting so it can be restarted later.
	if runningHash != "" {
		svc.scheduler.RemoveTaskSilent(runningHash, func() {
			svc.logBroker.CloseStream(runningHash)
		})
		svc.tickets.UpdateTicketStat(runningHash, int(StatWaiting))
	}

	// Start the new first task (if any).
	if shouldRunHash != "" {
		if err := svc.AddTicketTask(shouldRunHash); err != nil {
			return fmt.Errorf("start first task after reorder: %w", err)
		}
	}

	return nil
}

// GetChainTrigger returns the current chain-switch trigger mode.
// "success" = only switch to the next ticket on success;
// "any" = switch on any terminal state.
func (svc *SchedulerService) GetChainTrigger() string {
	t := svc.store.ChainTrigger
	if t == "" {
		return "success"
	}
	return t
}

// SetChainTrigger updates the chain-switch trigger mode and persists it.
// Accepts "success" or "any"; any other value is normalized to "success".
func (svc *SchedulerService) SetChainTrigger(mode string) error {
	switch mode {
	case "any":
		svc.store.ChainTrigger = "any"
	default:
		svc.store.ChainTrigger = "success"
	}
	return svc.store.Save()
}

func statName(s RunningStat) string {
	switch s {
	case StatWaiting:
		return i18n.T("stat.waiting", nil)
	case StatPending:
		return i18n.T("stat.pending", nil)
	case StatSuccess:
		return i18n.T("stat.success", nil)
	case StatFailed:
		return i18n.T("stat.failed", nil)
	case StatError:
		return i18n.T("stat.error", nil)
	default:
		return i18n.T("stat.unknown", nil)
	}
}

// ── Notification channel management ──────────────────────────────────────

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

// AddNotifyChannel adds a new notification channel and rebuilds the MultiNotifier.
func (svc *SchedulerService) AddNotifyChannel(ch FrontendNotifyChannel) (int, error) {
	svc.notifyOpsMu.Lock()
	defer svc.notifyOpsMu.Unlock()

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
	svc.persistNotify()
	return index, nil
}

// RemoveNotifyChannel removes a notification channel at the given index.
func (svc *SchedulerService) RemoveNotifyChannel(index int) error {
	svc.notifyOpsMu.Lock()
	defer svc.notifyOpsMu.Unlock()

	if !svc.notifyChData.Remove(index) {
		return errors.New(i18n.T("notify.error.index_not_found", map[string]interface{}{"Index": index}))
	}
	svc.rebuildNotifier()
	svc.persistNotify()
	return nil
}

// UpdateNotifyChannel updates a notification channel at the given index.
func (svc *SchedulerService) UpdateNotifyChannel(index int, ch FrontendNotifyChannel) error {
	svc.notifyOpsMu.Lock()
	defer svc.notifyOpsMu.Unlock()

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
	svc.rebuildNotifier()
	svc.persistNotify()
	return nil
}

// TestNotifyChannel sends a test message through the specified channel.
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

// GetNotifyChannelTypes returns metadata for all supported notification channel types.
// The frontend uses this to dynamically render the channel configuration form.
func (svc *SchedulerService) GetNotifyChannelTypes() []notify.NotifyChannelTypeMeta {
	return notify.GetNotifyChannelTypes()
}

// rebuildNotifier clears and rebuilds the MultiNotifier from all persisted channels.
// Disabled channels are skipped.
func (svc *SchedulerService) rebuildNotifier() {
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

// persistNotify saves the notification channel changes to persistent storage.
func (svc *SchedulerService) persistNotify() {
	if svc.store == nil {
		return
	}
	if err := svc.store.Save(); err != nil {
		println("Failed to persist notify channels:", err.Error())
	}
}

// ── Clock calibration ────────────────────────────────────────────────────

const clockCalibrationInterval = 120 * time.Second

// StartClockCalibration begins a background goroutine that periodically
// calibrates the scheduler's global time offset against Bilibili's server
// clock (with NTP fallback). Calibration runs every clockCalibrationInterval
// (default 120s).
func (svc *SchedulerService) StartClockCalibration() {
	svc.calibMu.Lock()
	if svc.calibCancel != nil {
		// Stop any previous loop before starting a new one.
		svc.calibCancel()
	}
	svc.calibCtx, svc.calibCancel = context.WithCancel(context.Background())
	ctx := svc.calibCtx
	svc.calibMu.Unlock()

	// Calibrate immediately at startup
	svc.calibrateOnce()

	go func() {
		ticker := time.NewTicker(clockCalibrationInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				svc.calibrateOnce()
			}
		}
	}()
}

// StopClockCalibration stops the background clock calibration goroutine.
func (svc *SchedulerService) StopClockCalibration() {
	svc.calibMu.Lock()
	cancel := svc.calibCancel
	svc.calibCancel = nil
	svc.calibCtx = nil
	svc.calibMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// calibrateOnce performs a single clock offset measurement and updates
// the scheduler. Skips the update if no tasks are present.
func (svc *SchedulerService) calibrateOnce() {
	// Always measure both Bilibili and NTP for display purposes.
	// Bilibili is preferred for the scheduler; fall back to NTP if it fails.

	// 1. Bilibili API
	biliOff, biliErr := clock.GetBilibiliClockOffset()
	if biliErr == nil {
		svc.offsetMu.Lock()
		svc.biliOffset = biliOff
		svc.offsetMu.Unlock()
		println("[clock] Bilibili offset:", biliOff)
	} else {
		println("[clock] Bilibili offset failed:", biliErr.Error())
	}

	// 2. NTP (always fetch independently)
	ntpOff, ntpErr := clock.GetNTPClockOffset("ntp.aliyun.com")
	if ntpErr == nil {
		svc.offsetMu.Lock()
		svc.ntpOffset = ntpOff
		svc.offsetMu.Unlock()
		println("[clock] NTP offset:", ntpOff)
	} else {
		println("[clock] NTP offset failed:", ntpErr.Error())
	}

	// 3. Update scheduler offset (Bilibili preferred, NTP fallback)
	if svc.scheduler.GetTaskCount() == 0 {
		return
	}

	var offset time.Duration
	if biliErr == nil {
		offset = biliOff
	} else if ntpErr == nil {
		offset = ntpOff
	} else {
		println("[clock] both sources failed, skipping calibration")
		return
	}

	oldOffset := svc.scheduler.GetGlobalOffset()
	svc.scheduler.SetGlobalOffset(offset)

	if global.Debug {
		fmt.Printf("[clock] offset updated: %v → %v (Δ%v)\n", oldOffset, offset, offset-oldOffset)
	}
}

// ── Retry interval settings ─────────────────────────────────────────────

// GetRetryInterval returns the global retry interval in milliseconds.
func (svc *SchedulerService) GetRetryInterval() int {
	return svc.store.RetryIntervalMs
}

// SetRetryInterval updates the global retry interval (ms), persists it,
// and broadcasts the change to all currently running tasks.
func (svc *SchedulerService) SetRetryInterval(ms int) {
	if ms < 50 {
		ms = 50 // minimum 50ms to avoid excessive requests
	}
	svc.store.RetryIntervalMs = ms
	if err := svc.store.Save(); err != nil {
		println("Failed to persist retry interval:", err.Error())
	}
	// Propagate to all running tasks immediately
	svc.scheduler.BroadcastInterval(time.Duration(ms) * time.Millisecond)
}

// GetStartDelay returns the global start delay (one-time initial delay) in milliseconds.
func (svc *SchedulerService) GetStartDelay() int {
	return svc.store.StartDelayMs
}

// SetStartDelay updates the global start delay (ms, 0-500), persists it,
// and broadcasts the change to all currently running tasks.
func (svc *SchedulerService) SetStartDelay(ms int) {
	if ms < 0 {
		ms = 0
	}
	if ms > 500 {
		ms = 500
	}
	svc.store.StartDelayMs = ms
	if err := svc.store.Save(); err != nil {
		println("Failed to persist start delay:", err.Error())
	}
	// Propagate to all running tasks immediately
	svc.scheduler.BroadcastStartDelay(time.Duration(ms) * time.Millisecond)
}

// GetGlobalOffset returns the current clock offset (server time − local time) in milliseconds.
// Positive means local clock is behind the server.
func (svc *SchedulerService) GetGlobalOffset() int64 {
	return svc.scheduler.GetGlobalOffset().Milliseconds()
}

// GetBilibiliOffset returns the last measured Bilibili API clock offset in milliseconds.
func (svc *SchedulerService) GetBilibiliOffset() int64 {
	svc.offsetMu.RLock()
	defer svc.offsetMu.RUnlock()
	return svc.biliOffset.Milliseconds()
}

// GetNTPOffset returns the last measured NTP clock offset in milliseconds.
func (svc *SchedulerService) GetNTPOffset() int64 {
	svc.offsetMu.RLock()
	defer svc.offsetMu.RUnlock()
	return svc.ntpOffset.Milliseconds()
}

// ── BWS (Bilibili World) reservation management ─────────────────────────

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

// AddBWSTask creates a BWSTask from a persisted BWS entry hash and starts it.
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

	// Check not already scheduled
	if svc.scheduler.HasTask(hash) {
		return fmt.Errorf("BWS task already exists")
	}

	targetTime := time.Unix(entry.ReserveTime, 0)
	logCh := svc.logBroker.CreateStream(hash)

	// Wire up notification
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

// RemoveBWSEntry removes a BWS entry from storage and its associated task.
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

// ReloadBWSTasks starts tasks for all valid BWS entries that are not yet
// scheduled. Call this on startup to recover persisted entries.
// Completed tasks (StatSuccess/StatFailed/StatError) are not re-scheduled.
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
