package cluster_service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"bilibili-ticket-golang/cluster/domain"
)

const bwsMetaFile = "data/bws/meta.json"
const bwsLogFile = "data/bws/debug.log"

// bwsLog writes a debug message to stderr and appends it to the BWS log file.
func bwsLog(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintln(os.Stderr, msg)
	f, err := os.OpenFile(bwsLogFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err == nil {
		fmt.Fprintln(f, time.Now().Format("15:04:05.000"), msg)
		f.Close()
	}
}

// ── BWS (Bilibili World) reservation types ──────────────────────

// BWSActivityInfo is a frontend-friendly activity entry returned by
// GetBWSReservationInfo.
type BWSActivityInfo struct {
	ReserveID        int    `json:"reserveId"`
	ActTitle         string `json:"actTitle"`
	ReserveBeginTime int64  `json:"reserveBeginTime"`
	ActBeginTime     int64  `json:"actBeginTime"`
	State            int    `json:"state"`
	DescribeInfo     string `json:"describeInfo"`
	ReserveDate      string `json:"reserveDate"`
}

// BWSTicketInfoItem is a frontend-friendly ticket info entry.
type BWSTicketInfoItem struct {
	Date       string `json:"date"`
	Ticket     string `json:"ticket"`
	ScreenName string `json:"screenName"`
	SkuName    string `json:"skuName"`
}

// BWSReservationResult is returned by GetBWSReservationInfo.
type BWSReservationResult struct {
	Activities  []BWSActivityInfo   `json:"activities"`
	TicketInfos []BWSTicketInfoItem `json:"ticketInfos"`
	ReservedIDs []int               `json:"reservedIds"`
}

// BWSSubmitInput is the JSON input for SubmitBWS.
type BWSSubmitInput struct {
	AccountID     string `json:"accountId"`
	WorkerID      string `json:"workerId"`
	ActivityID    int    `json:"activityId"`
	TicketNo      string `json:"ticketNo"`
	ActivityTitle string `json:"activityTitle"`
	ReserveTime   int64  `json:"reserveTime"`
	ReserveDate   string `json:"reserveDate"`
	StartDelayMs  int    `json:"startDelayMs"`
	LoopDelayMs   int    `json:"loopDelayMs"`
}

// BWSQueryInput is the JSON input for CheckBWSBind and GetBWSReservationInfo.
type BWSQueryInput struct {
	AccountID    string `json:"accountId"`
	WorkerID     string `json:"workerId"`
	ReserveDates string `json:"reserveDates,omitempty"`
	ReserveType  int    `json:"reserveType,omitempty"` // 1=activities, 2=goods
}

// ── BWS methods ──────────────────────────────────────────────────

// CheckBWSBind checks whether the given account has its BWS electronic
// ticket bound to a real-name identity. Accepts a JSON document with
// accountId and workerId.
func (s *ClusterService) CheckBWSBind(inputJSON string) (bool, error) {
	var input BWSQueryInput
	if err := json.Unmarshal([]byte(inputJSON), &input); err != nil {
		return false, fmt.Errorf("invalid input: %w", err)
	}
	ctx := context.Background()
	account, err := s.repository.Account(ctx, input.AccountID)
	if err != nil {
		return false, fmt.Errorf("account %s not found: %w", input.AccountID, err)
	}
	node, err := s.repository.Worker(ctx, input.WorkerID)
	if err != nil {
		return false, fmt.Errorf("worker %s not found: %w", input.WorkerID, err)
	}
	isBind, refreshed, err := s.client.CheckBWSBind(ctx, node, account.Credentials)
	if err != nil {
		return false, err
	}
	// Persist refreshed credentials
	if refreshed.Version > account.Credentials.Version {
		oldVer := account.Credentials.Version
		account.Credentials = refreshed
		_ = s.repository.PutAccount(ctx, account, &oldVer)
	}
	return isBind, nil
}

// GetBWSReservationInfo fetches all BWS activity information for the
// given dates. Accepts a JSON document with accountId, workerId, and
// reserveDates (comma-separated).
func (s *ClusterService) GetBWSReservationInfo(inputJSON string) (*BWSReservationResult, error) {
	var input BWSQueryInput
	if err := json.Unmarshal([]byte(inputJSON), &input); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}
	ctx := context.Background()
	account, err := s.repository.Account(ctx, input.AccountID)
	if err != nil {
		return nil, fmt.Errorf("account %s not found: %w", input.AccountID, err)
	}
	node, err := s.repository.Worker(ctx, input.WorkerID)
	if err != nil {
		return nil, fmt.Errorf("worker %s not found: %w", input.WorkerID, err)
	}
	activities, ticketInfos, reservedIDs, refreshed, err := s.client.GetBWSReservationInfo(ctx, node, account.Credentials, input.ReserveDates, input.ReserveType)
	if err != nil {
		return nil, err
	}
	// Persist refreshed credentials
	if refreshed.Version > account.Credentials.Version {
		oldVer := account.Credentials.Version
		account.Credentials = refreshed
		_ = s.repository.PutAccount(ctx, account, &oldVer)
	}

	result := &BWSReservationResult{
		Activities:  make([]BWSActivityInfo, 0, len(activities)),
		TicketInfos: make([]BWSTicketInfoItem, 0, len(ticketInfos)),
		ReservedIDs: make([]int, 0, len(reservedIDs)),
	}

	for _, a := range activities {
		result.Activities = append(result.Activities, BWSActivityInfo{
			ReserveID:        int(a.ReserveId),
			ActTitle:         a.ActTitle,
			ReserveBeginTime: a.ReserveBeginTime,
			ActBeginTime:     a.ActBeginTime,
			State:            int(a.State),
			DescribeInfo:     a.DescribeInfo,
			ReserveDate:      a.ReserveDate,
		})
	}

	for _, ti := range ticketInfos {
		result.TicketInfos = append(result.TicketInfos, BWSTicketInfoItem{
			Date:       ti.Date,
			Ticket:     ti.Ticket,
			ScreenName: ti.ScreenName,
			SkuName:    ti.SkuName,
		})
	}

	for id := range reservedIDs {
		result.ReservedIDs = append(result.ReservedIDs, int(id))
	}

	return result, nil
}

