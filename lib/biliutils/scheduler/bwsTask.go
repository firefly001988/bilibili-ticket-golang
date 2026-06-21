package scheduler

import (
	"bilibili-ticket-golang/lib/biliutils"
	"bilibili-ticket-golang/cmd/gui/i18n"
	"bilibili-ticket-golang/cmd/gui/store/configuration"
	"context"
	"fmt"
	"sync"
	"time"
)

// BWSTask implements ITask for BWS (Bilibili World) activity reservation.
//
// It follows the same lifecycle as TicketTask:
//   - waitForExecution → timer fires → executeAndStop → bwsReservationFunc
//   - bwsReservationFunc: waitForReserveTime → startReservationLoop
type BWSTask struct {
	ID         string
	TargetTime time.Time

	timer       *time.Timer
	stopChan    chan struct{}
	mutex       sync.RWMutex
	statLock    sync.Mutex
	executeLock sync.Mutex
	taskErr     error
	client      *biliutils.BiliClient
	config      configuration.BWSEntry
	notifyFn    func(message string)
	logCh       chan<- LogEntry
	stat        RunningStat
	ctx         context.Context
	cancelFunc  context.CancelFunc
	onComplete  func(stat RunningStat, userStopped bool)
	userStopped bool
}

// NewBWSTask creates a new BWSTask.
func NewBWSTask(client *biliutils.BiliClient, config configuration.BWSEntry, notifyFn func(string), logCh chan<- LogEntry, onComplete func(RunningStat, bool)) (*BWSTask, error) {
	if !config.Valid() {
		return nil, fmt.Errorf("bws config is invalid: %+v", config)
	}
	if client == nil {
		return nil, fmt.Errorf("bili-client is nil")
	}

	ctx, cancel := context.WithCancel(context.Background())
	return &BWSTask{
		client:     client,
		config:     config,
		notifyFn:   notifyFn,
		logCh:      logCh,
		stat:       StatWaiting,
		ctx:        ctx,
		cancelFunc: cancel,
		stopChan:   make(chan struct{}),
		onComplete: onComplete,
	}, nil
}

// ── ITask interface ────────────────────────────────────────────────────────

func (t *BWSTask) GetID() string            { return t.ID }
func (t *BWSTask) GetTargetTime() time.Time { return t.TargetTime }

func (t *BWSTask) Start(globalOffset time.Duration) {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	if t.GetStat() != StatWaiting {
		return
	}
	go t.run(globalOffset)
}

func (t *BWSTask) ForceStart() {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	if t.timer != nil && !t.timer.Stop() {
		select {
		case <-t.timer.C:
		default:
		}
	}
	if t.GetStat() != StatWaiting {
		return
	}
	select {
	case <-t.stopChan:
		// already closed
	default:
		close(t.stopChan)
	}
	go t.executeAndStop()
}

func (t *BWSTask) Stop() {
	t.mutex.Lock()
	stat := t.GetStat()
	if stat != StatWaiting && stat != StatPending {
		t.mutex.Unlock()
		return
	}
	t.userStopped = true
	select {
	case <-t.stopChan:
		// already closed
	default:
		close(t.stopChan)
	}
	if t.timer != nil {
		t.timer.Stop()
	}
	t.mutex.Unlock()
	t.setStat(StatFailed)
}

// StopSilent stops the task without triggering onComplete, so the persisted
// stat is NOT updated. Used by ReorderTickets when swapping the running task.
func (t *BWSTask) StopSilent() {
	t.mutex.Lock()
	stat := t.GetStat()
	if stat != StatWaiting && stat != StatPending {
		t.mutex.Unlock()
		return
	}
	select {
	case <-t.stopChan:
		// already closed
	default:
		close(t.stopChan)
	}
	if t.timer != nil {
		t.timer.Stop()
	}
	t.mutex.Unlock()

	// Cancel context and set stat to Failed in-memory only, but skip onComplete
	// so the persisted Stat stays as-is (Waiting).
	t.statLock.Lock()
	t.stat = StatFailed
	t.cancelFunc()
	t.statLock.Unlock()
}

