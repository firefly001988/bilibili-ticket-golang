package scheduler

import (
	"bilibili-ticket-golang/biliutils"
	"bilibili-ticket-golang/biliutils/token"
	"bilibili-ticket-golang/global"
	"bilibili-ticket-golang/models/bili/api"
	r "bilibili-ticket-golang/models/bili/response"
	definedErrors "bilibili-ticket-golang/models/errors"
	"bilibili-ticket-golang/store/configuration"
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"
)

// TicketTask represents a single ticket-grabbing task.
type TicketTask struct {
	ID         string
	TargetTime time.Time

	timer       *time.Timer
	stopChan    chan struct{}
	mutex       sync.RWMutex
	statLock    sync.Mutex
	executeLock sync.Mutex
	taskErr     error
	client      *biliutils.BiliClient
	ticket      configuration.TicketEntry
	notifyFn    func(message string)
	logCh       chan<- LogEntry
	stat        RunningStat
	username    string
	userid      int64
	interval    time.Duration
	ctx         context.Context
	cancelFunc  context.CancelFunc
	onComplete  func(stat RunningStat) // called when task terminates (persist hook)
}

// NewTicketTask creates a new TicketTask.
// onComplete is called with the final RunningStat when the task terminates (may be nil).
func NewTicketTask(client *biliutils.BiliClient, ticket configuration.TicketEntry, notifyFn func(string), interval time.Duration, logCh chan<- LogEntry, onComplete func(RunningStat)) (*TicketTask, error) {
	if !ticket.Valid() {
		return nil, definedErrors.NewRoutineCreateError("ticket data is invalid")
	}
	if client == nil {
		return nil, definedErrors.NewRoutineCreateError("bili-client is nil")
	}
	info, err := client.GetAccountStatus()
	if err != nil {
		return nil, errors.Join(definedErrors.NewRoutineCreateError("get login status error"), err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	return &TicketTask{
		client:     client,
		ticket:     ticket,
		notifyFn:   notifyFn,
		logCh:      logCh,
		stat:       StatWaiting,
		username:   info.Name,
		userid:     info.UID,
		interval:   interval,
		ctx:        ctx,
		cancelFunc: cancel,
		stopChan:   make(chan struct{}),
		onComplete: onComplete,
	}, nil
}

// GetID returns the task's unique identifier.
func (tt *TicketTask) GetID() string {
	return tt.ID
}

// GetTargetTime returns the target execution time.
func (tt *TicketTask) GetTargetTime() time.Time {
	return tt.TargetTime
}

// Start begins waiting for the target time, applying the global offset.
func (tt *TicketTask) Start(globalOffset time.Duration) {
	tt.mutex.Lock()
	defer tt.mutex.Unlock()

	if tt.GetStat() == StatPending {
		return
	}
	go tt.run(globalOffset)
}

// ForceStart immediately executes the task, skipping the timer.
func (tt *TicketTask) ForceStart() {
	tt.mutex.Lock()
	defer tt.mutex.Unlock()

	if tt.timer != nil && !tt.timer.Stop() {
		select {
		case <-tt.timer.C:
		default:
		}
	}
	if tt.GetStat() != StatWaiting {
		return
	}
	go tt.executeAndStop()
}

// Stop stops the task.
func (tt *TicketTask) Stop() {
	tt.mutex.Lock()

	stat := tt.getStatNoLock()
	if stat != StatWaiting && stat != StatPending {
		tt.mutex.Unlock()
		return
	}

	// Signal the timer/execution goroutines to stop
	select {
	case <-tt.stopChan:
		// already closed
	default:
		close(tt.stopChan)
	}

	if tt.timer != nil {
		tt.timer.Stop()
	}

	tt.mutex.Unlock()

	// Cancel the context (interrupts time.Sleep in the main loop via ctx.Done)
	tt.cancelFunc()

	// Set terminal state (must NOT hold mutex during setStat to avoid deadlock with executeAndStop)
	tt.setStatNoLock(StatFailed)
}

// GetStat returns the current task status.
func (tt *TicketTask) GetStat() RunningStat {
	tt.statLock.Lock()
	defer tt.statLock.Unlock()
	return tt.stat
}

func (tt *TicketTask) setStat(stat RunningStat) {
	tt.statLock.Lock()
	wasTerminal := tt.stat > 1
	tt.stat = stat
	if stat > 2 {
		tt.cancelFunc()
	}
	tt.statLock.Unlock()
	// Fire completion callback on first transition to terminal state
	if !wasTerminal && stat > 1 && tt.onComplete != nil {
		tt.onComplete(stat)
	}
}

// getStatNoLock returns the current stat without acquiring the lock.
// Caller must hold tt.statLock.
func (tt *TicketTask) getStatNoLock() RunningStat {
	return tt.stat
}

// setStatNoLock sets the stat without acquiring the lock.
// Caller must hold tt.statLock.
func (tt *TicketTask) setStatNoLock(stat RunningStat) {
	tt.statLock.Lock()
	wasTerminal := tt.stat > 1
	tt.stat = stat
	if stat > 2 {
		tt.cancelFunc()
	}
	tt.statLock.Unlock()
	if !wasTerminal && stat > 1 && tt.onComplete != nil {
		tt.onComplete(stat)
	}
}

// GetError returns the task's error.
func (tt *TicketTask) GetError() error {
	tt.statLock.Lock()
	defer tt.statLock.Unlock()
	return tt.taskErr
}

func (tt *TicketTask) setError(err error) {
	tt.statLock.Lock()
	wasTerminal := tt.stat > 1
	tt.stat = StatError
	tt.taskErr = err
	tt.statLock.Unlock()
	tt.cancelFunc()
	if !wasTerminal && tt.onComplete != nil {
		tt.onComplete(StatError)
	}
}

// sendLog sends a log entry to the log broker in a non-blocking manner.
func (tt *TicketTask) sendLog(level LogLevel, message string) {
	if tt.logCh == nil {
		return
	}
	entry := LogEntry{
		TaskID:    tt.ID,
		Level:     level,
		Message:   message,
		Timestamp: time.Now(),
	}
	select {
	case tt.logCh <- entry:
	default:
		// drop if channel is full to avoid blocking the ticket loop
	}
}

func (tt *TicketTask) rescheduleWithNewOffset(offsetDelta time.Duration) {
	tt.mutex.Lock()
	defer tt.mutex.Unlock()

	if tt.GetStat() != StatWaiting || tt.timer == nil {
		return
	}
	if tt.timer != nil && !tt.timer.Stop() {
		select {
		case <-tt.timer.C:
		default:
		}
	}
	adjustedTime := tt.TargetTime.Add(offsetDelta)
	waitDuration := time.Until(adjustedTime)
	if waitDuration <= 0 {
		go tt.executeAndStop()
		return
	}
	tt.timer = time.NewTimer(waitDuration)
	go tt.waitForExecution()
}

func (tt *TicketTask) run(globalOffset time.Duration) {
	tt.mutex.Lock()
	defer tt.mutex.Unlock()

	if tt.GetStat() != StatWaiting {
		return
	}

	adjustedTime := tt.TargetTime.Add(globalOffset)
	waitDuration := time.Until(adjustedTime)
	if waitDuration <= 0 {
		go tt.executeAndStop()
		return
	}
	tt.timer = time.NewTimer(waitDuration)
	go tt.waitForExecution()
}

func (tt *TicketTask) waitForExecution() {
	select {
	case <-tt.timer.C:
		tt.executeAndStop()
	case <-tt.stopChan:
	}
}

func (tt *TicketTask) executeAndStop() {
	// Mark as pending first (use statLock to avoid deadlock with Stop)
	tt.statLock.Lock()
	if tt.stat != StatWaiting {
		tt.statLock.Unlock()
		return
	}
	tt.stat = StatPending
	tt.statLock.Unlock()

	// Release the timer mutex before executing — ticketFunc may run for minutes
	// and Stop() needs to be able to cancel without waiting on this goroutine.
	tt.mutex.Lock()
	if tt.timer != nil {
		tt.timer.Stop()
		tt.timer = nil
	}
	tt.mutex.Unlock()

	defer func() {
		if err := recover(); err != nil {
			tt.setError(fmt.Errorf("%v", err))
		}
	}()
	tt.ticketFunc()
}

// ticketFunc is the core ticket-grabbing loop.
//
// Flow:
//  1. Get project info → determine hot/normal + real-name requirements
//  2. Choose token generator (CTokenGenerator for hot projects)
//  3. Fetch all SKU/screen pairs, match the target
//  4. Obtain RequestToken/PToken via prepare endpoint
//  5. Prepare buyer info (ordinary: name+tel; real-name: ID from confirmInfo)
//  6. Enter submit loop:
//     → SubmitOrder every interval
//     → Refresh tokens every 61 attempts
//     → On success (OrderId != 0): notify + mark StatSuccess
//     → On code 100034: update price and retry
//     → On code 100017: mark StatFailed (not for sale)
func (tt *TicketTask) ticketFunc() {
	pidString := strconv.FormatInt(tt.ticket.ProjectID, 10)

	tt.sendLog(LogInfo, fmt.Sprintf("任务开始 — 项目:%s 场次:%s 票种:%s 购票人:%s",
		tt.ticket.ProjectName, tt.ticket.ScreenName, tt.ticket.SkuName, tt.ticket.Buyer.String()))

	// 1. Get project info
	projectInfo, err := tt.client.GetProjectInformation(pidString)
	if err != nil {
		tt.sendLog(LogError, fmt.Sprintf("获取项目信息失败: %v", err))
		panic(err)
	}

	tt.sendLog(LogInfo, fmt.Sprintf("项目信息: %s (热门=%v, 实名=%v)", projectInfo.ProjectName, projectInfo.IsHotProject, projectInfo.IsForceRealName))

	// 2. Choose token generator based on project type
	var tokenGen token.Generator
	if projectInfo.IsHotProject {
		tokenGen = token.NewCTokenGenerator()
		tt.sendLog(LogInfo, "使用 CToken 生成器 (热门项目)")
	} else {
		tokenGen = token.NewNormalTokenGenerator()
		tt.sendLog(LogInfo, "使用普通 Token 生成器")
	}

	// 3. Get all ticket SKU/screen combos and match target
	allTickets, err := tt.client.GetTicketSkuIDsByProjectID(pidString)
	if err != nil {
		tt.sendLog(LogError, fmt.Sprintf("获取票种列表失败: %v", err))
		panic(err)
	}

	var targetSku *r.TicketSkuScreenID
	for _, ticketItem := range allTickets {
		if ticketItem.SkuID == tt.ticket.SkuID && ticketItem.ScreenID == tt.ticket.ScreenID {
			targetSku = &ticketItem
			break
		}
	}
	if targetSku == nil {
		tt.sendLog(LogError, fmt.Sprintf("未找到目标票种 (SKU:%d Screen:%d)", tt.ticket.SkuID, tt.ticket.ScreenID))
		panic(definedErrors.NewBilibiliMallTicketNotfoundError(tt.ticket.SkuID, tt.ticket.ProjectID, tt.ticket.ScreenID))
	}

	tt.sendLog(LogInfo, fmt.Sprintf("目标票种: %s — %s (¥%.2f)", targetSku.Name, targetSku.Desc, float64(targetSku.Price)/100.0))

	// 4. Obtain request token and ptoken
	whenGenPtoken := time.Now()
	orderTokens, err := tt.client.GetRequestTokenAndPToken(tokenGen, pidString, *targetSku)
	if err != nil {
		tt.sendLog(LogError, fmt.Sprintf("获取下单 Token 失败: %v", err))
		panic(err)
	}

	tt.sendLog(LogInfo, "下单 Token 已获取")

	// 5. Prepare buyer info based on buyer type
	var buyerData interface{}
	switch tt.ticket.Buyer.BuyerType {
	case r.Ordinary:
		buyerData = map[string]string{
			"tel":  tt.ticket.Buyer.Tel,
			"name": tt.ticket.Buyer.Name,
		}
		tt.sendLog(LogInfo, fmt.Sprintf("购票人(普通): %s %s", tt.ticket.Buyer.Name, tt.ticket.Buyer.Tel))
	case r.ForceRealName:
		confirmInfo, err := tt.client.GetConfirmInformation(orderTokens, pidString)
		if err != nil {
			tt.sendLog(LogError, fmt.Sprintf("获取实名信息失败: %v", err))
			panic(err)
		}
		for _, buyer := range confirmInfo.BuyerList.List {
			if buyer.Id == tt.ticket.Buyer.ID {
				buyerData = []map[string]any{{
					"id":          buyer.Id,
					"name":        buyer.Name,
					"tel":         buyer.Tel,
					"personal_id": buyer.PersonalId,
					"id_type":     buyer.IdType,
				}}
				tt.sendLog(LogInfo, fmt.Sprintf("购票人(实名): %s (ID:%d)", buyer.Name, buyer.Id))
			}
		}
		if buyerData == nil {
			tt.sendLog(LogError, fmt.Sprintf("未匹配到实名购票人 ID:%d", tt.ticket.Buyer.ID))
			panic(definedErrors.NewBilibiliMallBuyerNotfoundError(tt.ticket.Buyer))
		}
	}

	// 6. Main submission loop
	tt.sendLog(LogInfo, "进入提交循环...")
	var submitCount uint16 = 0
	for {
		select {
		case <-tt.ctx.Done():
			tt.sendLog(LogInfo, "任务被取消")
			return
		default:
			var (
				err         error
				code        int
				msg         string
				orderResult api.TicketOrderStruct
			)

			// Refresh token every N attempts to avoid rate limiting
			if submitCount >= global.MaxTokenRefreshCount {
				whenGenPtoken = time.Now()
				orderTokens, err = tt.client.GetRequestTokenAndPToken(tokenGen, pidString, *targetSku)
				if err != nil {
					tt.sendLog(LogWarn, fmt.Sprintf("刷新 Token 失败: %v", err))
				} else {
					tt.sendLog(LogDebug, "Token 已刷新")
				}
				submitCount = 0
				goto SLEEP
			}

			err, code, msg, orderResult = tt.client.SubmitOrder(tokenGen, whenGenPtoken, orderTokens, pidString, *targetSku, buyerData, tt.ticket.Buyer.BuyerType)
			if err != nil {
				tt.sendLog(LogWarn, fmt.Sprintf("提交订单失败: %v", err))
				goto SLEEP
			}

			// Success: OrderId is non-zero and code is 0, 100048, or 100079
			if (code == 0 || code == 100048 || code == 100079) && orderResult.OrderId != 0 {
				successMsg := fmt.Sprintf("🎉 抢票成功！订单号: %d", orderResult.OrderId)
				if tt.notifyFn != nil {
					tt.notifyFn(fmt.Sprintf("抢票成功！\n项目：%s\n场次：%s\n票种：%s\n购票人：%s\n购票用户：%s(%d)",
						tt.ticket.ProjectName, tt.ticket.ScreenName, tt.ticket.SkuName,
						tt.ticket.Buyer.String(), tt.username, tt.userid))
				}
				tt.sendLog(LogSuccess, successMsg)
				tt.setStat(StatSuccess)
				return
			}

			// Handle specific error codes
			switch code {
			case 100034:
				oldPrice := targetSku.Price
				targetSku.Price = orderResult.PayMoney
				tt.sendLog(LogInfo, fmt.Sprintf("价格变更: ¥%.2f → ¥%.2f", float64(oldPrice)/100.0, float64(targetSku.Price)/100.0))
			case 100017:
				tt.sendLog(LogError, fmt.Sprintf("不可售 — %s (%d)", msg, code))
				tt.setStat(StatFailed)
				return
			}

			tt.sendLog(LogDebug, fmt.Sprintf("#%d: %s (%d)", submitCount+1, msg, code))
		SLEEP:
			submitCount++
			// Cancellable sleep — respects context cancellation from Stop()
			select {
			case <-tt.ctx.Done():
				return
			case <-time.After(tt.interval):
			}
		}
	}
}