// BWSBindInput is the JSON input for BindBWSTicket.
type BWSBindInput struct {
	AccountID  string `json:"accountId"`
	WorkerID   string `json:"workerId"`
	Bid        int    `json:"bid"`
	IdType     int    `json:"idType"`
	PersonalID string `json:"personalId"`
	TicketNo   string `json:"ticketNo"`
	UserName   string `json:"userName"`
}

// BindBWSTicket binds a real-name identity to a BWS electronic ticket.
// Accepts a JSON document with the fields defined in BWSBindInput.
func (s *ClusterService) BindBWSTicket(inputJSON string) (int, string, error) {
	var input BWSBindInput
	if err := json.Unmarshal([]byte(inputJSON), &input); err != nil {
		return -1, "", fmt.Errorf("invalid bind input: %w", err)
	}
	ctx := context.Background()
	node, err := s.repository.Worker(ctx, input.WorkerID)
	if err != nil {
		return -1, "", fmt.Errorf("worker %s not found: %w", input.WorkerID, err)
	}
	account, err := s.repository.Account(ctx, input.AccountID)
	if err != nil {
		return -1, "", fmt.Errorf("account %s not found: %w", input.AccountID, err)
	}
	code, message, refreshed, err := s.client.BindBWSTicket(ctx, node, account.Credentials,
		input.Bid, input.IdType, input.PersonalID, input.TicketNo, input.UserName)
	if err != nil {
		return -1, "", err
	}
	if refreshed.Version > account.Credentials.Version {
		oldVer := account.Credentials.Version
		account.Credentials = refreshed
		_ = s.repository.PutAccount(ctx, account, &oldVer)
	}
	return code, message, nil
}