func (t *BWSTask) GetStat() RunningStat {
	t.statLock.Lock()
	defer t.statLock.Unlock()
	return t.stat
}

func (t *BWSTask) GetError() error {
	t.statLock.Lock()
	defer t.statLock.Unlock()
	return t.taskErr
}

func (t *BWSTask) UpdateInterval(newInterval time.Duration) {
	// BWS task uses per-config loop delay; store it
	t.mutex.Lock()
	defer t.mutex.Unlock()
	t.sendLog(LogInfo, i18n.T("bws.interval_updated", map[string]interface{}{"Old": time.Duration(t.config.LoopDelayMs) * time.Millisecond, "New": newInterval}))
	t.config.LoopDelayMs = int(newInterval.Milliseconds())
}

func (t *BWSTask) UpdateStartDelay(newDelay time.Duration) {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	t.sendLog(LogInfo, i18n.T("bws.delay_updated", map[string]interface{}{"Old": time.Duration(t.config.StartDelayMs) * time.Millisecond, "New": newDelay}))
	t.config.StartDelayMs = int(newDelay.Milliseconds())
}

func (t *BWSTask) rescheduleWithNewOffset(offsetDelta time.Duration) {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	if t.GetStat() != StatWaiting || t.timer == nil {
		return
	}
	if !t.timer.Stop() {
		select {
		case <-t.timer.C:
		default:
		}
	}
	adjustedTime := t.TargetTime.Add(offsetDelta)
	waitDuration := time.Until(adjustedTime)
	if waitDuration <= 0 {
		// Clean up old goroutine before starting immediate execution.
		select {
		case <-t.stopChan:
		default:
			close(t.stopChan)
		}
		t.stopChan = make(chan struct{})
		go t.executeAndStop()
		return
	}
	t.timer = time.NewTimer(waitDuration)
	timerC := t.timer.C // captured under mutex — no data race
	select {
	case <-t.stopChan:
	default:
		close(t.stopChan)
	}
	t.stopChan = make(chan struct{})
	go t.waitForExecution(timerC)
}

// ── Internal state helpers ─────────────────────────────────────────────────

func (t *BWSTask) setStat(stat RunningStat) {
	t.statLock.Lock()
	wasTerminal := t.stat > StatPending
	t.stat = stat
	if stat > StatPending {
		t.cancelFunc()
	}
	t.statLock.Unlock()
	if !wasTerminal && stat > StatPending && t.onComplete != nil {
		t.onComplete(stat, t.userStopped)
	}
}

func (t *BWSTask) setError(err error) {
	t.statLock.Lock()
	wasTerminal := t.stat > StatPending
	t.stat = StatError
	t.taskErr = err
	t.statLock.Unlock()
	t.cancelFunc()
	if !wasTerminal && t.onComplete != nil {
		t.onComplete(StatError, t.userStopped)
	}
}

func (t *BWSTask) sendLog(level LogLevel, message string) {
	if t.logCh == nil {
		return
	}
	entry := LogEntry{
		TaskID:    t.ID,
		Level:     level,
		Message:   message,
		Timestamp: time.Now(),
	}
	select {
	case t.logCh <- entry:
	default:
	}
}

// ── Execution pipeline ─────────────────────────────────────────────────────

func (t *BWSTask) run(globalOffset time.Duration) {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	if t.GetStat() != StatWaiting {
		return
	}
	adjustedTime := t.TargetTime.Add(globalOffset)
	waitDuration := time.Until(adjustedTime)
	if waitDuration <= 0 {
		go t.executeAndStop()
		return
	}
	t.timer = time.NewTimer(waitDuration)
	timerC := t.timer.C // captured under mutex — no data race
	go t.waitForExecution(timerC)
}

// waitForExecution waits for the timer to fire or the task to be stopped.
// timerC is captured under lock by the caller to avoid data races on t.timer.
func (t *BWSTask) waitForExecution(timerC <-chan time.Time) {
	select {
	case <-timerC:
		t.executeAndStop()
	case <-t.stopChan:
	}
}

