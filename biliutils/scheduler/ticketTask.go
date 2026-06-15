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
	startDelay  time.Duration
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

	if tt.GetStat() != StatWaiting {
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
	select {
	case <-tt.stopChan:
		// already closed
	default:
		close(tt.stopChan)
	}
	go tt.executeAndStop()
}

// Stop stops the task.
func (tt *TicketTask) Stop() {
	tt.mutex.Lock()

	stat := tt.GetStat()
	if stat != StatWaiting && stat != StatPending {
		tt.mutex.Unlock()
		return
	}

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

	tt.setStat(StatFailed)
}

// GetStat returns the current task status.
func (tt *TicketTask) GetStat() RunningStat {
	tt.statLock.Lock()
	defer tt.statLock.Unlock()
	return tt.stat
}

func (tt *TicketTask) setStat(stat RunningStat) {
	tt.statLock.Lock()
	wasTerminal := tt.stat > StatPending
	tt.stat = stat
	if stat > StatPending {
		tt.cancelFunc()
	}
	tt.statLock.Unlock()
	if !wasTerminal && stat > StatPending && tt.onComplete != nil {
		tt.onComplete(stat)
	}
}

func (tt *TicketTask) UpdateInterval(newInterval time.Duration) {
	tt.mutex.Lock()
	defer tt.mutex.Unlock()
	tt.sendLog(LogInfo, fmt.Sprintf("更新请求间隔: %v → %v", tt.interval, newInterval))
	tt.interval = newInterval
}

// UpdateStartDelay updates the one-time initial delay applied before the first submit attempt.
func (tt *TicketTask) UpdateStartDelay(newDelay time.Duration) {
	tt.mutex.Lock()
	defer tt.mutex.Unlock()
	tt.sendLog(LogInfo, fmt.Sprintf("更新启动延迟: %v → %v", tt.startDelay, newDelay))
	tt.startDelay = newDelay
}

// GetError returns the task's error.
func (tt *TicketTask) GetError() error {
	tt.statLock.Lock()
	defer tt.statLock.Unlock()
	return tt.taskErr
}

func (tt *TicketTask) setError(err error) {
	tt.statLock.Lock()
	wasTerminal := tt.stat > StatPending
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
	if !tt.timer.Stop() {
		select {
		case <-tt.timer.C:
		default:
		}
	}
	adjustedTime := tt.TargetTime.Add(offsetDelta)
	waitDuration := time.Until(adjustedTime)
	if waitDuration <= 0 {
		// Clean up old goroutine before starting immediate execution.
		select {
		case <-tt.stopChan:
		default:
			close(tt.stopChan)
		}
		tt.stopChan = make(chan struct{})
		go tt.executeAndStop()
		return
	}
	tt.timer = time.NewTimer(waitDuration)
	timerC := tt.timer.C // captured under mutex — no data race
	select {
	case <-tt.stopChan:
	default:
		close(tt.stopChan)
	}
	tt.stopChan = make(chan struct{})
	go tt.waitForExecution(timerC)
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
	timerC := tt.timer.C // captured under mutex — no data race
	go tt.waitForExecution(timerC)
}