// SubmitBWS submits a BWS reservation task to a worker.
// Accepts a JSON document with the fields defined in BWSSubmitInput.
// Returns the attempt ID that can be used with Status/Logs/Stop.
func (s *ClusterService) SubmitBWS(inputJSON string) (string, error) {
	var input BWSSubmitInput
	if err := json.Unmarshal([]byte(inputJSON), &input); err != nil {
		return "", fmt.Errorf("invalid BWS submit input: %w", err)
	}
	if input.AccountID == "" || input.WorkerID == "" {
		return "", fmt.Errorf("accountId and workerId are required")
	}
	if input.ActivityID <= 0 || input.TicketNo == "" || input.ReserveTime <= 0 {
		return "", fmt.Errorf("activityId, ticketNo, and reserveTime are required")
	}
	// Ticket number only needs last 4 digits
	if len(input.TicketNo) > 4 {
		input.TicketNo = input.TicketNo[len(input.TicketNo)-4:]
	}

	ctx := context.Background()
	account, err := s.repository.Account(ctx, input.AccountID)
	if err != nil {
		return "", fmt.Errorf("account %s not found: %w", input.AccountID, err)
	}
	node, err := s.repository.Worker(ctx, input.WorkerID)
	if err != nil {
		return "", fmt.Errorf("worker %s not found: %w", input.WorkerID, err)
	}
	intentID := randomClusterID("bws-intent")
	attemptID := randomClusterID("bws-attempt")

	startDelayMS := int64(input.StartDelayMs)
	loopDelayMS := int64(input.LoopDelayMs)
	if loopDelayMS <= 0 {
		loopDelayMS = 50
	}

	spec := domain.ExecutionSpec{
		AttemptID:        attemptID,
		IntentID:         intentID,
		StartMode:        domain.StartScheduled,
		StartAt:          time.Unix(input.ReserveTime, 0),
		Deadline:         time.Unix(input.ReserveTime, 0).Add(10 * time.Minute),
		IntervalMS:       loopDelayMS,
		StartDelayMS:     startDelayMS,
		Credentials:      account.Credentials,
		TaskType:         domain.TaskTypeBWS,
		BWSActivityID:    input.ActivityID,
		BWSTicketNo:      input.TicketNo,
		BWSActivityTitle: input.ActivityTitle,
		BWSReserveTime:   input.ReserveTime,
		BWSReserveDate:   input.ReserveDate,
	}

	// Persist metadata — bwsMeta is the authoritative store for BWS tasks.
	s.mu.Lock()
	s.bwsMeta[attemptID] = input
	s.mu.Unlock()
	s.saveBWSMetadata()
	log.Printf("[BWS] SubmitBWS: metadata saved attemptID=%s activityTitle=%s reserveDate=%s", attemptID, input.ActivityTitle, input.ReserveDate)

	// Send to worker.
	if err := s.client.Submit(ctx, node, spec); err != nil {
		log.Printf("[BWS] SubmitBWS: worker rejected attemptID=%s err=%v", attemptID, err)
		return "", fmt.Errorf("submit BWS task: %w", err)
	}

	log.Printf("[BWS] SubmitBWS: sent to worker successfully attemptID=%s", attemptID)
	return attemptID, nil
}

// ── BWS metadata persistence ────────────────────────────────────

// loadBWSMetadata loads previously saved BWS submission metadata from disk.
func (s *ClusterService) loadBWSMetadata() {
	data, err := os.ReadFile(bwsMetaFile)
	if err != nil {
		log.Printf("[BWS] loadBWSMetadata: no meta file yet (%v)", err)
		return
	}
	var meta map[string]BWSSubmitInput
	if err := json.Unmarshal(data, &meta); err != nil {
		log.Printf("[BWS] loadBWSMetadata: unmarshal failed: %v", err)
		return
	}
	s.mu.Lock()
	s.bwsMeta = meta
	s.mu.Unlock()
	log.Printf("[BWS] loadBWSMetadata: loaded %d entries from %s", len(meta), bwsMetaFile)
}

// saveBWSMetadata writes the current bwsMeta map to disk.
func (s *ClusterService) saveBWSMetadata() {
	s.mu.RLock()
	data, err := json.MarshalIndent(s.bwsMeta, "", "  ")
	s.mu.RUnlock()
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(bwsMetaFile), 0755); err != nil {
		return
	}
	_ = os.WriteFile(bwsMetaFile, data, 0644)
}

// ── BWS task management (Status / Logs / Stop) ─────────────────

// BWSLogEntry is a frontend-friendly log entry.
type BWSLogEntry struct {
	Sequence  int64  `json:"sequence"`
	Time      string `json:"time"`
	Stage     string `json:"stage"`
	Message   string `json:"message"`
	Code      int    `json:"code"`
	Retryable bool   `json:"retryable"`
}