func (t *BWSTask) executeAndStop() {
	t.statLock.Lock()
	if t.stat != StatWaiting {
		t.statLock.Unlock()
		return
	}
	t.stat = StatPending
	t.statLock.Unlock()

	t.mutex.Lock()
	if t.timer != nil {
		t.timer.Stop()
		t.timer = nil
	}
	t.mutex.Unlock()

	defer func() {
		if err := recover(); err != nil {
			t.setError(fmt.Errorf("panic: %v", err))
		}
	}()
	t.bwsReservationFunc()
}

// ── BWS reservation logic ──────────────────────────────────────────────────

// bwsReservationFunc is the entry point for the BWS reservation flow.
// It first waits until the precise reservation time, then enters the
// submission loop.
func (t *BWSTask) bwsReservationFunc() {
	// Snapshot config under lock to avoid data races with UpdateInterval/UpdateStartDelay.
	t.mutex.RLock()
	activityTitle := t.config.ActivityTitle
	activityID := t.config.ActivityID
	reserveDate := t.config.ReserveDate
	ticketNo := t.config.TicketNo
	startDelayMs := t.config.StartDelayMs
	loopDelayMs := t.config.LoopDelayMs
	t.mutex.RUnlock()

	t.sendLog(LogInfo, i18n.T("bws.task_started", map[string]interface{}{
		"Activity": activityTitle, "ActivityID": activityID, "Date": reserveDate, "TicketNo": ticketNo}))

	// Step 1: wait until the reservation time (with NTP calibration)
	t.waitForReserveTime(startDelayMs, activityTitle)

	// Step 2: enter the reservation submission loop
	t.startReservationLoop(loopDelayMs, activityID, ticketNo, activityTitle, reserveDate)
}

// ── waitForReserveTime ─────────────────────────────────────────────────────
//
// Waits until the reservation time accounting for startDelayMs.
// Automatically performs NTP/Bilibili clock calibration 5 minutes before
// the reservation time to ensure precise timing.

const (
	calibLogIntervalSec = 3 // print countdown every 3 seconds
	pollInterval        = 100 * time.Millisecond
)

func (t *BWSTask) waitForReserveTime(startDelayMs int, activityTitle string) {
	lastLogTime := int64(0)

	targetTime := t.TargetTime.Add(time.Duration(startDelayMs) * time.Millisecond)

	t.sendLog(LogInfo, i18n.T("bws.waiting_target", map[string]interface{}{
		"Target": targetTime.Format("2006-01-02 15:04:05.000"), "Delay": startDelayMs}))

	for {
		select {
		case <-t.ctx.Done():
			return
		default:
		}

		now := time.Now()
		remaining := targetTime.Sub(now)

		if remaining <= 0 {
			if startDelayMs > 0 {
				t.sendLog(LogInfo, i18n.T("bws.time_reached_delayed", map[string]interface{}{"Delay": startDelayMs}))
			} else if startDelayMs < 0 {
				t.sendLog(LogInfo, i18n.T("bws.time_reached_early", map[string]interface{}{"Early": -startDelayMs}))
			} else {
				t.sendLog(LogInfo, i18n.T("bws.time_reached_start", nil))
			}
			return
		}

		curUnix := now.Unix()

		// Log countdown every calibLogIntervalSec seconds (only when > 5 seconds remaining)
		remainingSec := remaining.Seconds()
		if remainingSec > 5 {
			if curUnix > lastLogTime+calibLogIntervalSec {
				lastLogTime = curUnix
				t.sendLog(LogInfo, i18n.T("bws.waiting_status", map[string]interface{}{
					"Activity": activityTitle, "Remaining": fmt.Sprintf("%.1f", remainingSec), "Delay": startDelayMs}))
			}
		} else if remainingSec <= 5 && lastLogTime < curUnix {
			lastLogTime = curUnix
			t.sendLog(LogInfo, i18n.T("bws.about_to_start", nil))
		}

		// Poll every pollInterval for precision — cancellable via ctx
		select {
		case <-t.ctx.Done():
			return
		case <-time.After(pollInterval):
		}
	}
}