// waitForExecution waits for the timer to fire or the task to be stopped.
// timerC is captured under lock by the caller to avoid data races on tt.timer.
func (tt *TicketTask) waitForExecution(timerC <-chan time.Time) {
	select {
	case <-timerC:
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
//  5. Apply one-time start delay (if configured, wait before first submit)
//  6. Prepare buyer info (ordinary: name+tel; real-name: ID from confirmInfo)
//  7. Enter submit loop:
//     → SubmitOrder every interval
//     → Refresh tokens every 61 attempts
//     → On success (OrderId != 0): notify + mark StatSuccess
//     → On code 100034: update price and retry
//     → On code 100017: mark StatFailed (not for sale)
func (tt *TicketTask) ticketFunc() {
	// Snapshot mutable fields under lock to avoid data races with UpdateInterval/UpdateStartDelay.
	tt.mutex.RLock()
	interval := tt.interval
	startDelay := tt.startDelay
	tt.mutex.RUnlock()

	pidString := strconv.FormatInt(tt.ticket.ProjectID, 10)

	tt.sendLog(LogInfo, fmt.Sprintf("任务开始 — 项目:%s 场次:%s 票种:%s 购票人:%s",
		tt.ticket.ProjectName, tt.ticket.ScreenName, tt.ticket.SkuName, tt.ticket.Buyer.String()))

	// 1. Get project info
	projectInfo, err := tt.client.GetProjectInformation(pidString)
	if err != nil {
		tt.sendLog(LogError, fmt.Sprintf("获取项目信息失败: %v", err))
		tt.setError(err)
		return
	}

	tt.sendLog(LogInfo, fmt.Sprintf("项目信息: %s (热门=%v, 实名=%v)", projectInfo.ProjectName, projectInfo.IsHotProject, projectInfo.IsForceRealName))

	// 2. Choose token generator based on project type
	var tokenGen token.Generator
	if projectInfo.IsHotProject {
		ec := token.NewEncodeData(tt.client.GetBrowserUA(), fmt.Sprintf("https://mall.bilibili.com/neul-next/ticket-renovation/detail.html?id=%d&outsideMall=no&outsideMall=no#themeType=2", tt.ticket.ProjectID))
		tokenGen = token.NewCToken2026Generator(ec)
		tt.sendLog(LogInfo, "使用 CToken 生成器 (热门项目)")
	} else {
		tokenGen = token.NewNormalTokenGenerator()
		tt.sendLog(LogInfo, "使用普通 Token 生成器")
	}

	// 3. Get all ticket SKU/screen combos and match target
	allTickets, err := tt.client.GetTicketSkuIDsByProjectID(pidString)
	if err != nil {
		tt.sendLog(LogError, fmt.Sprintf("获取票种列表失败: %v", err))
		tt.setError(err)
		return
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
		tt.setError(definedErrors.NewBilibiliMallTicketNotfoundError(tt.ticket.SkuID, tt.ticket.ProjectID, tt.ticket.ScreenID))
		return
	}

	tt.sendLog(LogInfo, fmt.Sprintf("目标票种: %s — %s (¥%.2f)", targetSku.Name, targetSku.Desc, float64(targetSku.Price)/100.0))

	// 4. Obtain request token and ptoken
	whenGenPtoken := time.Now()
	orderTokens, err := tt.client.GetRequestTokenAndPToken(tokenGen, pidString, *targetSku)
	if err != nil {
		tt.sendLog(LogError, fmt.Sprintf("获取下单 Token 失败: %v", err))
		tt.setError(err)
		return
	}

	tt.sendLog(LogInfo, "下单 Token 已获取")

	// 5. Apply one-time start delay before the first submit attempt
	if startDelay > 0 {
		tt.sendLog(LogInfo, fmt.Sprintf("启动延时 %v...", startDelay))
		select {
		case <-tt.ctx.Done():
			tt.sendLog(LogInfo, "任务在启动延时期间被取消")
			return
		case <-time.After(startDelay):
		}
	}

	// 6. Prepare buyer info based on buyer type
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
			tt.setError(err)
			return
		}
		for _, buyer := range confirmInfo.BuyerList.List {
			if buyer.Id == tt.ticket.Buyer.ID {
				buyerData = []map[string]any{{
					"id":                  buyer.Id,
					"uid":                 buyer.Uid,
					"accountId":           buyer.AccountId,
					"name":                buyer.Name,
					"tel":                 buyer.Tel,
					"account_channel":     buyer.AccountChannel,
					"personal_id":         buyer.PersonalId,
					"id_card_front":       buyer.IdCardFront,
					"id_card_back":        buyer.IdCardBack,
					"is_default":          buyer.IsDefault,
					"id_type":             buyer.IdType,
					"verify_status":       buyer.VerifyStatus,
					"isBuyerInfoVerified": buyer.IsBuyerInfoVerified,
					"isBuyerValid":        buyer.IsBuyerValid,
				}}
				tt.sendLog(LogInfo, fmt.Sprintf("购票人(实名): %s (ID:%d)", buyer.Name, buyer.Id))
			}
		}
		if buyerData == nil {
			tt.sendLog(LogError, fmt.Sprintf("未匹配到实名购票人 ID:%d", tt.ticket.Buyer.ID))
			tt.setError(definedErrors.NewBilibiliMallBuyerNotfoundError(tt.ticket.Buyer))
			return
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
		}

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
		} else {
			var (
				err         error
				code        int
				msg         string
				orderResult api.TicketOrderStruct
			)

			err, code, msg, orderResult = tt.client.SubmitOrder(tokenGen, whenGenPtoken, orderTokens, pidString, *targetSku, buyerData, tt.ticket.Buyer.BuyerType)
			if err != nil {
				tt.sendLog(LogWarn, fmt.Sprintf("提交订单失败: %v", err))
			} else {

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
			}
		}

		submitCount++
		// Cancellable sleep — respects context cancellation from Stop()
		select {
		case <-tt.ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}