// BWSStatusResult is returned by GetBWSTaskStatus.
type BWSStatusResult struct {
	AttemptID string `json:"attemptId"`
	SpecHash  string `json:"specHash"`
	State     string `json:"state"`
	Success   bool   `json:"success"`
	OrderID   string `json:"orderId,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Message   string `json:"message,omitempty"`
	Retryable bool   `json:"retryable"`
}

// GetBWSTaskStatus returns the current status of a BWS task on a worker.
func (s *ClusterService) GetBWSTaskStatus(workerID string, attemptID string) (*BWSStatusResult, error) {
	ctx := context.Background()
	node, err := s.repository.Worker(ctx, workerID)
	if err != nil {
		return nil, fmt.Errorf("worker %s not found: %w", workerID, err)
	}
	status, err := s.client.Status(ctx, node, attemptID)
	if err != nil {
		return nil, err
	}
	return &BWSStatusResult{
		AttemptID: status.Result.AttemptID,
		SpecHash:  status.Result.SpecHash,
		State:     string(status.State),
		Success:   status.Result.Success,
		OrderID:   status.Result.OrderID,
		Reason:    string(status.Result.Reason),
		Message:   status.Result.Message,
		Retryable: status.Result.Retryable,
	}, nil
}

// GetBWSTaskLogs returns the log entries for a BWS task on a worker.
func (s *ClusterService) GetBWSTaskLogs(workerID string, attemptID string) ([]BWSLogEntry, error) {
	ctx := context.Background()
	node, err := s.repository.Worker(ctx, workerID)
	if err != nil {
		return nil, fmt.Errorf("worker %s not found: %w", workerID, err)
	}
	logs, err := s.client.Logs(ctx, node, attemptID)
	if err != nil {
		return nil, err
	}
	entries := make([]BWSLogEntry, 0, len(logs))
	for _, e := range logs {
		entries = append(entries, BWSLogEntry{
			Sequence:  e.Sequence,
			Time:      e.Time.Format(time.RFC3339),
			Stage:     e.Stage,
			Message:   e.Message,
			Code:      e.Code,
			Retryable: e.Retryable,
		})
	}
	return entries, nil
}

// StopBWSTask stops a running BWS task on a worker.
func (s *ClusterService) StopBWSTask(workerID string, attemptID string) error {
	ctx := context.Background()
	node, err := s.repository.Worker(ctx, workerID)
	if err != nil {
		return fmt.Errorf("worker %s not found: %w", workerID, err)
	}
	return s.client.Stop(ctx, node, attemptID)
}

// ── BWS entry listing ──────────────────────────────────────────

// BWSListEntry is a frontend-friendly BWS reservation entry
// that combines attempt state with submission metadata.
type BWSListEntry struct {
	ID            string `json:"id"`
	AccountID     string `json:"accountId"`
	WorkerID      string `json:"workerId"`
	ActivityID    int    `json:"activityId"`
	ActivityTitle string `json:"activityTitle"`
	TicketNo      string `json:"ticketNo"`
	ReserveTime   int64  `json:"reserveTime"`
	ReserveDate   string `json:"reserveDate"`
	StartDelayMs  int    `json:"startDelayMs"`
	LoopDelayMs   int    `json:"loopDelayMs"`
	Status        string `json:"status"`
	Message       string `json:"message"`
}

// ListBWSEntries returns ALL BWS reservation entries across all workers.
// It builds the list from the persisted bwsMeta cache, enriched with
// live state from the dispatcher / DB when available.
func (s *ClusterService) ListBWSEntries() ([]BWSListEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Collect attempt states from dispatcher + DB for enrichment.
	attemptStates := make(map[string]struct {
		state   domain.AttemptState
		message string
	})

	for _, a := range s.dispatcher.Attempts() {
		attemptStates[a.ID] = struct {
			state   domain.AttemptState
			message string
		}{state: a.State, message: a.Result.Message}
	}

	ctx := context.Background()
	dbAttempts, _ := s.repository.ListAttempts(ctx)
	for _, a := range dbAttempts {
		if _, exists := attemptStates[a.ID]; !exists {
			attemptStates[a.ID] = struct {
				state   domain.AttemptState
				message string
			}{state: a.State, message: a.Result.Message}
		}
	}

	// Build result from bwsMeta (the authoritative list of BWS tasks).
	var result []BWSListEntry
	for attemptID, meta := range s.bwsMeta {
		entry := BWSListEntry{
			ID:            attemptID,
			AccountID:     meta.AccountID,
			WorkerID:      meta.WorkerID,
			Status:        string(domain.AttemptWaiting),
			ActivityID:    meta.ActivityID,
			ActivityTitle: meta.ActivityTitle,
			TicketNo:      meta.TicketNo,
			ReserveTime:   meta.ReserveTime,
			ReserveDate:   meta.ReserveDate,
			StartDelayMs:  meta.StartDelayMs,
			LoopDelayMs:   meta.LoopDelayMs,
		}
		if st, ok := attemptStates[attemptID]; ok {
			entry.Status = string(st.state)
			entry.Message = st.message
		}
		result = append(result, entry)
	}

	log.Printf("[BWS] ListBWSEntries: bwsMeta=%d attemptStates=%d result=%d",
		len(s.bwsMeta), len(attemptStates), len(result))

	return result, nil
}

// DeleteBWSEntry removes a BWS entry from the metadata cache.
// The entry will stop appearing in ListBWSEntries immediately.
// Call StopBWSTask first if the task is still running on a worker.
func (s *ClusterService) DeleteBWSEntry(attemptID string) error {
	s.mu.Lock()
	delete(s.bwsMeta, attemptID)
	s.mu.Unlock()
	s.saveBWSMetadata()
	log.Printf("[BWS] DeleteBWSEntry: removed %s", attemptID)
	return nil
}