// ── startReservationLoop ───────────────────────────────────────────────────
//
// Continuously sends reservation requests until success, a terminal error,
// or the task is cancelled.

const (
	bwsCodeSuccess      = 0     // reservation successful
	bwsCodeNotOpen      = 75637 // not yet open
	bwsCodeRateLimit    = -702  // too many requests
	bwsCodeNetworkErr   = -1    // network error
	bwsCodeRiskControl  = 412   // risk control triggered
	bwsCodeThrottled    = 429   // rate limited
	bwsCodeFullReserved = 75574 // already reserved or full
)

// bwsRetryDelay is the per-request delay within the reservation loop.
// When zero, requests are sent as fast as possible.
const bwsDefaultRetryDelay = 50 * time.Millisecond

// bwsRiskControlWait is how long to wait when a 412 (risk control) is received.
const bwsRiskControlWait = 180 * time.Second

// bwsThrottleWait is how long to wait when a 429 (rate limited) is received.
const bwsThrottleWait = 500 * time.Millisecond

func (t *BWSTask) startReservationLoop(loopDelayMs int, activityID int, ticketNo, activityTitle, reserveDate string) {
	loopDelay := time.Duration(loopDelayMs) * time.Millisecond
	if loopDelay <= 0 {
		loopDelay = bwsDefaultRetryDelay
	}

	t.sendLog(LogInfo, i18n.T("bws.enter_loop", map[string]interface{}{
		"ActivityID": activityID, "TicketNo": ticketNo, "Interval": loopDelay}))

	for {
		select {
		case <-t.ctx.Done():
			return
		default:
		}

		code, msg, err := t.client.MakeBWSReservation(ticketNo, activityID)

		if err != nil {
			t.sendLog(LogError, i18n.T("bws.network_error", map[string]interface{}{"Error": err.Error()}))
			// Fall through to sleep before retrying.
		} else {
			switch code {
			case bwsCodeSuccess:
				t.sendLog(LogSuccess, i18n.T("bws.success", nil))
				if t.notifyFn != nil {
					t.notifyFn(i18n.T("bws.success_notify", map[string]interface{}{
						"Activity": activityTitle, "Date": reserveDate}))
				}
				t.setStat(StatSuccess)
				return

			case bwsCodeNotOpen:
				t.sendLog(LogInfo, i18n.T("bws.status_not_open", nil))

			case bwsCodeRateLimit:
				t.sendLog(LogWarn, i18n.T("bws.status_rate_limit", nil))

			case bwsCodeNetworkErr:
				t.sendLog(LogError, i18n.T("bws.status_network_error", nil))

			case bwsCodeRiskControl:
				t.sendLog(LogWarn, i18n.T("bws.status_risk_control", map[string]interface{}{"Wait": fmt.Sprintf("%.0f", bwsRiskControlWait.Seconds())}))
				select {
				case <-t.ctx.Done():
					return
				case <-time.After(bwsRiskControlWait):
				}
				continue // don't apply loopDelay after a long wait

			case bwsCodeThrottled:
				t.sendLog(LogWarn, i18n.T("bws.status_throttled", map[string]interface{}{"Wait": fmt.Sprintf("%.0f", bwsThrottleWait.Seconds())}))
				select {
				case <-t.ctx.Done():
					return
				case <-time.After(bwsThrottleWait):
				}
				continue

			case bwsCodeFullReserved:
				t.sendLog(LogInfo, i18n.T("bws.status_full", map[string]interface{}{"Msg": msg}))
				t.setStat(StatFailed)
				return

			default:
				t.sendLog(LogWarn, i18n.T("bws.unknown_response", map[string]interface{}{"Code": code, "Msg": msg}))
			}
		}

		select {
		case <-t.ctx.Done():
			return
		case <-time.After(loopDelay):
		}
	}
}
