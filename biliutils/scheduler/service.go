package scheduler

import (
	"bilibili-ticket-golang/biliutils"
	"bilibili-ticket-golang/biliutils/clock"
	"bilibili-ticket-golang/biliutils/notify"
	"bilibili-ticket-golang/global"
	r "bilibili-ticket-golang/models/bili/response"
	"bilibili-ticket-golang/store/configuration"
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
	Hash        string `json:"hash"`
	Expire      int64  `json:"expire"`
	Start       int64  `json:"start"`
	ProjectID   int64  `json:"projectId"`
	ProjectName string `json:"projectName"`
	SkuID       int64  `json:"skuId"`
	SkuName     string `json:"skuName"`
	ScreenID    int64  `json:"screenId"`
	ScreenName  string `json:"screenName"`
	BuyerName   string `json:"buyerName"`
	BuyerTel    string `json:"buyerTel,omitempty"`
	BuyerID     int64  `json:"buyerId,omitempty"`
	// Stat persists the task execution result (RunningStat).
	Stat int `json:"stat"`
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
	notifier     *notify.MultiNotifier
	notifyChData *configuration.NotifyChannelData
	store        *configuration.DataStorage // for persisting notify channel changes

	calibCtx    context.Context
	calibCancel context.CancelFunc

	// Last measured clock offsets (server − local); positive = local is behind.
	biliOffset time.Duration
	ntpOffset  time.Duration
	offsetMu   sync.RWMutex
}

// NewSchedulerService creates a new SchedulerService.
func NewSchedulerService(client *biliutils.BiliClient, logBroker *LogBroker, tickets *configuration.TicketData, notifier *notify.MultiNotifier, notifyChData *configuration.NotifyChannelData, store *configuration.DataStorage) *SchedulerService {
	return &SchedulerService{
		scheduler:    NewDynamicScheduler(),
		client:       client,
		logBroker:    logBroker,
		tickets:      tickets,
		notifier:     notifier,
		notifyChData: notifyChData,
		store:        store,
	}
}

// AddTicketTask creates a TicketTask from the given ticket hash and starts it.
// intervalMs is the polling interval in milliseconds (e.g. 200 = 200ms between retries).
func (svc *SchedulerService) AddTicketTask(hash string, intervalMs int) error {
	// Find the ticket by hash (non-mutating read to avoid side effects)
	tickets := svc.tickets.GetTicketsNoMutate()

	// Debug: dump all known ticket hashes
	if global.Debug {
		fmt.Printf("[DEBUG] AddTicketTask: looking for hash=%s, total tickets=%d\n", hash, len(tickets))
		for i, t := range tickets {
			fmt.Printf("[DEBUG]   ticket[%d]: hash=%s expire=%d start=%d valid=%v\n",
				i, t.Hash(), t.Expire, t.Start, t.Valid())
		}
	}

	var ticket *configuration.TicketEntry
	for i := range tickets {
		if tickets[i].Hash() == hash {
			ticket = &tickets[i]
			break
		}
	}
	if ticket == nil {
		return fmt.Errorf("ticket not found: %s", hash)
	}

	if !ticket.Valid() {
		return errors.New("ticket is expired or invalid")
	}

	// Check not already scheduled
	existing := svc.scheduler.GetTaskStatus()
	for id := range existing {
		if id == hash {
			return errors.New("task already exists")
		}
	}

	targetTime := time.Unix(ticket.Start, 0)
	interval := time.Duration(intervalMs) * time.Millisecond

	logCh := svc.logBroker.CreateStream(hash)

	// Wire up notification: use MultiNotifier.Notify if there are any channels configured
	var notifyFn func(string)
	if svc.notifier != nil && svc.notifier.Count() > 0 {
		notifyFn = func(msg string) {
			svc.notifier.Notify(msg)
		}
	}

	task, err := NewTicketTask(svc.client, *ticket, notifyFn, interval, logCh, func(stat RunningStat) {
		svc.tickets.UpdateTicketStat(hash, int(stat))
	})
	if err != nil {
		return fmt.Errorf("create task: %w", err)
	}
	task.ID = hash
	task.TargetTime = targetTime

	svc.scheduler.AddTask(task)
	return nil
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
			fts.BuyerName = t.Buyer.String()
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
			BuyerName:   t.Buyer.Name,
			BuyerTel:    t.Buyer.Tel,
			BuyerID:     t.Buyer.ID,
			Stat:        t.Stat,
		}
		result[i] = ft
	}
	return result
}

// AddTicket adds a ticket to persistent storage and returns its hash.
func (svc *SchedulerService) AddTicket(ticket FrontendTicket) (string, error) {
	entry := configuration.TicketEntry{
		Expire:      ticket.Expire,
		Start:       ticket.Start,
		ProjectID:   ticket.ProjectID,
		ProjectName: ticket.ProjectName,
		SkuID:       ticket.SkuID,
		SkuName:     ticket.SkuName,
		ScreenID:    ticket.ScreenID,
		ScreenName:  ticket.ScreenName,
		Buyer: r.TicketBuyer{
			Name: ticket.BuyerName,
			Tel:  ticket.BuyerTel,
			ID:   ticket.BuyerID,
		},
	}
	if ticket.BuyerID > 0 {
		entry.Buyer.BuyerType = r.ForceRealName
	} else {
		entry.Buyer.BuyerType = r.Ordinary
	}

	hash := entry.Hash()

	if !entry.Valid() {
		return "", errors.New("ticket is expired or invalid")
	}

	if !svc.tickets.AddTicket(entry) {
		return "", fmt.Errorf("重复票据: 相同的购票人+项目+场次+票种已存在")
	}

	fmt.Printf("[DEBUG] AddTicket: hash=%s expire=%d start=%d buyerType=%d buyer=%+v\n",
		hash, entry.Expire, entry.Start, entry.Buyer.BuyerType, entry.Buyer)

	return hash, nil
}

