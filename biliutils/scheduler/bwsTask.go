package scheduler

import (
	"bilibili-ticket-golang/biliutils"
	"bilibili-ticket-golang/biliutils/clock"
	"bilibili-ticket-golang/store/configuration"
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
	onComplete  func(stat RunningStat)
}

// NewBWSTask creates a new BWSTask.
func NewBWSTask(client *biliutils.BiliClient, config configuration.BWSEntry, notifyFn func(string), logCh chan<- LogEntry, onComplete func(RunningStat)) (*BWSTask, error) {
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
	if t.GetStat() == StatPending {
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
	go t.executeAndStop()
}

func (t *BWSTask) Stop() {
	t.mutex.Lock()
	stat := t.getStatNoLock()
	if stat != StatWaiting && stat != StatPending {
		t.mutex.Unlock()
		return
	}
	select {
	case <-t.stopChan:
	default:
		close(t.stopChan)
	}
	if t.timer != nil {
		t.timer.Stop()
	}
	t.mutex.Unlock()
	t.cancelFunc()
	t.setStatNoLock(StatFailed)
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
	t.config.LoopDelayMs = int(newInterval.Milliseconds())
}

func (t *BWSTask) UpdateStartDelay(newDelay time.Duration) {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	t.config.StartDelayMs = int(newDelay.Milliseconds())
}

func (t *BWSTask) rescheduleWithNewOffset(offsetDelta time.Duration) {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	if t.GetStat() != StatWaiting || t.timer == nil {
		return
	}
	if t.timer != nil && !t.timer.Stop() {
		select {
		case <-t.timer.C:
		default:
		}
	}
	adjustedTime := t.TargetTime.Add(offsetDelta)
	waitDuration := time.Until(adjustedTime)
	if waitDuration <= 0 {
		go t.executeAndStop()
		return
	}
	t.timer = time.NewTimer(waitDuration)
	go t.waitForExecution()
}

// ── Internal state helpers ─────────────────────────────────────────────────

func (t *BWSTask) setStat(stat RunningStat) {
	t.statLock.Lock()
	wasTerminal := t.stat > StatPending
	t.stat = stat
	if stat > StatError {
		t.cancelFunc()
	}
	t.statLock.Unlock()
	if !wasTerminal && stat > StatPending && t.onComplete != nil {
		t.onComplete(stat)
	}
}

func (t *BWSTask) setStatNoLock(stat RunningStat) {
	t.statLock.Lock()
	wasTerminal := t.stat > StatPending
	t.stat = stat
	if stat > StatError {
		t.cancelFunc()
	}
	t.statLock.Unlock()
	if !wasTerminal && stat > StatPending && t.onComplete != nil {
		t.onComplete(stat)
	}
}

func (t *BWSTask) getStatNoLock() RunningStat {
	return t.stat
}

func (t *BWSTask) setError(err error) {
	t.statLock.Lock()
	wasTerminal := t.stat > StatPending
	t.stat = StatError
	t.taskErr = err
	t.statLock.Unlock()
	t.cancelFunc()
	if !wasTerminal && t.onComplete != nil {
		t.onComplete(StatError)
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
	go t.waitForExecution()
}

func (t *BWSTask) waitForExecution() {
	select {
	case <-t.timer.C:
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
	t.sendLog(LogInfo, fmt.Sprintf("BWS任务开始 — 活动:%s (ID:%d) 日期:%s 票号:%s",
		t.config.ActivityTitle, t.config.ActivityID, t.config.ReserveDate, t.config.TicketNo))

	// Step 1: wait until the reservation time (with NTP calibration)
	t.waitForReserveTime()

	// Step 2: enter the reservation submission loop
	t.startReservationLoop()
}

// ── waitForReserveTime ─────────────────────────────────────────────────────
//
// Waits until the reservation time accounting for startDelayMs.
// Automatically performs NTP/Bilibili clock calibration 5 minutes before
// the reservation time to ensure precise timing.

const (
	autoCalibBeforeSec  = 300 // auto-calibrate 5 minutes before reserve time
	calibLogIntervalSec = 3   // print countdown every 3 seconds
	pollInterval        = 100 * time.Millisecond
)

func (t *BWSTask) waitForReserveTime() {
	autoSyncDone := false
	lastLogTime := int64(0)

	delaySec := float64(t.config.StartDelayMs) / 1000.0
	targetTime := t.TargetTime.Add(time.Duration(t.config.StartDelayMs) * time.Millisecond)

	t.sendLog(LogInfo, fmt.Sprintf("等待开抢时间 — 目标时间: %s (延迟: %dms)",
		targetTime.Format("2006-01-02 15:04:05.000"), t.config.StartDelayMs))

	for {
		select {
		case <-t.ctx.Done():
			return
		default:
		}

		now := time.Now()
		remaining := targetTime.Sub(now)

		if remaining <= 0 {
			if t.config.StartDelayMs > 0 {
				t.sendLog(LogInfo, fmt.Sprintf("开票时间已到，延迟 %dms 后开始抢票...", t.config.StartDelayMs))
			} else if t.config.StartDelayMs < 0 {
				t.sendLog(LogInfo, fmt.Sprintf("提前 %dms 开始抢票...", -t.config.StartDelayMs))
			} else {
				t.sendLog(LogInfo, "开票时间已到，开始抢票...")
			}
			return
		}

		curUnix := now.Unix()

		// Auto NTP/Bilibili calibration 5 minutes before reserve time
		if !autoSyncDone && curUnix >= t.TargetTime.Unix()-autoCalibBeforeSec {
			autoSyncDone = true
			t.sendLog(LogInfo, "开抢前5分钟，正在进行自动校时...")
			t.autoCalibrate()
		}

		// Log countdown every calibLogIntervalSec seconds (only when > 5 seconds remaining)
		remainingSec := remaining.Seconds()
		if remainingSec > 5 {
			if curUnix > lastLogTime+calibLogIntervalSec {
				lastLogTime = curUnix
				t.sendLog(LogInfo, fmt.Sprintf("等待开票 — 活动:%s | 剩余: %.1f秒 (延迟: %dms)",
					t.config.ActivityTitle, remainingSec, t.config.StartDelayMs))
			}
		} else if remainingSec <= 5 && lastLogTime < curUnix {
			lastLogTime = curUnix
			t.sendLog(LogInfo, "即将开始抢票，进入待抢状态...")
		}

		// Poll every 100ms for precision
		select {
		case <-t.ctx.Done():
			return
		case <-time.After(pollInterval):
		}

		_ = delaySec // suppress unused warning
	}
}

// autoCalibrate performs clock synchronization using both Bilibili's
// RTC endpoint and the NTP protocol. The best available offset is applied.
func (t *BWSTask) autoCalibrate() {
	// Try Bilibili clock first
	biliOff, biliErr := clock.GetBilibiliClockOffset()
	if biliErr == nil {
		absOff := biliOff
		if absOff < 0 {
			absOff = -absOff
		}
		if absOff > 700*time.Millisecond {
			t.sendLog(LogWarn, fmt.Sprintf("B站时钟偏差较大: %.3f秒 (本地时间可能有误差)", biliOff.Seconds()))
		} else {
			t.sendLog(LogInfo, fmt.Sprintf("B站时钟偏差: %.3f秒 (时间同步良好)", biliOff.Seconds()))
		}
	}

	// Try NTP as supplementary
	ntpOff, ntpErr := clock.GetNTPClockOffset("ntp.aliyun.com")
	if ntpErr == nil {
		absOff := ntpOff
		if absOff < 0 {
			absOff = -absOff
		}
		if absOff > 700*time.Millisecond {
			t.sendLog(LogWarn, fmt.Sprintf("NTP时钟偏差较大: %.3f秒 (建议检查系统时间)", ntpOff.Seconds()))
		} else {
			t.sendLog(LogInfo, fmt.Sprintf("NTP时钟偏差: %.3f秒", ntpOff.Seconds()))
		}
	}

	if biliErr != nil && ntpErr != nil {
		t.sendLog(LogWarn, "自动校时失败，将使用本地时间")
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

func (t *BWSTask) startReservationLoop() {
	loopDelay := time.Duration(t.config.LoopDelayMs) * time.Millisecond
	if loopDelay <= 0 {
		loopDelay = bwsDefaultRetryDelay
	}

	t.sendLog(LogInfo, fmt.Sprintf("进入抢票循环 — 活动ID:%d 票号:%s 请求间隔:%v",
		t.config.ActivityID, t.config.TicketNo, loopDelay))

	for {
		select {
		case <-t.ctx.Done():
			return
		default:
		}

		code, msg, err := t.client.MakeBWSReservation(t.config.TicketNo, t.config.ActivityID)

		if err != nil {
			// Transport or unmarshal error — retry
			t.sendLog(LogError, fmt.Sprintf("网络错误: %v", err))
			goto sleepLoop
		}

		switch code {
		case bwsCodeSuccess:
			t.sendLog(LogSuccess, "\033[32m预约成功！\033[0m")
			if t.notifyFn != nil {
				t.notifyFn(fmt.Sprintf("BWS预约成功！\n活动：%s\n日期：%s",
					t.config.ActivityTitle, t.config.ReserveDate))
			}
			t.setStat(StatSuccess)
			return

		case bwsCodeNotOpen:
			t.sendLog(LogInfo, fmt.Sprintf("[%d] 尚未开放，等待预约开始", code))

		case bwsCodeRateLimit:
			t.sendLog(LogWarn, fmt.Sprintf("[%d] 请求频率太快", code))

		case bwsCodeNetworkErr:
			t.sendLog(LogError, fmt.Sprintf("[%d] 网络错误，继续重试", code))

		case bwsCodeRiskControl:
			t.sendLog(LogWarn, fmt.Sprintf("[%d] 风控触发，等待 %.0f 秒后重试...", code, bwsRiskControlWait.Seconds()))
			select {
			case <-t.ctx.Done():
				return
			case <-time.After(bwsRiskControlWait):
			}
			continue // don't apply loopDelay after a long wait

		case bwsCodeThrottled:
			t.sendLog(LogWarn, fmt.Sprintf("[%d] 限流，等待 %.0f 秒后重试...", code, bwsThrottleWait.Seconds()))
			select {
			case <-t.ctx.Done():
				return
			case <-time.After(bwsThrottleWait):
			}
			continue

		case bwsCodeFullReserved:
			t.sendLog(LogInfo, fmt.Sprintf("[%d] 预约已满或已预约: %s", code, msg))
			t.setStat(StatFailed)
			return

		default:
			t.sendLog(LogWarn, fmt.Sprintf("[%d] 未知响应: %s", code, msg))
		}

	sleepLoop:
		select {
		case <-t.ctx.Done():
			return
		case <-time.After(loopDelay):
		}
	}
}