// RemoveTicket removes a ticket from storage and its associated task (if any).
func (svc *SchedulerService) RemoveTicket(hash string) {
	svc.scheduler.RemoveTask(hash)
	svc.tickets.RemoveTicketByHash(hash)
}

// ReloadTickets starts tasks for all valid tickets that are not yet scheduled.
// Call this on startup to recover persisted tickets.
// Completed tasks (StatSuccess/StatFailed/StatError) are not re-scheduled.
func (svc *SchedulerService) ReloadTickets(intervalMs int) {
	tickets := svc.tickets.GetTicketsNoMutate()
	existingMap := svc.scheduler.GetTaskStatus()

	for i := range tickets {
		hash := tickets[i].Hash()
		if _, exists := existingMap[hash]; exists {
			continue
		}
		if !tickets[i].Valid() {
			continue
		}
		// Skip tasks that already finished
		if tickets[i].Stat == int(StatSuccess) || tickets[i].Stat == int(StatFailed) || tickets[i].Stat == int(StatError) {
			continue
		}
		_ = svc.AddTicketTask(hash, intervalMs)
	}
}

// FetchRealNameBuyers obtains the real-name buyer list for a given project/sku/screen.
// Returns a list of verified buyers the user can choose from.
// Works for both hot and normal projects.
func (svc *SchedulerService) FetchRealNameBuyers() ([]FrontendBuyer, error) {
	err, buyers := svc.client.GetRealnameBuyerList()
	if err != nil {
		return nil, fmt.Errorf("获取实名信息失败: %w", err)
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

func statName(s RunningStat) string {
	switch s {
	case StatWaiting:
		return "等待中"
	case StatPending:
		return "执行中"
	case StatSuccess:
		return "已成功"
	case StatFailed:
		return "已失败"
	case StatError:
		return "错误"
	default:
		return "未知"
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
	nc := configuration.NotifyChannel{
		Type:    ch.Type,
		Name:    ch.Name,
		Enabled: ch.Enabled,
		Params:  ch.Params,
	}

	n, err := nc.ToNotifier()
	if err != nil {
		return -1, fmt.Errorf("创建通知渠道失败: %w", err)
	}

	index := svc.notifyChData.Add(nc)
	svc.notifier.Add(n)
	svc.persistNotify()
	return index, nil
}

// RemoveNotifyChannel removes a notification channel at the given index.
func (svc *SchedulerService) RemoveNotifyChannel(index int) error {
	if !svc.notifyChData.Remove(index) {
		return fmt.Errorf("通知渠道索引 %d 不存在", index)
	}
	// Rebuild MultiNotifier from remaining channels
	svc.rebuildNotifier()
	svc.persistNotify()
	return nil
}

// UpdateNotifyChannel updates a notification channel at the given index.
func (svc *SchedulerService) UpdateNotifyChannel(index int, ch FrontendNotifyChannel) error {
	nc := configuration.NotifyChannel{
		Type:    ch.Type,
		Name:    ch.Name,
		Enabled: ch.Enabled,
		Params:  ch.Params,
	}

	n, err := nc.ToNotifier()
	if err != nil {
		return fmt.Errorf("更新通知渠道失败: %w", err)
	}

	if !svc.notifyChData.Update(index, nc) {
		return fmt.Errorf("通知渠道索引 %d 不存在", index)
	}
	svc.rebuildNotifier()
	_ = n // n is used to validate; notifier is rebuilt from all channels
	svc.persistNotify()
	return nil
}

// TestNotifyChannel sends a test message through the specified channel.
func (svc *SchedulerService) TestNotifyChannel(index int) error {
	channels := svc.notifyChData.GetAll()
	if index < 0 || index >= len(channels) {
		return fmt.Errorf("通知渠道索引 %d 不存在", index)
	}

	n, err := channels[index].ToNotifier()
	if err != nil {
		return err
	}

	if b, err := n.Test(); !b {
		return fmt.Errorf("测试消息发送失败：请检查通知渠道配置是否正确: %s", err)
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
// clock (with NTP fallback). Calibration runs every 10 seconds while there
// are active tasks.
func (svc *SchedulerService) StartClockCalibration() {
	svc.calibCtx, svc.calibCancel = context.WithCancel(context.Background())

	// Calibrate immediately at startup
	svc.calibrateOnce()

	go func() {
		ticker := time.NewTicker(clockCalibrationInterval)
		defer ticker.Stop()

		for {
			select {
			case <-svc.calibCtx.Done():
				return
			case <-ticker.C:
				svc.calibrateOnce()
			}
		}
	}()
}

// StopClockCalibration stops the background clock calibration goroutine.
func (svc *SchedulerService) StopClockCalibration() {
	if svc.calibCancel != nil {
		svc.calibCancel()
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
